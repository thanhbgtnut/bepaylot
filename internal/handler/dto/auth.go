package dto

import (
	"time"

	"github.com/thanhenti/bepaylot/internal/types"
)

// AuthConfig is the body of GET /v1/auth/config: what the login page offers.
type AuthConfig struct {
	RegistrationEnabled bool           `json:"registration_enabled"`
	PasswordMinLength   int            `json:"password_min_length"`
	OIDC                AuthOIDCConfig `json:"oidc"`
	// AuthBypass: the server runs with http.auth_bypass (dev only); the UI
	// skips the login page.
	AuthBypass bool `json:"auth_bypass"`
}

// AuthOIDCConfig tells the login page whether to show the OIDC button.
type AuthOIDCConfig struct {
	Enabled     bool   `json:"enabled"`
	DisplayName string `json:"display_name,omitempty"`
}

// RegisterRequest is the body of POST /v1/auth/register.
type RegisterRequest struct {
	Email    string `json:"email" example:"an.nguyen@example.com"`
	Name     string `json:"name,omitempty" example:"Nguyễn An"`
	Password string `json:"password" example:"correct horse battery"`
}

// LoginRequest is the body of POST /v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RefreshRequest is the body of POST /v1/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// LogoutRequest is the optional body of POST /v1/auth/logout (used when the
// access token has already been dropped).
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

// ChangePasswordRequest is the body of POST /v1/auth/change-password.
type ChangePasswordRequest struct {
	// CurrentPassword may be empty for accounts without a local password
	// (created through OIDC or cmd/seed).
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// UserView is the public view of a user.
type UserView struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	AuthProvider string     `json:"auth_provider" example:"local"`
	HasPassword  bool       `json:"has_password"`
	IsAdmin      bool       `json:"is_admin"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// AuthTokens is returned by login, register, refresh, change-password and
// (base64url-encoded in #oidc_result) the OIDC callback.
type AuthTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type" example:"Bearer"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         UserView  `json:"user"`
	IsNewUser    bool      `json:"is_new_user,omitempty"`
}

// OKResponse is a bare success body.
type OKResponse struct {
	Success bool `json:"success"`
}

// APIKeyView is one API key of the current user (never the plaintext).
type APIKeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// APIKeyList is the body of GET /v1/auth/api-keys.
type APIKeyList struct {
	Data []APIKeyView `json:"data"`
}

// CreateAPIKeyRequest is the body of POST /v1/auth/api-keys.
type CreateAPIKeyRequest struct {
	Name string `json:"name" example:"curl"`
}

// CreatedAPIKey returns the plaintext key exactly once.
type CreatedAPIKey struct {
	Key    APIKeyView `json:"key"`
	APIKey string     `json:"api_key" example:"sk-bepaylot-…"`
}

// UserViewFrom maps a user.
func UserViewFrom(u types.User, isAdmin bool) UserView {
	return UserView{
		ID: u.ID.String(), Email: u.Email, Name: u.Name, AuthProvider: u.AuthProvider,
		HasPassword: u.HasPassword, IsAdmin: isAdmin, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
	}
}

// APIKeyViewFrom maps a key.
func APIKeyViewFrom(k types.APIKey) APIKeyView {
	return APIKeyView{ID: k.ID.String(), Name: k.Name, Prefix: k.Prefix, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt, CreatedAt: k.CreatedAt}
}
