package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
)

func TestStateRoundTripAndTamper(t *testing.T) {
	s := &Service{secret: []byte("k")}
	raw, err := s.signState(oidcState{Nonce: "n1", RedirectURI: "http://x/cb", IssuedAt: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.verifyState(raw)
	if err != nil || st.Nonce != "n1" || st.RedirectURI != "http://x/cb" {
		t.Fatalf("round trip: %+v %v", st, err)
	}
	payload, sig, _ := strings.Cut(raw, ".")
	forged, _ := json.Marshal(oidcState{Nonce: "n1", RedirectURI: "http://evil/cb", IssuedAt: time.Now().Unix()})
	if _, err := s.verifyState(base64.RawURLEncoding.EncodeToString(forged) + "." + sig); err == nil {
		t.Fatal("forged payload accepted")
	}
	if _, err := (&Service{secret: []byte("other")}).verifyState(payload + "." + sig); err == nil {
		t.Fatal("state signed with another secret accepted")
	}
	old, _ := s.signState(oidcState{Nonce: "n", RedirectURI: "http://x/cb", IssuedAt: time.Now().Add(-11 * time.Minute).Unix()})
	if _, err := s.verifyState(old); err == nil {
		t.Fatal("expired state accepted")
	}
}

func newTestService(t *testing.T, tweak func(*config.Auth)) (*Service, *postgres.Store) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || testing.Short() {
		t.Skip("set TEST_DATABASE_URL to run integration tests")
	}
	ctx := context.Background()
	st, err := postgres.Open(ctx, config.DB{DSN: dsn, MaxConns: 4, MinConns: 1, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	cfg := config.Auth{AccessTTL: time.Hour, RefreshTTL: 2 * time.Hour, Registration: config.RegistrationOpen, PasswordMinLength: 8,
		OIDC: config.OIDC{Provider: "oidc", Scopes: []string{"openid", "email", "profile"}}}
	if tweak != nil {
		tweak(&cfg)
	}
	s, err := New(ctx, st, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func cleanupEmail(t *testing.T, st *postgres.Store, email string) {
	t.Cleanup(func() { st.Pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email) })
}

func TestPasswordFlow(t *testing.T) {
	s, st := newTestService(t, nil)
	ctx := context.Background()
	email := fmt.Sprintf("auth-%s@bepaylot.local", uuid.NewString()[:8])
	cleanupEmail(t, st, email)

	if _, err := s.Register(ctx, email, "", "short"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("short password: %v", err)
	}
	reg, err := s.Register(ctx, strings.ToUpper(email), "", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if reg.User.Email != email || !reg.User.HasPassword || reg.User.Name == "" {
		t.Fatalf("registered user: %+v", reg.User)
	}
	if _, err := s.Register(ctx, email, "x", "correct horse"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := s.Login(ctx, email, "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	lg, err := s.Login(ctx, email, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Authenticate(ctx, lg.AccessToken); err != nil || u.ID != reg.User.ID {
		t.Fatalf("authenticate: %v", err)
	}
	if _, err := s.Authenticate(ctx, lg.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("refresh token accepted as access token")
	}

	// Refresh rotates: the old refresh token works once.
	rf, err := s.Refresh(ctx, lg.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(ctx, lg.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reused refresh token: %v", err)
	}

	// Password change revokes every session and hands out a new pair.
	if _, err := s.ChangePassword(ctx, rf.User, "nope nope", "new password 1"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong current password: %v", err)
	}
	cp, err := s.ChangePassword(ctx, rf.User, "correct horse", "new password 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, rf.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("token issued before the password change still works")
	}
	if _, err := s.Authenticate(ctx, cp.AccessToken); err != nil {
		t.Fatal(err)
	}

	// Logout ends every session.
	if err := s.Logout(ctx, cp.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, cp.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("token still valid after logout")
	}
	if _, err := s.Refresh(ctx, cp.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("refresh still valid after logout")
	}
}

func TestRegistrationClosedAndSecretStable(t *testing.T) {
	s, st := newTestService(t, func(c *config.Auth) { c.Registration = config.RegistrationClosed })
	if _, err := s.Register(context.Background(), "closed@bepaylot.local", "", "correct horse"); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed registration: %v", err)
	}
	s2, err := New(context.Background(), st, s.cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(s2.secret) != string(s.secret) {
		t.Fatal("generated JWT secret changed between instances")
	}
}

// fakeIdP is a minimal OIDC provider: discovery, JWKS and a token endpoint
// that returns an RS256 ID token for the last authorization request.
type fakeIdP struct {
	srv           *httptest.Server
	key           *rsa.PrivateKey
	nonce         string
	sub, email    string
	emailVerified bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
			"jwks_uri": p.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good-code" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": p.srv.URL, "aud": "bepaylot-test", "sub": p.sub, "email": p.email, "email_verified": p.emailVerified,
			"name": "OIDC User", "nonce": p.nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
		})
		tok.Header["kid"] = "k1"
		idt, _ := tok.SignedString(key)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idt, "expires_in": 60})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func TestOIDCFlow(t *testing.T) {
	idp := newFakeIdP(t)
	s, st := newTestService(t, func(c *config.Auth) {
		c.OIDC.Enabled, c.OIDC.IssuerURL, c.OIDC.ClientID, c.OIDC.ClientSecret = true, idp.srv.URL, "bepaylot-test", "secret"
	})
	ctx := context.Background()
	idp.sub, idp.email, idp.emailVerified = uuid.NewString(), fmt.Sprintf("oidc-%s@bepaylot.local", uuid.NewString()[:8]), true
	cleanupEmail(t, st, idp.email)

	start := func() (state, nonce string) {
		authURL, nonce, err := s.OIDCStart(ctx, "http://app.local/v1/auth/oidc/callback", "http://web.local/login")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(authURL)
		if u.Query().Get("nonce") != nonce || u.Query().Get("client_id") != "bepaylot-test" {
			t.Fatalf("authorization url: %s", authURL)
		}
		idp.nonce = nonce
		if rt := s.OIDCReturnTo(u.Query().Get("state")); rt != "http://web.local/login" {
			t.Fatalf("return_to from state: %q", rt)
		}
		return u.Query().Get("state"), nonce
	}

	state, nonce := start()
	if _, _, err := s.OIDCCallback(ctx, "good-code", state, "other-browser"); err == nil {
		t.Fatal("callback without the matching nonce cookie accepted")
	}
	tk, created, err := s.OIDCCallback(ctx, "good-code", state, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !created || tk.User.Email != idp.email || tk.User.AuthProvider != "oidc" || tk.User.HasPassword {
		t.Fatalf("provisioned user: created=%v %+v", created, tk.User)
	}
	if _, err := s.Authenticate(ctx, tk.AccessToken); err != nil {
		t.Fatal(err)
	}

	// Second sign-in finds the same user by subject.
	state, nonce = start()
	tk2, created, err := s.OIDCCallback(ctx, "good-code", state, nonce)
	if err != nil || created || tk2.User.ID != tk.User.ID {
		t.Fatalf("second sign-in: created=%v err=%v", created, err)
	}

	// An unverified email is refused.
	idp.sub, idp.emailVerified = uuid.NewString(), false
	state, nonce = start()
	var ue *OIDCUserError
	if _, _, err := s.OIDCCallback(ctx, "good-code", state, nonce); !errors.As(err, &ue) || ue.Code != "email_not_verified" {
		t.Fatalf("unverified email: %v", err)
	}
}
