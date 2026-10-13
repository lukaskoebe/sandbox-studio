import {
  test as base,
  expect,
  request,
  type APIRequestContext,
  type Page,
} from "@playwright/test"
import { startStudio, studioPort, type Studio } from "./studio"

/** What the fake guest of a sandbox recorded (internal/e2e, GET /api/e2e/sandboxes/{id}). */
export type GuestState = {
  connected: boolean
  configs: number
  env: Record<string, string>
  ca: boolean
  ports: number[]
  connections: {
    host: string
    port: number
    done: boolean
    status?: number
    body?: string
    error?: string
  }[]
}

/** Studio's API and the fake runtime's control API, logged in. */
export class Api {
  constructor(
    readonly request: APIRequestContext,
    readonly env: string
  ) {}

  async json<T>(
    method: string,
    path: string,
    data?: unknown
  ): Promise<T | undefined> {
    const res = await this.request.fetch(path, { method, data })
    expect(res.ok(), `${method} ${path}: ${await res.text()}`).toBe(true)
    const text = await res.text()
    return text ? (JSON.parse(text) as T) : undefined
  }

  async createSandbox(name: string, teammateId?: string) {
    const sb = await this.json<{ id: string }>(
      "POST",
      `/api/environments/${this.env}/sandboxes`,
      teammateId ? { name, teammateId } : { name }
    )
    return sb!.id
  }

  guest(id: string) {
    return this.json<GuestState>("GET", `/api/e2e/sandboxes/${id}`).then(
      (s) => s!
    )
  }

  /** Makes the fake guest request http://host:port/ through the gateway. */
  connect(id: string, host: string, port = 80) {
    return this.json("POST", `/api/e2e/sandboxes/${id}/connect`, { host, port })
  }

  /** Dismisses every pending approval, so tests don't see each other's. */
  async dismissApprovals() {
    const pending =
      (await this.json<{ id: string; environmentId: string }[]>(
        "GET",
        "/api/approvals?status=pending"
      )) ?? []
    for (const a of pending)
      await this.request.post(
        `/api/environments/${a.environmentId}/approvals/${a.id}/decide`,
        { data: { action: "dismiss", scope: "environment" } }
      )
  }
}

type StorageState = Awaited<ReturnType<APIRequestContext["storageState"]>>

/**
 * Exchanges a fresh login link for a session cookie. The work mode is chosen and the guided
 * setup dismissed, so that tests start on the app's pages rather than on /setup.
 */
async function logIn(studio: Studio): Promise<StorageState> {
  const ctx = await request.newContext({ baseURL: studio.url })
  const link = new URL(await studio.loginURL())
  const token = new URLSearchParams(link.hash.slice(1)).get("studio-login")
  expect((await ctx.post("/api/auth/exchange", { data: { token } })).ok()).toBe(
    true
  )
  expect(
    (
      await ctx.put("/api/settings/ui", { data: { agentView: "terminal" } })
    ).ok()
  ).toBe(true)
  const envs = await (await ctx.get("/api/environments")).json()
  expect(
    (
      await ctx.put(`/api/setup?env=${envs[0].id}`, {
        data: { steps: {}, dismissed: true },
      })
    ).ok()
  ).toBe(true)
  const state = await ctx.storageState()
  await ctx.dispose()
  return state
}

export const test = base.extend<
  { api: Api; env: string },
  { studio: Studio; session: StorageState }
>({
  // One Studio per worker, on its own port and data directory.
  studio: [
    // Playwright wants an object pattern for fixtures; this one takes none.
    // eslint-disable-next-line no-empty-pattern
    async ({}, use, workerInfo) => {
      const studio = await startStudio(studioPort(workerInfo.parallelIndex * 2))
      await use(studio)
      await studio.stop()
    },
    { scope: "worker" },
  ],
  session: [
    async ({ studio }, use) => use(await logIn(studio)),
    { scope: "worker" },
  ],
  baseURL: async ({ studio }, use) => use(studio.url),
  storageState: async ({ session }, use) => use(session),
  api: async ({ studio, session }, use) => {
    const ctx = await request.newContext({
      baseURL: studio.url,
      storageState: session,
    })
    const envs = await (await ctx.get("/api/environments")).json()
    await use(new Api(ctx, envs[0].id))
    await ctx.dispose()
  },
  env: async ({ api }, use) => use(api.env),
})

export { expect }

/** A unique sandbox name for a test. */
/** A teammate on a fake Anthropic key, added through the API if the environment has none yet. */
export async function addTeammate(api: Api, env: string, name: string) {
  const providers = await api.json<
    { id: string; harnesses: string[] | null }[]
  >("GET", `/api/environments/${env}/providers`)
  const provider =
    providers?.at(0) ??
    (await api.json<{ id: string; harnesses: string[] | null }>(
      "POST",
      `/api/environments/${env}/providers`,
      { name: "Anthropic", kind: "anthropic_api", apiKey: "sk-ant-e2e-fake" }
    ))!
  const teammate = await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/teammates`,
    {
      name,
      harness: provider.harnesses?.at(0) ?? "claude",
      providerId: provider.id,
      model: "claude-sonnet-5-5",
    }
  )
  return teammate!.id
}

export function sandboxName(prefix: string) {
  return `${prefix}-${Math.random().toString(36).slice(2, 8)}`
}

/** The approval stack shown on every page. */
export function approvals(page: Page) {
  return page.getByRole("region", { name: "Approvals" })
}

/**
 * The visible terminal. The test browser has WebGL off, so xterm.js renders rows as DOM
 * text; its own class names are the only handle on them.
 */
export function terminal(page: Page) {
  // Clicks go to the xterm root: the screen above the rows takes the pointer events.
  const root = page.locator(".xterm").filter({ visible: true })
  const rows = root.locator(".xterm-rows")
  return {
    rows,
    /** Waits for the fake shell's prompt, then types a command. */
    async run(command: string) {
      await expect(rows).toContainText("$")
      await root.click()
      await page.keyboard.type(command)
      await page.keyboard.press("Enter")
    },
    /** A row that holds exactly text, such as a command's output. */
    line(text: string) {
      const escaped = text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
      return rows
        .locator("> div")
        .filter({ hasText: new RegExp(`^\\s*${escaped}\\s*$`) })
    },
  }
}
