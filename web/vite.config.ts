import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Dev proxy: /v1 + /ui → navarisd on :8080. The SPA itself is served
// directly by Vite, so /assets and /sandboxes/*/terminal are NOT proxied.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/v1": {
        target: "http://localhost:8080",
        changeOrigin: true,
        ws: true,
        // Rewrite Origin so the backend's WebSocket accept check
        // (nhooyr/websocket compares Origin host to request Host) passes
        // in dev. Without this, /v1/events and /v1/sandboxes/*/attach
        // are rejected with "Origin localhost:5173 is not authorized for
        // Host localhost:8080".
        configure: (proxy) => {
          proxy.on("proxyReqWs", (proxyReq) => {
            proxyReq.setHeader("origin", "http://localhost:8080");
          });
        },
      },
      "/ui": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
