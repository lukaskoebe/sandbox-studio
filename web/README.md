# web

Sandbox Studio UI: React, TanStack Router (SPA, no Start), TanStack Query, shadcn/ui.

- `pnpm dev` serves on :3000 and proxies `/api` to the Studio server on :7878.
- `pnpm build` writes `dist/`, which the Go binary embeds.
- `pnpm dlx shadcn@latest add <component>` adds components.
