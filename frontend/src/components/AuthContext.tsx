import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";

import { getAuthConfig, getMe, logout } from "../api/auth";
import { session, SIGNED_OUT_EVENT } from "../api/client";
import type { AuthConfig, AuthTokens, AuthUser } from "../api/types";
import { Loading } from "./ui";

interface AuthState {
  user: AuthUser | null;
  config: AuthConfig | null;
  // Set when /v1/auth/config could not be loaded (server down, wrong base URL).
  configError: string;
  ready: boolean;
  signIn: (t: AuthTokens) => void;
  signOut: () => Promise<void>;
  setUser: (u: AuthUser) => void;
  reload: () => void;
}

const Ctx = createContext<AuthState>(null!);
export const useAuth = () => useContext(Ctx);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUserState] = useState<AuthUser | null>(null);
  const [config, setConfig] = useState<AuthConfig | null>(null);
  const [configError, setConfigError] = useState("");
  const [ready, setReady] = useState(false);
  const [gen, setGen] = useState(0);

  useEffect(() => {
    let live = true;
    (async () => {
      let cfg: AuthConfig | null = null;
      try {
        cfg = await getAuthConfig();
        setConfigError("");
      } catch (e) {
        setConfigError((e as Error).message);
      }
      if (!live) return;
      setConfig(cfg);
      // With http.auth_bypass (dev) every request is the dev user; otherwise
      // a stored token is checked (and refreshed) against /auth/me.
      if (cfg?.auth_bypass || session.token) {
        try {
          const me = await getMe();
          if (live) {
            if (!cfg?.auth_bypass) session.setUser(me);
            setUserState(me);
          }
        } catch {
          if (live) setUserState(null);
        }
      } else setUserState(null);
      if (live) setReady(true);
    })();
    return () => {
      live = false;
    };
  }, [gen]);

  useEffect(() => {
    const off = () => setUserState(null);
    window.addEventListener(SIGNED_OUT_EVENT, off);
    return () => window.removeEventListener(SIGNED_OUT_EVENT, off);
  }, []);

  const signIn = useCallback((t: AuthTokens) => {
    session.save(t);
    setUserState(t.user);
  }, []);
  const signOut = useCallback(async () => {
    await logout();
    setUserState(null);
  }, []);
  const setUser = useCallback((u: AuthUser) => {
    session.setUser(u);
    setUserState(u);
  }, []);

  return (
    <Ctx.Provider value={{ user, config, configError, ready, signIn, signOut, setUser, reload: () => setGen((g) => g + 1) }}>
      {children}
    </Ctx.Provider>
  );
}

// Sends visitors without a session to /login, remembering where they were.
export function RequireAuth({ children }: { children: ReactNode }) {
  const { user, ready } = useAuth();
  const loc = useLocation();
  if (!ready) return <Loading />;
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />;
  return <>{children}</>;
}
