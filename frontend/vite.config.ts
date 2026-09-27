import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";

// In dev, /v1 is proxied to the bepaylot API so the browser stays same-origin
// (no CORS, and the session tokens never leave localhost). Override the target with
// BEPAYLOT_API=http://host:port npm run dev.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const target = env.BEPAYLOT_API || "http://localhost:8080";
  return {
    plugins: [react(), tailwindcss()],
    // The graph route lazy-loads three.js (~1 MB); the rest stays small.
    build: { chunkSizeWarningLimit: 1200 },
    server: {
      port: 5174,
      proxy: {
        // xfwd: the API sees the browser's host (X-Forwarded-Host), so the
        // OIDC callback URL it derives points back at this dev server.
        "/v1": { target, changeOrigin: true, xfwd: true },
        "/healthz": { target, changeOrigin: true },
      },
    },
  };
});
