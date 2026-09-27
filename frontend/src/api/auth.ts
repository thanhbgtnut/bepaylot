// Sign-in endpoints (/v1/auth/*), modelled on WeKnora's auth API.
import { request, session, settings } from "./client";
import type { APIKey, AuthConfig, AuthTokens, AuthUser } from "./types";

export const getAuthConfig = () => request<AuthConfig>("/auth/config", { noRefresh: true });

export const login = (email: string, password: string) =>
  request<AuthTokens>("/auth/login", { body: { email, password }, noRefresh: true });

export const register = (body: { email: string; name?: string; password: string }) =>
  request<AuthTokens>("/auth/register", { body, noRefresh: true });

export const getMe = () => request<AuthUser>("/auth/me");

// Signing out revokes every session of the user on the server.
export async function logout() {
  const refresh_token = session.refresh;
  if (session.token || refresh_token) {
    await request("/auth/logout", { body: { refresh_token }, noRefresh: true }).catch(() => {});
  }
  session.clear();
}

export const changePassword = (current_password: string, new_password: string) =>
  request<AuthTokens>("/auth/change-password", { body: { current_password, new_password } });

export const listAPIKeys = () => request<{ data: APIKey[] }>("/auth/api-keys").then((r) => r.data ?? []);
export const createAPIKey = (name: string) => request<{ key: APIKey; api_key: string }>("/auth/api-keys", { body: { name } });
export const revokeAPIKey = (id: string) => request(`/auth/api-keys/${id}`, { method: "DELETE" });

// The browser navigates here (not fetch): the server sets a nonce cookie
// and redirects to the OIDC provider. return_to brings the browser back to
// this page even when the API is on another origin.
export const oidcStartURL = () => settings.base + "/v1/auth/oidc/start?return_to=" + encodeURIComponent(location.origin + "/login");

// The OIDC callback returns to /login with #oidc_result=<base64url JSON> or
// #oidc_error=<code>&oidc_error_description=…
export function readOIDCHash(hash: string): { tokens?: AuthTokens; error?: string } | null {
  const p = new URLSearchParams(hash.replace(/^#/, ""));
  const result = p.get("oidc_result");
  if (result) {
    try {
      const b64 = result.replace(/-/g, "+").replace(/_/g, "/");
      const json = new TextDecoder().decode(Uint8Array.from(atob(b64 + "===".slice((b64.length + 3) % 4)), (c) => c.charCodeAt(0)));
      return { tokens: JSON.parse(json) };
    } catch {
      return { error: "Không đọc được kết quả đăng nhập OIDC" };
    }
  }
  const err = p.get("oidc_error");
  if (err) return { error: OIDC_ERRORS[err] ?? `Đăng nhập OIDC thất bại (${err})${p.get("oidc_error_description") ? ": " + p.get("oidc_error_description") : ""}` };
  return null;
}

const OIDC_ERRORS: Record<string, string> = {
  invalid_state: "Phiên đăng nhập OIDC không hợp lệ hoặc đã quá 10 phút — thử lại.",
  access_denied: "Bạn đã huỷ đăng nhập ở nhà cung cấp OIDC.",
  missing_email: "Nhà cung cấp OIDC không trả về email (cần scope email).",
  email_not_verified: "Email ở nhà cung cấp OIDC chưa được xác minh.",
  account_disabled: "Tài khoản đã bị khoá.",
  provider_unavailable: "Không kết nối được nhà cung cấp OIDC.",
};
