import { defineConfig } from "vite"
import { tanstackRouter } from "@tanstack/router-plugin/vite"
import viteReact from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

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
    proxy: { "/api": { target: "http://127.0.0.1:7878", ws: true } },
  },
})
