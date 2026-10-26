package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDC sign-in follows WeKnora's redirect flow (authorization code, backend
// exchanges the code):
//
//  1. GET /v1/auth/oidc/start → a random nonce goes into an HttpOnly cookie
//     and, with the callback URL and the frontend page to return to, into a
//     signed "state"; 302 to the provider. Every flow uses PKCE (S256), so a
//     public client (client id only, no secret, e.g. an internal Keycloak
//     client) works; the verifier is derived from the nonce and the server
//     secret, so it needs no storage.
//  2. The provider redirects to GET /v1/auth/oidc/callback?code&state. The
//     state signature, age and nonce cookie are checked (the cookie binds the
//     flow to this browser), the code is exchanged, the ID token verified
//     (issuer, audience, nonce) and the user found or created by subject, then
//     by email.
//  3. The browser is sent to the frontend (return_to from the state, else
//     auth.oidc.frontend_url) with #oidc_result=<base64url JSON
//     of the same body as /v1/auth/login> or #oidc_error=<code>.

// oidcStateMaxAge bounds the time between start and callback.
const oidcStateMaxAge = 10 * time.Minute

type oidcProvider struct {
	verifier *oidc.IDTokenVerifier
	provider *oidc.Provider
}

// OIDCEnabled reports whether the login page shows the OIDC button.
func (s *Service) OIDCEnabled() bool { return s.cfg.OIDC.Enabled }

// OIDCDisplayName labels the OIDC button.
func (s *Service) OIDCDisplayName() string { return s.cfg.OIDC.DisplayName }

// OIDCFrontendURL is where the callback sends the browser.
func (s *Service) OIDCFrontendURL() string {
	if s.cfg.OIDC.FrontendURL != "" {
		return s.cfg.OIDC.FrontendURL
	}
	return "/login"
}

// OIDCRedirectURL is the callback URL: the configured one, or fallback
// (derived by the handler from the request).
func (s *Service) OIDCRedirectURL(fallback string) string {
	if s.cfg.OIDC.RedirectURL != "" {
		return s.cfg.OIDC.RedirectURL
	}
	return fallback
}

// provider discovers the issuer once; a failed discovery is retried on the
// next sign-in instead of failing startup.
func (s *Service) provider(ctx context.Context) (*oidcProvider, error) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oidcProv != nil {
		return s.oidcProv, nil
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	p, err := discover(dctx, s.cfg.OIDC.IssuerURL)
	if err != nil {
		return nil, err
	}
	s.oidcProv = &oidcProvider{provider: p, verifier: p.Verifier(&oidc.Config{ClientID: s.cfg.OIDC.ClientID})}
	return s.oidcProv, nil
}

// issuerCandidates turns the URL an admin pastes for the provider into the
// issuer URLs to try for discovery, most likely first. Besides an exact
// issuer it accepts a Keycloak realm URL in its usual shapes: with or
// without the legacy /auth prefix or a trailing /auth, "realm" for
// "realms", an endpoint under the realm (…/protocol/openid-connect/auth) or
// the discovery URL itself.
func issuerCandidates(raw string) []string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	u = strings.TrimSuffix(u, "/.well-known/openid-configuration")
	if i := strings.Index(u, "/protocol/openid-connect"); i >= 0 {
		u = u[:i]
	}
	var out []string
	seen := map[string]bool{}
	add := func(c string) {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	bases := []string{u}
	if t := strings.TrimSuffix(u, "/auth"); t != u {
		bases = append(bases, t)
	}
	for _, b := range bases {
		add(b)
		if strings.Contains(b, "/realm/") {
			add(strings.Replace(b, "/realm/", "/realms/", 1))
		}
	}
	// Keycloak before 17 serves realms under /auth.
	for _, c := range append([]string(nil), out...) {
		if i := strings.Index(c, "/realms/"); i >= 0 && !strings.HasSuffix(c[:i], "/auth") {
			add(c[:i] + "/auth" + c[i:])
		}
	}
	return out
}

// discover finds the provider from the configured URL (issuerCandidates).
// The issuer the provider names in its discovery document is trusted even
// when it differs from the URL used to reach it (an internal Keycloak
// reached by another host name than its public one); ID tokens are then
// checked against that issuer.
func discover(ctx context.Context, raw string) (*oidc.Provider, error) {
	var errs []string
	for _, c := range issuerCandidates(raw) {
		issuer, err := discoveryIssuer(ctx, c)
		if err == nil {
			var p *oidc.Provider
			if p, err = oidc.NewProvider(oidc.InsecureIssuerURLContext(ctx, issuer), c); err == nil {
				return p, nil
			}
		}
		errs = append(errs, c+": "+err.Error())
	}
	return nil, fmt.Errorf("oidc discovery failed for %s (%s)", raw, strings.Join(errs, "; "))
}

// discoveryIssuer reads the issuer of a discovery document.
func discoveryIssuer(ctx context.Context, base string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery answered %s", resp.Status)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil || doc.Issuer == "" {
		return "", fmt.Errorf("not a discovery document")
	}
	return doc.Issuer, nil
}

// pkceVerifier is the PKCE code verifier of a flow: an HMAC of its nonce,
// so the callback recomputes it from the state and the provider never sees
// it before the code exchange.
func (s *Service) pkceVerifier(nonce string) string {
	m := hmac.New(sha256.New, append([]byte("oidc-pkce:"), s.secret...))
	m.Write([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Service) oauth2Config(p *oidcProvider, redirectURL string) *oauth2.Config {
	ep := p.provider.Endpoint()
	if s.cfg.OIDC.ClientSecret == "" {
		// Public client: client_id goes in the form body, no Basic auth.
		ep.AuthStyle = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{
		ClientID:     s.cfg.OIDC.ClientID,
		ClientSecret: s.cfg.OIDC.ClientSecret,
		Endpoint:     ep,
		RedirectURL:  redirectURL,
		Scopes:       s.cfg.OIDC.Scopes,
	}
}

// OIDCStart returns the provider's authorization URL and the nonce the
// handler must put in the binding cookie. returnTo (already checked by the
// caller against trusted origins, may be empty) is where the callback sends
// the browser.
func (s *Service) OIDCStart(ctx context.Context, redirectURL, returnTo string) (authURL, nonce string, err error) {
	if !s.OIDCEnabled() {
		return "", "", ErrOIDCDisabled
	}
	p, err := s.provider(ctx)
	if err != nil {
		return "", "", err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	nonce = base64.RawURLEncoding.EncodeToString(buf)
	state, err := s.signState(oidcState{Nonce: nonce, RedirectURI: redirectURL, ReturnTo: returnTo, IssuedAt: time.Now().Unix()})
	if err != nil {
		return "", "", err
	}
	return s.oauth2Config(p, redirectURL).AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(s.pkceVerifier(nonce))), nonce, nil
}

// OIDCUserError is an OIDC failure whose Code is safe to show in the UI.
type OIDCUserError struct {
	Code string
	Err  error
}

func (e *OIDCUserError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *OIDCUserError) Unwrap() error { return e.Err }

func oidcErr(code string, err error) error { return &OIDCUserError{Code: code, Err: err} }

// OIDCCallback completes the flow and signs the user in. created reports a
// newly provisioned account.
func (s *Service) OIDCCallback(ctx context.Context, code, rawState, cookieNonce string) (t Tokens, created bool, err error) {
	if !s.OIDCEnabled() {
		return Tokens{}, false, oidcErr("oidc_disabled", ErrOIDCDisabled)
	}
	st, err := s.verifyState(rawState)
	if err != nil {
		return Tokens{}, false, oidcErr("invalid_state", err)
	}
	if cookieNonce == "" || !hmac.Equal([]byte(cookieNonce), []byte(st.Nonce)) {
		return Tokens{}, false, oidcErr("invalid_state", errors.New("nonce cookie missing or mismatched"))
	}
	if code == "" {
		return Tokens{}, false, oidcErr("missing_code", errors.New("no authorization code"))
	}
	p, err := s.provider(ctx)
	if err != nil {
		return Tokens{}, false, oidcErr("provider_unavailable", err)
	}
	cfg := s.oauth2Config(p, st.RedirectURI)
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(s.pkceVerifier(st.Nonce)))
	if err != nil {
		return Tokens{}, false, oidcErr("exchange_failed", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return Tokens{}, false, oidcErr("exchange_failed", errors.New("no id_token in token response"))
	}
	idt, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return Tokens{}, false, oidcErr("invalid_id_token", err)
	}
	if !hmac.Equal([]byte(idt.Nonce), []byte(st.Nonce)) {
		return Tokens{}, false, oidcErr("invalid_id_token", errors.New("id_token nonce mismatch"))
	}

	var c idClaims
	if err := idt.Claims(&c); err != nil {
		return Tokens{}, false, oidcErr("invalid_id_token", err)
	}
	// Providers that keep email/name out of the ID token serve them from the
	// userinfo endpoint.
	if c.Email == "" {
		if ui, err := p.provider.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var more idClaims
			if ui.Claims(&more) == nil {
				c.merge(more)
			}
			if c.Email == "" {
				c.Email = ui.Email
			}
		}
	}
	email, err := normalizeEmail(c.Email)
	if err != nil {
		return Tokens{}, false, oidcErr("missing_email", errors.New("provider returned no valid email (request the email scope)"))
	}
	// The email links the identity to an existing account, so an address the
	// provider says it has not verified is refused.
	if c.EmailVerified != nil && !*c.EmailVerified {
		return Tokens{}, false, oidcErr("email_not_verified", fmt.Errorf("email %s is not verified at the provider", email))
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.PreferredUsername)
	}
	u, created, err := s.st.Users.UpsertOIDC(ctx, s.cfg.OIDC.Provider, idt.Subject, email, name)
	if err != nil {
		return Tokens{}, false, err
	}
	if !u.IsActive {
		return Tokens{}, false, oidcErr("account_disabled", ErrDisabled)
	}
	if created {
		s.log.Info("user provisioned via oidc", "user_id", u.ID, "provider", s.cfg.OIDC.Provider)
	}
	t, err = s.signIn(ctx, u)
	return t, created, err
}

type idClaims struct {
	Email             string `json:"email"`
	EmailVerified     *bool  `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
}

func (c *idClaims) merge(o idClaims) {
	if c.Email == "" {
		c.Email, c.EmailVerified = o.Email, o.EmailVerified
	}
	if c.Name == "" {
		c.Name = o.Name
	}
	if c.PreferredUsername == "" {
		c.PreferredUsername = o.PreferredUsername
	}
}

// oidcState is carried through the provider as the OAuth2 state:
// base64url(JSON) "." base64url(HMAC-SHA256), like WeKnora's.
type oidcState struct {
	Nonce       string `json:"nonce"`
	RedirectURI string `json:"redirect_uri"`
	ReturnTo    string `json:"return_to,omitempty"`
	IssuedAt    int64  `json:"iat"`
}

// OIDCReturnTo is the frontend page recorded in a valid state, else the
// configured frontend URL.
func (s *Service) OIDCReturnTo(rawState string) string {
	if st, err := s.verifyState(rawState); err == nil && st.ReturnTo != "" {
		return st.ReturnTo
	}
	return s.OIDCFrontendURL()
}

func (s *Service) stateMAC(payload []byte) []byte {
	m := hmac.New(sha256.New, append([]byte("oidc-state:"), s.secret...))
	m.Write(payload)
	return m.Sum(nil)
}

func (s *Service) signState(st oidcState) (string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(s.stateMAC(raw)), nil
}

func (s *Service) verifyState(raw string) (oidcState, error) {
	var st oidcState
	payloadB64, sigB64, ok := strings.Cut(raw, ".")
	if !ok {
		return st, errors.New("malformed state")
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(payloadB64)
	sig, err2 := base64.RawURLEncoding.DecodeString(sigB64)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, s.stateMAC(payload)) {
		return st, errors.New("state signature mismatch")
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		return st, err
	}
	age := time.Since(time.Unix(st.IssuedAt, 0))
	if st.Nonce == "" || st.RedirectURI == "" || age > oidcStateMaxAge || age < -time.Minute {
		return st, errors.New("state expired or incomplete")
	}
	return st, nil
}
