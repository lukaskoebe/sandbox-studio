import { hostname } from "node:os"
import type { IncomingMessage } from "node:http"
import { defineConfig } from "vite"
import { tanstackRouter } from "@tanstack/router-plugin/vite"
import viteReact from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

const studio = "http://127.0.0.1:7878"

// Studio only accepts loopback hosts and same-origin writes. The dev proxy presents
// requests from this dev page as Studio's own origin, and leaves any other origin intact
// so Studio still refuses cross-site requests.
function sameOriginAsStudio(
  proxyReq: { setHeader: (k: string, v: string) => void },
  req: IncomingMessage
) {
  const origin = req.headers.origin
  if (origin && new URL(origin).host === req.headers.host)
    proxyReq.setHeader("origin", studio)
}

export default defineConfig({
  resolve: { tsconfigPaths: true },
  plugins: [
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    tailwindcss(),
    viteReact(),
  ],
  build: { outDir: "../internal/webui/static/app", emptyOutDir: true },
  server: {
    port: 3000,
    // `pnpm dev:lan` listens on all interfaces; IP addresses and this machine's name work.
    allowedHosts: [hostname(), `${hostname()}.local`],
    proxy: {
      "/api": {
        target: studio,
        ws: true,
        changeOrigin: true,
        configure: (proxy) => {
          proxy.on("proxyReq", sameOriginAsStudio)
          proxy.on("proxyReqWs", sameOriginAsStudio)
        },
      },
    },
  },
})
