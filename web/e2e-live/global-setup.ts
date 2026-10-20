import { request, type APIResponse } from "@playwright/test"

/**
 * Exchanges STUDIO_LOGIN_URL's one-time token for a session, and hands the session to the
 * tests in STUDIO_LIVE_STATE (workers inherit the environment). The guided setup is
 * dismissed, so that pages open as themselves rather than on /setup.
 */
export default async function globalSetup() {
  const login = process.env.STUDIO_LOGIN_URL
  if (!login)
    throw new Error(
      "Set STUDIO_LOGIN_URL to a fresh link from `studio login-url` of the running Studio."
    )
  const link = new URL(login)
  const token = new URLSearchParams(link.hash.slice(1)).get("studio-login")
  if (!token) throw new Error(`${login} has no studio-login token.`)

  const ctx = await request.newContext({ baseURL: link.origin })
  const check = async (what: string, res: Promise<APIResponse>) => {
    const r = await res
    if (!r.ok()) throw new Error(`${what}: HTTP ${r.status()}`)
  }
  await check("log in", ctx.post("/api/auth/exchange", { data: { token } }))
  const envs = (await (await ctx.get("/api/environments")).json()) as {
    id: string
  }[]
  await check(
    "dismiss the setup",
    ctx.put(`/api/setup?env=${envs[0].id}`, {
      data: { steps: {}, dismissed: true },
    })
  )
  process.env.STUDIO_LIVE_STATE = JSON.stringify(await ctx.storageState())
  await ctx.dispose()
}
