import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, loadEnv, type Plugin } from "vite";

// The build goes into the Go package that embeds it (internal/webui), so the
// server binary serves the app. Vite empties the folder first; this puts back
// the tracked placeholder the //go:embed pattern needs.
const outDir = fileURLToPath(new URL("../internal/webui/dist", import.meta.url));
const keepPlaceholder: Plugin = {
  name: "keep-gitkeep",
  closeBundle: () => writeFileSync(resolve(outDir, ".gitkeep"), ""),
};

// In dev, /v1 is proxied to the bepaylot API so the browser stays same-origin
// (no CORS, and the session tokens never leave localhost). Override the target with
// BEPAYLOT_API=http://host:port npm run dev.
export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const target = env.BEPAYLOT_API || "http://localhost:8080";
  // BEPAYLOT_CONTEXT_ROOT (e.g. /bepaylot): the dev server serves the app
  // and proxies the API under it, like the server behind an ingress.
  const root = "/" + (env.BEPAYLOT_CONTEXT_ROOT ?? "").replace(/^\/+|\/+$/g, "");
  const prefix = root === "/" ? "" : root;
  return {
    plugins: [react(), tailwindcss(), keepPlaceholder],
    // Relative asset URLs: the server puts <base href="{context root}/"> in
    // index.html (BEPAYLOT_CONTEXT_ROOT), so one build runs under any path.
    base: command === "build" ? "./" : prefix + "/",
    // The graph route lazy-loads three.js (~1 MB); the rest stays small.
    build: { chunkSizeWarningLimit: 1200, outDir, emptyOutDir: true },
    server: {
      port: 5174,
      proxy: {
        // xfwd: the API sees the browser's host (X-Forwarded-Host), so the
        // OIDC callback URL it derives points back at this dev server.
        [prefix + "/v1"]: { target, changeOrigin: true, xfwd: true },
        [prefix + "/healthz"]: { target, changeOrigin: true },
      },
    },
  };
});
