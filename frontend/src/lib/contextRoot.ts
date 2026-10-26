// The path prefix the app is served under (BEPAYLOT_CONTEXT_ROOT), e.g.
// "/bepaylot" when an ingress serves several apps on one domain; "" at the
// domain root. The server writes it into index.html as <base href="…/">, so
// one build runs under any context root. The Vite dev server has no <base>:
// there it is Vite's base (vite.config.ts reads the same variable).
export const CONTEXT_ROOT = (document.querySelector("base")?.getAttribute("href") ?? (import.meta.env.DEV ? import.meta.env.BASE_URL : "/")).replace(/\/+$/, "");
