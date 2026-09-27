// Package auth signs users in, modelled on WeKnora's user service: email +
// password accounts (bcrypt) and OIDC sign-in both end in a pair of HS256
// JWTs — a short-lived access token and a longer-lived refresh token. Every
// issued token is recorded (by hash) in auth_tokens, so a token is accepted
// only while its row is live: logout and password changes revoke all of a
// user's tokens, and a refresh token is rotated on use. API keys (x-api-key)
// stay available for scripts and are checked by the middleware directly.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Errors surfaced to the API.
var (
	ErrBadRequest         = errors.New("bad request")
	ErrInvalidCredentials = errors.New("email hoặc mật khẩu không đúng")
	ErrEmailTaken         = errors.New("email đã được đăng ký")
	ErrRegistrationClosed = errors.New("đăng ký tài khoản đang tắt (auth.registration=closed)")
	ErrDisabled           = errors.New("tài khoản đã bị khoá")
	ErrInvalidToken       = errors.New("token không hợp lệ hoặc đã hết hạn")
	ErrWrongPassword      = errors.New("mật khẩu hiện tại không đúng")
	ErrOIDCDisabled       = errors.New("OIDC chưa được bật (auth.oidc.enabled)")
)

// Token kinds, stored in auth_tokens.token_type and the JWT "typ" claim.
const (
	kindAccess  = "access"
	kindRefresh = "refresh"
)

// Tokens is the result of a successful sign-in, refresh or password change.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time // of the access token
	User         types.User
}

// Service implements sign-in.
type Service struct {
	st     *postgres.Store
	cfg    config.Auth
	secret []byte
	log    *slog.Logger

	oidcMu   sync.Mutex
	oidcProv *oidcProvider // discovered lazily, see provider()
}

// New builds the service. With an empty auth.jwt_secret the signing key is
// generated once and kept in the database.
func New(ctx context.Context, st *postgres.Store, cfg config.Auth, log *slog.Logger) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	secret := cfg.JWTSecret
	if secret == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		var err error
		if secret, err = st.Tokens.Secret(ctx, "jwt_secret", base64.RawStdEncoding.EncodeToString(buf)); err != nil {
			return nil, err
		}
	}
	return &Service{st: st, cfg: cfg, secret: []byte(secret), log: log.With("module", "auth")}, nil
}

// RegistrationOpen reports whether the login page may offer sign-up.
func (s *Service) RegistrationOpen() bool { return s.cfg.Registration == config.RegistrationOpen }

// PasswordMinLength is the shortest accepted password.
func (s *Service) PasswordMinLength() int { return s.cfg.PasswordMinLength }

// Register creates a local account and signs it in.
func (s *Service) Register(ctx context.Context, email, name, password string) (Tokens, error) {
	if !s.RegistrationOpen() {
		return Tokens{}, ErrRegistrationClosed
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return Tokens{}, err
	}
	if err := s.checkPassword(password); err != nil {
		return Tokens{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Tokens{}, err
	}
	u, err := s.st.Users.Register(ctx, email, name, string(hash))
	if errors.Is(err, postgres.ErrEmailTaken) {
		return Tokens{}, ErrEmailTaken
	}
	if err != nil {
		return Tokens{}, err
	}
	s.log.Info("user registered", "user_id", u.ID)
	return s.signIn(ctx, u)
}

// Login checks an email + password.
func (s *Service) Login(ctx context.Context, email, password string) (Tokens, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return Tokens{}, ErrInvalidCredentials
	}
	u, hash, err := s.st.Users.Credentials(ctx, email)
	if errors.Is(err, postgres.ErrNotFound) {
		// Same cost as a real check, so response time does not reveal
		// which emails exist.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return Tokens{}, ErrInvalidCredentials
	}
	if err != nil {
		return Tokens{}, err
	}
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Tokens{}, ErrInvalidCredentials
	}
	if !u.IsActive {
		return Tokens{}, ErrDisabled
	}
	return s.signIn(ctx, u)
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("bepaylot-timing-equaliser"), bcrypt.DefaultCost)

// Authenticate resolves an access token to its user.
func (s *Service) Authenticate(ctx context.Context, token string) (types.User, error) {
	uid, err := s.verify(ctx, token, kindAccess)
	if err != nil {
		return types.User{}, err
	}
	u, err := s.st.Users.Get(ctx, uid)
	if err != nil {
		return types.User{}, ErrInvalidToken
	}
	if !u.IsActive {
		return types.User{}, ErrDisabled
	}
	return u, nil
}

// Refresh exchanges a refresh token for a new pair. The old refresh token is
// revoked, so each one works once.
func (s *Service) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	uid, err := s.verify(ctx, refresh, kindRefresh)
	if err != nil {
		return Tokens{}, err
	}
	if ok, err := s.st.Tokens.Revoke(ctx, refresh); err != nil {
		return Tokens{}, err
	} else if !ok {
		return Tokens{}, ErrInvalidToken
	}
	u, err := s.st.Users.Get(ctx, uid)
	if err != nil {
		return Tokens{}, ErrInvalidToken
	}
	if !u.IsActive {
		return Tokens{}, ErrDisabled
	}
	return s.issue(ctx, u)
}

// Logout revokes every token of the user the (possibly expired) token
// belongs to, as WeKnora does: signing out ends all sessions.
func (s *Service) Logout(ctx context.Context, token string) error {
	claims, err := s.parse(token, jwt.WithoutClaimsValidation())
	if err != nil {
		return ErrInvalidToken
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		return ErrInvalidToken
	}
	return s.st.Tokens.RevokeUser(ctx, uid)
}

// ChangePassword sets a new password, revokes every session and returns a
// fresh pair for the caller. Accounts without a local password (OIDC or
// seeded) may set one without the current password.
func (s *Service) ChangePassword(ctx context.Context, u types.User, current, next string) (Tokens, error) {
	if err := s.checkPassword(next); err != nil {
		return Tokens{}, err
	}
	hash, err := s.st.Users.PasswordHash(ctx, u.ID)
	if err != nil {
		return Tokens{}, err
	}
	if hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return Tokens{}, ErrWrongPassword
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return Tokens{}, err
	}
	if err := s.st.Users.SetPassword(ctx, u.ID, string(newHash)); err != nil {
		return Tokens{}, err
	}
	if err := s.st.Tokens.RevokeUser(ctx, u.ID); err != nil {
		return Tokens{}, err
	}
	u.HasPassword = true
	return s.issue(ctx, u)
}

func (s *Service) signIn(ctx context.Context, u types.User) (Tokens, error) {
	s.st.Users.TouchLogin(ctx, u.ID)
	if n, err := s.st.Tokens.PurgeExpired(ctx); err == nil && n > 0 {
		s.log.Debug("purged expired tokens", "n", n)
	}
	return s.issue(ctx, u)
}

func (s *Service) issue(ctx context.Context, u types.User) (Tokens, error) {
	now := time.Now()
	access, accessExp, err := s.sign(u, kindAccess, now, s.cfg.AccessTTL)
	if err != nil {
		return Tokens{}, err
	}
	refresh, refreshExp, err := s.sign(u, kindRefresh, now, s.cfg.RefreshTTL)
	if err != nil {
		return Tokens{}, err
	}
	if err := s.st.Tokens.Record(ctx, u.ID, access, kindAccess, accessExp); err != nil {
		return Tokens{}, err
	}
	if err := s.st.Tokens.Record(ctx, u.ID, refresh, kindRefresh, refreshExp); err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, RefreshToken: refresh, ExpiresAt: accessExp, User: u}, nil
}

type claims struct {
	Email string `json:"email,omitempty"`
	Type  string `json:"typ"`
	jwt.RegisteredClaims
}

func (s *Service) sign(u types.User, kind string, now time.Time, ttl time.Duration) (string, time.Time, error) {
	exp := now.Add(ttl)
	c := claims{
		Email: u.Email,
		Type:  kind,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID.String(),
			Issuer:    "bepaylot",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(), // keeps two tokens issued in the same second distinct
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	return tok, exp, err
}

func (s *Service) parse(token string, opts ...jwt.ParserOption) (*claims, error) {
	var c claims
	opts = append(opts, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer("bepaylot"))
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return s.secret, nil }, opts...)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// verify checks signature, expiry and kind, then that the token is still
// recorded as live.
func (s *Service) verify(ctx context.Context, token, kind string) (uuid.UUID, error) {
	c, err := s.parse(token)
	if err != nil || c.Type != kind {
		return uuid.Nil, ErrInvalidToken
	}
	uid, err := s.st.Tokens.Active(ctx, token, kind)
	if errors.Is(err, postgres.ErrNotFound) {
		return uuid.Nil, ErrInvalidToken
	}
	if err != nil {
		return uuid.Nil, err
	}
	if uid.String() != c.Subject {
		return uuid.Nil, ErrInvalidToken
	}
	return uid, nil
}

func (s *Service) checkPassword(p string) error {
	if len([]rune(p)) < s.cfg.PasswordMinLength {
		return fmt.Errorf("%w: mật khẩu cần ít nhất %d ký tự", ErrBadRequest, s.cfg.PasswordMinLength)
	}
	if len(p) > 72 { // bcrypt's limit
		return fmt.Errorf("%w: mật khẩu dài tối đa 72 byte", ErrBadRequest)
	}
	return nil
}

func normalizeEmail(e string) (string, error) {
	e = strings.ToLower(strings.TrimSpace(e))
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e {
		return "", fmt.Errorf("%w: email không hợp lệ", ErrBadRequest)
	}
	return e, nil
}
