import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";

// In dev, /v1 is proxied to the bepaylot API so the browser stays same-origin
// (no CORS, and the API key never leaves localhost). Override the target with
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
        "/v1": { target, changeOrigin: true },
        "/healthz": { target, changeOrigin: true },
      },
    },
  };
});
