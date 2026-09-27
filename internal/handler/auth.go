package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/auth"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// oidcNonceCookie binds an OIDC flow to the browser that started it.
const oidcNonceCookie = "bp_oidc_nonce"

func (h *Handlers) authReady(c *app.RequestContext) bool {
	if h.Auth == nil {
		c.JSON(consts.StatusServiceUnavailable, dto.NewError("api_error", "auth service not configured"))
		return false
	}
	return true
}

func (h *Handlers) authError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, auth.ErrBadRequest), errors.Is(err, auth.ErrWrongPassword):
		c.JSON(consts.StatusBadRequest, dto.NewError("invalid_request_error", strings.TrimPrefix(err.Error(), auth.ErrBadRequest.Error()+": ")))
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidToken):
		c.JSON(consts.StatusUnauthorized, dto.NewError("authentication_error", err.Error()))
	case errors.Is(err, auth.ErrEmailTaken):
		c.JSON(consts.StatusConflict, dto.NewError("invalid_request_error", err.Error()))
	case errors.Is(err, auth.ErrRegistrationClosed), errors.Is(err, auth.ErrDisabled):
		c.JSON(consts.StatusForbidden, dto.NewError("permission_error", err.Error()))
	case errors.Is(err, auth.ErrOIDCDisabled):
		c.JSON(consts.StatusNotFound, dto.NewError("not_found_error", err.Error()))
	default:
		h.serverError(c, err)
	}
}

func (h *Handlers) tokensView(t auth.Tokens, isNew bool) dto.AuthTokens {
	return dto.AuthTokens{
		AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, TokenType: "Bearer",
		ExpiresAt: t.ExpiresAt, User: dto.UserViewFrom(t.User, h.adminEmail(t.User)), IsNewUser: isNew,
	}
}

// AuthConfig handles GET /v1/auth/config.
//
// @Summary  What the login page offers (sign-up, OIDC button)
// @Tags     Auth
// @Produce  json
// @Success  200  {object}  dto.AuthConfig
// @Router   /v1/auth/config [get]
func (h *Handlers) AuthConfig(_ context.Context, c *app.RequestContext) {
	out := dto.AuthConfig{}
	if h.Config != nil {
		out.AuthBypass = h.Config.HTTP.AuthBypass
	}
	if h.Auth != nil {
		out.RegistrationEnabled = h.Auth.RegistrationOpen()
		out.PasswordMinLength = h.Auth.PasswordMinLength()
		out.OIDC = dto.AuthOIDCConfig{Enabled: h.Auth.OIDCEnabled()}
		if out.OIDC.Enabled {
			out.OIDC.DisplayName = h.Auth.OIDCDisplayName()
		}
	}
	c.JSON(consts.StatusOK, out)
}

// Register handles POST /v1/auth/register.
//
// @Summary      Create an account with email + password and sign in
// @Description  Refused with 403 when auth.registration is "closed". The response carries a JWT pair like /v1/auth/login.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      dto.RegisterRequest  true  "Account"
// @Success      201      {object}  dto.AuthTokens
// @Failure      400      {object}  dto.ErrorResponse
// @Failure      403      {object}  dto.ErrorResponse
// @Failure      409      {object}  dto.ErrorResponse
// @Router       /v1/auth/register [post]
func (h *Handlers) Register(ctx context.Context, c *app.RequestContext) {
	if !h.authReady(c) {
		return
	}
	var req dto.RegisterRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	t, err := h.Auth.Register(ctx, req.Email, req.Name, req.Password)
	if err != nil {
		h.authError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, h.tokensView(t, true))
}

// Login handles POST /v1/auth/login.
//
// @Summary      Sign in with email + password
// @Description  Returns an access token (send as `Authorization: Bearer`, default 24h) and a refresh token (default 7 days, single use).
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      dto.LoginRequest  true  "Credentials"
// @Success      200      {object}  dto.AuthTokens
// @Failure      401      {object}  dto.ErrorResponse
// @Failure      403      {object}  dto.ErrorResponse
// @Router       /v1/auth/login [post]
func (h *Handlers) Login(ctx context.Context, c *app.RequestContext) {
	if !h.authReady(c) {
		return
	}
	var req dto.LoginRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	t, err := h.Auth.Login(ctx, req.Email, req.Password)
	if err != nil {
		h.authError(c, err)
		return
	}
	c.JSON(consts.StatusOK, h.tokensView(t, false))
}

// RefreshToken handles POST /v1/auth/refresh.
//
// @Summary      Exchange a refresh token for a new token pair
// @Description  The refresh token is revoked on use (rotation).
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      dto.RefreshRequest  true  "Refresh token"
// @Success      200      {object}  dto.AuthTokens
// @Failure      401      {object}  dto.ErrorResponse
// @Router       /v1/auth/refresh [post]
func (h *Handlers) RefreshToken(ctx context.Context, c *app.RequestContext) {
	if !h.authReady(c) {
		return
	}
	var req dto.RefreshRequest
	if err := c.BindJSON(&req); err != nil || req.RefreshToken == "" {
		h.badRequest(c, "refresh_token is required")
		return
	}
	t, err := h.Auth.Refresh(ctx, req.RefreshToken)
	if err != nil {
		h.authError(c, err)
		return
	}
	c.JSON(consts.StatusOK, h.tokensView(t, false))
}

// Logout handles POST /v1/auth/logout.
//
// @Summary      Sign out everywhere
// @Description  Revokes every token of the user owning the presented token (`Authorization: Bearer <access token>`, or `refresh_token` in the body); expired tokens are accepted.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      dto.LogoutRequest  false  "Refresh token, when no access token is sent"
// @Success      200      {object}  dto.OKResponse
// @Failure      401      {object}  dto.ErrorResponse
// @Router       /v1/auth/logout [post]
func (h *Handlers) Logout(ctx context.Context, c *app.RequestContext) {
	if !h.authReady(c) {
		return
	}
	token := ""
	if b := string(c.GetHeader("Authorization")); len(b) > 7 && strings.EqualFold(b[:7], "Bearer ") {
		token = strings.TrimSpace(b[7:])
	}
	if token == "" {
		var req dto.LogoutRequest
		_ = c.BindJSON(&req)
		token = req.RefreshToken
	}
	if token == "" {
		h.badRequest(c, "send the access token (Authorization: Bearer) or refresh_token")
		return
	}
	if err := h.Auth.Logout(ctx, token); err != nil {
		h.authError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OKResponse{Success: true})
}

// OIDCStart handles GET /v1/auth/oidc/start.
//
// @Summary      Start OIDC sign-in (302 to the provider)
// @Description  Sets an HttpOnly nonce cookie that the callback checks. The login page navigates here. return_to (the frontend's /login URL) must be on the request's origin, in http.cors_origins, or on localhost in development.
// @Tags         Auth
// @Param        return_to  query  string  false  "Frontend page to return to"
// @Success      302
// @Failure      400  {object}  dto.ErrorResponse
// @Failure      404  {object}  dto.ErrorResponse
// @Failure      500  {object}  dto.ErrorResponse
// @Router       /v1/auth/oidc/start [get]
func (h *Handlers) OIDCStart(ctx context.Context, c *app.RequestContext) {
	if !h.authReady(c) {
		return
	}
	origin := requestOrigin(c)
	returnTo := string(c.Query("return_to"))
	if returnTo != "" && !h.trustedReturn(returnTo, origin) {
		h.badRequest(c, "return_to is not a trusted origin (add it to http.cors_origins)")
		return
	}
	authURL, nonce, err := h.Auth.OIDCStart(ctx, h.Auth.OIDCRedirectURL(origin+"/v1/auth/oidc/callback"), returnTo)
	if err != nil {
		h.authError(c, err)
		return
	}
	c.SetCookie(oidcNonceCookie, nonce, 600, "/v1/auth/oidc", "", protocol.CookieSameSiteLaxMode, isHTTPS(c), true)
	c.Redirect(consts.StatusFound, []byte(authURL))
}

// OIDCCallback handles GET /v1/auth/oidc/callback.
//
// @Summary      OIDC redirect target
// @Description  Exchanges the code, finds or creates the user (by subject, then email) and redirects to return_to from the start request (else auth.oidc.frontend_url) with `#oidc_result=<base64url JSON of dto.AuthTokens>` or `#oidc_error=<code>&oidc_error_description=…`.
// @Tags         Auth
// @Param        code               query  string  false  "Authorization code"
// @Param        state              query  string  false  "Signed state"
// @Param        error              query  string  false  "Provider error"
// @Param        error_description  query  string  false  "Provider error text"
// @Success      302
// @Router       /v1/auth/oidc/callback [get]
func (h *Handlers) OIDCCallback(ctx context.Context, c *app.RequestContext) {
	if !h.authReady(c) {
		return
	}
	front := h.Auth.OIDCReturnTo(string(c.Query("state")))
	fail := func(code, desc string) {
		frag := "#oidc_error=" + url.QueryEscape(code)
		if desc != "" {
			frag += "&oidc_error_description=" + url.QueryEscape(desc)
		}
		c.Redirect(consts.StatusFound, []byte(front+frag))
	}
	// The nonce cookie is single use.
	nonce := string(c.Cookie(oidcNonceCookie))
	c.SetCookie(oidcNonceCookie, "", -1, "/v1/auth/oidc", "", protocol.CookieSameSiteLaxMode, isHTTPS(c), true)

	if e := string(c.Query("error")); e != "" {
		fail(e, string(c.Query("error_description")))
		return
	}
	t, created, err := h.Auth.OIDCCallback(ctx, string(c.Query("code")), string(c.Query("state")), nonce)
	if err != nil {
		var ue *auth.OIDCUserError
		if errors.As(err, &ue) {
			h.Log.Warn("oidc sign-in failed", "code", ue.Code, "err", ue.Err)
			fail(ue.Code, ue.Err.Error())
			return
		}
		h.Log.Error("oidc sign-in failed", "err", err)
		fail("login_failed", "")
		return
	}
	raw, err := json.Marshal(h.tokensView(t, created))
	if err != nil {
		fail("login_failed", "")
		return
	}
	c.Redirect(consts.StatusFound, []byte(front+"#oidc_result="+base64.RawURLEncoding.EncodeToString(raw)))
}

// Me handles GET /v1/auth/me.
//
// @Summary   The signed-in user
// @Tags      Auth
// @Produce   json
// @Success   200  {object}  dto.UserView
// @Failure   401  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/auth/me [get]
func (h *Handlers) Me(_ context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	c.JSON(consts.StatusOK, dto.UserViewFrom(u, h.adminEmail(u)))
}

// ChangePassword handles POST /v1/auth/change-password.
//
// @Summary      Change (or set) the password
// @Description  Revokes every session of the user and returns a new token pair. Accounts without a password (OIDC, cmd/seed) may leave current_password empty.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      dto.ChangePasswordRequest  true  "Passwords"
// @Success      200      {object}  dto.AuthTokens
// @Failure      400      {object}  dto.ErrorResponse
// @Failure      401      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/auth/change-password [post]
func (h *Handlers) ChangePassword(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok || !h.authReady(c) {
		return
	}
	var req dto.ChangePasswordRequest
	if err := c.BindJSON(&req); err != nil {
		h.badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	t, err := h.Auth.ChangePassword(ctx, u, req.CurrentPassword, req.NewPassword)
	if err != nil {
		h.authError(c, err)
		return
	}
	c.JSON(consts.StatusOK, h.tokensView(t, false))
}

// ListAPIKeys handles GET /v1/auth/api-keys.
//
// @Summary   The signed-in user's API keys (for scripts and curl)
// @Tags      Auth
// @Produce   json
// @Success   200  {object}  dto.APIKeyList
// @Failure   401  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/auth/api-keys [get]
func (h *Handlers) ListAPIKeys(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	keys, err := h.Store.APIKeys.List(ctx, u.ID)
	if err != nil {
		h.serverError(c, err)
		return
	}
	out := dto.APIKeyList{Data: make([]dto.APIKeyView, 0, len(keys))}
	for _, k := range keys {
		out.Data = append(out.Data, dto.APIKeyViewFrom(k))
	}
	c.JSON(consts.StatusOK, out)
}

// CreateAPIKey handles POST /v1/auth/api-keys.
//
// @Summary      Issue an API key; the plaintext is returned only here
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      dto.CreateAPIKeyRequest  false  "Label"
// @Success      201      {object}  dto.CreatedAPIKey
// @Failure      401      {object}  dto.ErrorResponse
// @Security     ApiKeyAuth
// @Router       /v1/auth/api-keys [post]
func (h *Handlers) CreateAPIKey(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var req dto.CreateAPIKeyRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			h.badRequest(c, "invalid JSON body: "+err.Error())
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "web"
	}
	plaintext, key, err := h.Store.APIKeys.Issue(ctx, u.ID, name)
	if err != nil {
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusCreated, dto.CreatedAPIKey{Key: dto.APIKeyViewFrom(key), APIKey: plaintext})
}

// RevokeAPIKey handles DELETE /v1/auth/api-keys/{id}.
//
// @Summary   Revoke one of the user's API keys
// @Tags      Auth
// @Produce   json
// @Param     id   path      string  true  "Key id"
// @Success   200  {object}  dto.OKResponse
// @Failure   404  {object}  dto.ErrorResponse
// @Security  ApiKeyAuth
// @Router    /v1/auth/api-keys/{id} [delete]
func (h *Handlers) RevokeAPIKey(ctx context.Context, c *app.RequestContext) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.notFound(c, "api key not found")
		return
	}
	if err := h.Store.APIKeys.Revoke(ctx, u.ID, id); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			h.notFound(c, "api key not found")
			return
		}
		h.serverError(c, err)
		return
	}
	c.JSON(consts.StatusOK, dto.OKResponse{Success: true})
}

func (h *Handlers) adminEmail(u types.User) bool {
	if h.Config == nil {
		return false
	}
	for _, e := range h.Config.HTTP.AdminEmails {
		if strings.EqualFold(strings.TrimSpace(e), u.Email) {
			return true
		}
	}
	return false
}

// trustedReturn allows OIDC return_to pages whose origin is the request's
// own, listed in http.cors_origins, the configured frontend URL, or — in
// development with no CORS list — localhost. Anything else could receive
// the tokens in the URL fragment.
func (h *Handlers) trustedReturn(raw, reqOrigin string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	origin := u.Scheme + "://" + u.Host
	if origin == reqOrigin {
		return true
	}
	if f, err := url.Parse(h.Auth.OIDCFrontendURL()); err == nil && f.Host != "" && f.Scheme+"://"+f.Host == origin {
		return true
	}
	if h.Config == nil {
		return false
	}
	for _, o := range h.Config.HTTP.CORSOrigins {
		if strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return true
		}
	}
	if len(h.Config.HTTP.CORSOrigins) == 0 && h.Config.Env == "development" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
	}
	return false
}

func isHTTPS(c *app.RequestContext) bool {
	return strings.EqualFold(string(c.GetHeader("X-Forwarded-Proto")), "https") || string(c.URI().Scheme()) == "https"
}

// requestOrigin is scheme://host as the browser sees it, honouring the
// X-Forwarded-* headers of a proxy (Vite's dev proxy, nginx).
func requestOrigin(c *app.RequestContext) string {
	scheme := "http"
	if isHTTPS(c) {
		scheme = "https"
	}
	host := string(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = string(c.Host())
	}
	host, _, _ = strings.Cut(host, ",")
	return scheme + "://" + strings.TrimSpace(host)
}
