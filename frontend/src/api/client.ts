// Low-level HTTP client: connection settings, the sign-in session (JWT access
// + refresh token, refreshed transparently on 401 like WeKnora's web UI),
// JSON requests, authenticated blob URLs for page images, multipart upload
// with progress, and SSE parsing.

import type { AuthTokens, AuthUser } from "./types";

const store = {
  get(k: string, d = ""): string {
    try {
      return localStorage.getItem("bp." + k) ?? d;
    } catch {
      return d;
    }
  },
  set(k: string, v: string | null) {
    try {
      if (v == null) localStorage.removeItem("bp." + k);
      else localStorage.setItem("bp." + k, v);
    } catch {
      /* private mode */
    }
  },
};

// An empty base means "same origin": in dev, vite proxies /v1 to the API.
export const settings = {
  get base(): string {
    return store.get("base", import.meta.env.VITE_API_BASE ?? "").replace(/\/+$/, "");
  },
  set base(v: string) {
    store.set("base", v.trim() || null);
  },
  get kb(): string {
    return store.get("kb");
  },
  set kb(v: string) {
    store.set("kb", v || null);
  },
  // The case last opened in each knowledge base.
  caseOf(kb: string): string {
    return store.get("case." + kb);
  },
  setCaseOf(kb: string, id: string) {
    store.set("case." + kb, id || null);
  },
};

// The signed-in session, kept in localStorage like WeKnora's web UI.
export const session = {
  get token(): string {
    return store.get("token");
  },
  get refresh(): string {
    return store.get("refresh");
  },
  get user(): AuthUser | null {
    try {
      return JSON.parse(store.get("user", "null"));
    } catch {
      return null;
    }
  },
  save(t: AuthTokens) {
    store.set("token", t.access_token);
    store.set("refresh", t.refresh_token);
    store.set("user", JSON.stringify(t.user));
  },
  setUser(u: AuthUser) {
    store.set("user", JSON.stringify(u));
  },
  clear() {
    store.set("token", null);
    store.set("refresh", null);
    store.set("user", null);
  },
};

// Fired when the session can no longer be refreshed; the app goes to /login.
export const SIGNED_OUT_EVENT = "bp:signed-out";

// One refresh at a time: parallel 401s wait for the same rotation (the
// refresh token is single use).
let refreshing: Promise<boolean> | null = null;
export function refreshSession(): Promise<boolean> {
  if (!session.refresh) return Promise.resolve(false);
  refreshing ??= (async () => {
    try {
      const res = await fetch(url("/auth/refresh"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: session.refresh }),
      });
      if (!res.ok) return false;
      session.save(await res.json());
      return true;
    } catch {
      return false;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

function signedOut() {
  session.clear();
  window.dispatchEvent(new Event(SIGNED_OUT_EVENT));
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

function headers(extra?: Record<string, string>): Record<string, string> {
  const h: Record<string, string> = { ...extra };
  if (session.token) h["Authorization"] = "Bearer " + session.token;
  return h;
}

async function errorOf(res: Response): Promise<ApiError> {
  let msg = `${res.status} ${res.statusText}`;
  try {
    const j = await res.json();
    const m = j?.error?.message ?? j?.error ?? j?.message;
    if (m) msg = typeof m === "string" ? m : JSON.stringify(m);
  } catch {
    /* not JSON */
  }
  if (res.status === 401 && !res.url.includes("/auth/")) msg = "Phiên đăng nhập đã hết hạn — hãy đăng nhập lại.";
  return new ApiError(res.status, msg);
}

export const url = (path: string) => settings.base + "/v1" + path;

export interface RequestOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  // Public sign-in calls: a 401 is a wrong password, not an expired session.
  noRefresh?: boolean;
}

async function send(path: string, opt: RequestOptions = {}): Promise<Response> {
  const attempt = async () => {
    const init: RequestInit = { method: opt.method ?? (opt.body !== undefined ? "POST" : "GET"), headers: headers(opt.headers), signal: opt.signal };
    if (opt.body !== undefined) {
      init.body = JSON.stringify(opt.body);
      (init.headers as Record<string, string>)["Content-Type"] = "application/json";
    }
    try {
      return await fetch(url(path), init);
    } catch (e) {
      if ((e as Error).name === "AbortError") throw e;
      throw new ApiError(0, `Không kết nối được máy chủ ${settings.base || location.origin} (${(e as Error).message})`);
    }
  };
  let res = await attempt();
  if (res.status === 401 && !opt.noRefresh && session.token) {
    if (await refreshSession()) res = await attempt();
    if (res.status === 401) signedOut();
  }
  if (!res.ok) throw await errorOf(res);
  return res;
}

export async function request<T>(path: string, opt?: RequestOptions): Promise<T> {
  const res = await send(path, opt);
  const ct = res.headers.get("content-type") ?? "";
  return (ct.includes("json") ? res.json() : res.text()) as Promise<T>;
}

export const requestRaw = send;

// Page images need the API key header, so they are fetched as blobs. The
// server may redirect to a presigned object-store URL; fetch follows it.
const blobCache = new Map<string, Promise<string>>();
export function blobURL(path: string): Promise<string> {
  let p = blobCache.get(path);
  if (!p) {
    p = send(path)
      .then(async (res) => URL.createObjectURL(await res.blob()))
      .catch((e) => {
        blobCache.delete(path);
        throw e;
      });
    blobCache.set(path, p);
  }
  return p;
}

export async function download(path: string, fileName: string) {
  const res = await send(path);
  const href = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = href;
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(href), 10_000);
}

// Multipart upload with progress (fetch has no upload progress events).
export function uploadForm<T>(path: string, form: FormData, onProgress?: (p: number) => void, retried = false): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url(path));
    if (session.token) xhr.setRequestHeader("Authorization", "Bearer " + session.token);
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onload = () => {
      if (xhr.status === 401 && !retried && session.token) {
        refreshSession().then((ok) => {
          if (ok) uploadForm<T>(path, form, onProgress, true).then(resolve, reject);
          else {
            signedOut();
            reject(new ApiError(401, "Phiên đăng nhập đã hết hạn — hãy đăng nhập lại."));
          }
        });
        return;
      }
      let j: unknown = null;
      try {
        j = JSON.parse(xhr.responseText);
      } catch {
        /* ignore */
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(j as T);
      else {
        const err = (j as { error?: { message?: string } } | null)?.error?.message;
        reject(new ApiError(xhr.status, err || xhr.statusText || "Upload lỗi"));
      }
    };
    xhr.onerror = () => reject(new ApiError(0, "Không kết nối được máy chủ"));
    xhr.send(form);
  });
}

// Reads Server-Sent Events from a fetch response (POST streams need custom
// headers, which EventSource cannot send).
export async function readSSE(res: Response, onEvent: (event: string, data: any) => void) {
  const reader = res.body!.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let m: RegExpExecArray | null;
    while ((m = /\r?\n\r?\n/.exec(buf))) {
      const chunk = buf.slice(0, m.index);
      buf = buf.slice(m.index + m[0].length);
      let event = "message";
      let data = "";
      for (const line of chunk.split(/\r?\n/)) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data += (data ? "\n" : "") + line.slice(5).replace(/^ /, "");
      }
      if (!data) continue;
      let payload: unknown = data;
      try {
        payload = JSON.parse(data);
      } catch {
        /* raw text */
      }
      onEvent(event, payload);
    }
  }
}
