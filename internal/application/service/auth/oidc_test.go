package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/thanhenti/bepaylot/internal/config"
)

func TestIssuerCandidates(t *testing.T) {
	got := issuerCandidates("http://kc.local/realm/internal/auth/")
	want := []string{
		"http://kc.local/realm/internal/auth", "http://kc.local/realms/internal/auth",
		"http://kc.local/realm/internal", "http://kc.local/realms/internal",
		"http://kc.local/auth/realms/internal/auth", "http://kc.local/auth/realms/internal",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := issuerCandidates("https://kc/auth/realms/main/protocol/openid-connect/auth"); got[0] != "https://kc/auth/realms/main" || len(got) != 1 {
		t.Fatalf("endpoint URL: %q", got)
	}
}

// An internal Keycloak public client: the admin pastes a realm URL in a
// loose shape, the discovery names another (public) host as issuer, and
// there is no client secret. The authorization URL uses PKCE S256 with the
// verifier the callback recomputes.
func TestOIDCStartKeycloakPublicClient(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realms/internal/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		realm := srv.URL + "/realms/internal/protocol/openid-connect"
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": "https://sso.public.example/realms/internal", "authorization_endpoint": realm + "/auth",
			"token_endpoint": realm + "/token", "jwks_uri": realm + "/certs", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	defer srv.Close()
	s := &Service{secret: []byte("test-secret"), cfg: config.Auth{OIDC: config.OIDC{Enabled: true, IssuerURL: srv.URL + "/realm/internal/auth", ClientID: "bepaylot-web"}}}

	authURL, nonce, err := s.OIDCStart(context.Background(), "http://app.local/bepaylot/v1/auth/oidc/callback", "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	sum := sha256.Sum256([]byte(s.pkceVerifier(nonce)))
	if !strings.HasSuffix(u.Path, "/realms/internal/protocol/openid-connect/auth") || q.Get("client_id") != "bepaylot-web" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || q.Has("client_secret") {
		t.Fatalf("authorization url: %s", authURL)
	}
	p, _ := s.provider(context.Background())
	if s.oauth2Config(p, "").Endpoint.AuthStyle != oauth2.AuthStyleInParams {
		t.Fatal("public client must send client_id in the token request body")
	}
}
