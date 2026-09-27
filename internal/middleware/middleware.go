// Package middleware holds the Hertz middleware: request id, structured access
// logging, panic recovery, permissive CORS, and authentication (JWT access
// tokens from the login page, or API keys).
package middleware

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	"github.com/thanhenti/bepaylot/internal/types"
)

// devBypassEmail/devBypassName identify the fixed local user that stands in
// for authentication when Auth's bypass flag is set.
const (
	devBypassEmail = "dev-bypass@bepaylot.local"
	devBypassName  = "Dev Bypass User"
)

type ctxKey string

const (
	keyRequestID ctxKey = "request_id"
	keyUser      ctxKey = "user"
)

// RequestID assigns a request id (honouring an inbound X-Request-Id).
func RequestID() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		id := string(c.GetHeader("X-Request-Id"))
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(string(keyRequestID), id)
		c.Header("X-Request-Id", id)
		c.Next(ctx)
	}
}

// RequestIDFrom returns the request id set by RequestID.
func RequestIDFrom(c *app.RequestContext) string {
	if v, ok := c.Get(string(keyRequestID)); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// AccessLog logs one line per request after it completes.
func AccessLog(log *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		c.Next(ctx)
		log.Info("http",
			"method", string(c.Method()),
			"path", string(c.Path()),
			"status", c.Response.StatusCode(),
			"dur_ms", time.Since(start).Milliseconds(),
			"request_id", RequestIDFrom(c),
		)
	}
}

// Recovery converts a panic into a 500 error envelope.
func Recovery(log *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", "err", r, "path", string(c.Path()), "request_id", RequestIDFrom(c))
				c.AbortWithStatusJSON(consts.StatusInternalServerError, dto.NewError("internal_server_error", "internal error"))
			}
		}()
		c.Next(ctx)
	}
}

// CORS applies permissive CORS suitable for API usage.
func CORS(origins []string) app.HandlerFunc {
	allowAll := len(origins) == 0
	set := map[string]bool{}
	for _, o := range origins {
		set[o] = true
	}
	return func(ctx context.Context, c *app.RequestContext) {
		origin := string(c.GetHeader("Origin"))
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if set[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version, X-Request-Id")
		if string(c.Method()) == consts.MethodOptions {
			c.AbortWithStatus(consts.StatusNoContent)
			return
		}
		c.Next(ctx)
	}
}

// TokenAuthenticator resolves a JWT access token (issued by /v1/auth/login,
// /register, /refresh or the OIDC callback) to its user.
type TokenAuthenticator interface {
	Authenticate(ctx context.Context, token string) (types.User, error)
}

// looksLikeJWT tells a JWT from an API key in an Authorization header.
func looksLikeJWT(s string) bool {
	return strings.HasPrefix(s, "eyJ") && strings.Count(s, ".") == 2
}

// Auth authenticates a request the way WeKnora does: an `Authorization:
// Bearer <JWT>` access token first, else an API key (`x-api-key`, or an API
// key sent as the Bearer value). The user is stored in the request context.
//
// When bypass is true (BEPAYLOT_AUTH_BYPASS=true), the check is skipped
// entirely: every request is treated as a fixed local dev user, created once
// on first use. This is a local-development convenience only — never enable
// it in a deployed environment, since it removes all request authentication.
func Auth(tokens TokenAuthenticator, keys *postgres.APIKeysRepo, users *postgres.UsersRepo, bypass bool) app.HandlerFunc {
	var (
		once    sync.Once
		devUser types.User
		devErr  error
	)
	return func(ctx context.Context, c *app.RequestContext) {
		if bypass {
			once.Do(func() { devUser, devErr = users.Create(ctx, devBypassEmail, devBypassName) })
			if devErr != nil {
				c.AbortWithStatusJSON(consts.StatusInternalServerError, dto.NewError("internal_server_error", "auth bypass: "+devErr.Error()))
				return
			}
			c.Set(string(keyUser), devUser)
			c.Next(ctx)
			return
		}

		var bearer string
		if b := string(c.GetHeader("Authorization")); len(b) > 7 && strings.EqualFold(b[:7], "Bearer ") {
			bearer = strings.TrimSpace(b[7:])
		}
		if bearer != "" && looksLikeJWT(bearer) && tokens != nil {
			user, err := tokens.Authenticate(ctx, bearer)
			if err != nil {
				c.AbortWithStatusJSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "invalid or expired access token"))
				return
			}
			c.Set(string(keyUser), user)
			c.Next(ctx)
			return
		}

		raw := string(c.GetHeader("x-api-key"))
		if raw == "" {
			raw = bearer
		}
		if raw == "" {
			c.AbortWithStatusJSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "sign in, or send an x-api-key header"))
			return
		}
		user, err := keys.Verify(ctx, raw)
		if err != nil {
			c.AbortWithStatusJSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "invalid api key"))
			return
		}
		if !user.IsActive {
			c.AbortWithStatusJSON(consts.StatusForbidden, dto.NewError("permission_error", "account disabled"))
			return
		}
		c.Set(string(keyUser), user)
		c.Next(ctx)
	}
}

// UserFrom returns the authenticated user, or ok=false.
func UserFrom(c *app.RequestContext) (types.User, bool) {
	v, ok := c.Get(string(keyUser))
	if !ok {
		return types.User{}, false
	}
	u, ok := v.(types.User)
	return u, ok
}

var _ = utils.H{}
