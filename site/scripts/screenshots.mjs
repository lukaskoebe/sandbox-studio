// Captures the screenshots in src/assets/screenshots from a running scratch Studio.
//
//   node scripts/screenshots.mjs '<login link>'
//
// Run it against a throwaway Studio (its own SANDBOX_STUDIO_HOME and -addr), never your
// real one: it seeds demo data with fake keys, creates two sandboxes and leaves them for
// you to delete. It uses an installed Chromium (CHROMIUM, default /snap/bin/chromium) and
// never downloads a browser. No harness session is started, so no model API is called.
import { mkdir } from "node:fs/promises"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright-core"

const link = process.argv[2]
if (!link?.includes("#studio-login=")) {
  console.error("usage: node scripts/screenshots.mjs '<login link printed by studio>'")
  process.exit(2)
}
const base = new URL(link).origin
const out = fileURLToPath(new URL("../src/assets/screenshots/", import.meta.url))
await mkdir(out, { recursive: true })

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM ?? "/snap/bin/chromium",
  headless: true,
})
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  colorScheme: "light",
  baseURL: base,
})
const page = await context.newPage()
await page.goto(link)
await page.getByText("Sandboxes", { exact: true }).first().waitFor()

// --- API helpers (the page's session cookie authenticates them) ---------------------------

async function api(method, path, body) {
  const res = await page.request.fetch(path, {
    method,
    data: body,
    headers: body ? { "content-type": "application/json" } : undefined,
  })
  if (!res.ok()) throw new Error(`${method} ${path}: ${res.status()} ${await res.text()}`)
  return res.status() === 204 ? null : res.json()
}
const list = async (path) => (await api("GET", path)) ?? []
async function ensure(path, match, body) {
  const found = (await list(path)).find(match)
  return found ?? api("POST", path, body)
}

// --- Demo data ----------------------------------------------------------------------------

const FAKE_KEY = "sk-demo-not-a-real-key"
const envs = await list("/api/environments")
const env = envs[0].id
await ensure("/api/environments", (e) => e.name === "Private", { name: "Private" })
const E = `/api/environments/${env}`

const anthropic = await ensure(`${E}/providers`, (p) => p.name === "anthropic", {
  name: "anthropic",
  kind: "anthropic_api",
  apiKey: FAKE_KEY,
})
const openai = await ensure(`${E}/providers`, (p) => p.name === "openai", {
  name: "openai",
  kind: "openai_api",
  apiKey: FAKE_KEY,
})
const ada = await ensure(`${E}/teammates`, (t) => t.name === "Ada", {
  name: "Ada",
  role: "Backend engineer",
  soul: "You are careful and test-driven. Prefer small, reviewable commits and explain trade-offs briefly.",
  harness: "claude",
  providerId: anthropic.id,
  model: "claude-sonnet-4-5",
})
const linus = await ensure(`${E}/teammates`, (t) => t.name === "Linus", {
  name: "Linus",
  role: "Frontend engineer",
  soul: "You care about accessibility and fast pages. Keep the UI consistent with the design system.",
  harness: "opencode",
  providerId: openai.id,
  model: "gpt-5.1-codex",
})
await ensure(`${E}/secrets`, (s) => s.name === "STRIPE_TEST_KEY", {
  name: "STRIPE_TEST_KEY",
  value: "sk_test_demo_not_a_real_key",
  hosts: ["api.stripe.com"],
  note: "Stripe test mode",
})
for (const rule of [
  { host: "registry.npmjs.org", action: "allow", ports: [443], note: "npm packages" },
  { host: "*.pypi.org", action: "allow", ports: [443] },
  { host: "files.pythonhosted.org", action: "allow", ports: [443] },
  { host: "*.doubleclick.net", action: "deny", note: "No ad trackers" },
  {
    host: "api.stripe.com",
    action: "proxy",
    ports: [443],
    config: { headers: { Authorization: "Bearer {secret.STRIPE_TEST_KEY}" } },
    note: "Adds the test key on the way out",
  },
  { host: "storybook.js.org", action: "allow", ports: [443], teammateId: linus.id },
]) {
  await ensure(`${E}/rules`, (r) => r.host === rule.host, rule)
}
await ensure(`${E}/forges`, (f) => f.name === "gh", {
  name: "gh",
  kind: "github",
  baseUrl: "https://github.com",
  token: "github_pat_demo_not_a_real_token",
})
// Facts without entities or attributes never pair up, so consolidation has nothing to judge.
const facts = await list(`${E}/memory/facts?scope=shared`)
for (const text of [
  "The webshop uses PostgreSQL 17 and runs migrations with goose.",
  "Releases are cut from main every second Tuesday.",
  "Prefer pnpm over npm in every JavaScript project.",
]) {
  if (!facts.some?.((f) => f.text === text))
    await api("POST", `${E}/memory/facts`, { kind: "fact", text, scope: "shared" })
}
await ensure(`${E}/memory/pages`, (p) => p.slug === "project/webshop", {
  title: "Webshop",
  slug: "project/webshop",
  kind: "project",
  scope: "shared",
  compiled:
    "A Go API with a React storefront.\n\n- `api/` holds the HTTP handlers and the PostgreSQL schema.\n- `web/` is the storefront, built with Vite.\n- Run `make test` before pushing.",
})

const webshop = await ensure(`${E}/sandboxes`, (s) => s.name === "webshop", {
  name: "webshop",
  teammateId: ada.id,
})
const storefront = await ensure(`${E}/sandboxes`, (s) => s.name === "storefront", {
  name: "storefront",
  teammateId: linus.id,
})

for (const pattern of [
  { action: "click", origin: "https://github.com", label: "Sign in*", verdict: "allow" },
  { action: "*", origin: "https://dashboard.stripe.com", verdict: "deny" },
  { action: "fill", origin: "*", label: "*card number*", verdict: "sensitive" },
]) {
  await ensure(
    `${E}/teammates/${ada.id}/browser/patterns`,
    (p) => p.origin === pattern.origin && p.verdict === pattern.verdict,
    pattern
  )
}
const checkpoints = await list(`${E}/sandboxes/${storefront.id}/checkpoints`)
if (!checkpoints.some?.((c) => c.name === "before-upgrade")) {
  await api("POST", `${E}/sandboxes/${storefront.id}/checkpoints`, { name: "before-upgrade" })
}

// --- Screenshots --------------------------------------------------------------------------

// The scratch instance runs on other ports and paths; show the defaults instead.
async function tidy(p = page) {
  await p.evaluate(() => {
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      n.nodeValue = n.nodeValue
        .replace(/\/home\/[^/\s]+\/git\/\.scratch-docs-site\/data/g, "~/.local/share/sandbox-studio")
        .replace(/\/home\/[^/\s]+\//g, "~/")
        .replace(/127\.0\.0\.1:79(7[89]|80)/g, (_, p) => `127.0.0.1:78${p}`)
    }
  })
}
async function shot(name, p = page) {
  await p.waitForLoadState("networkidle").catch(() => {})
  await p.waitForTimeout(600)
  await tidy(p)
  await p.screenshot({ path: `${out}${name}.jpg`, type: "jpeg", quality: 82 })
  console.log("captured", name)
}
async function go(path) {
  await page.goto(`/e/${env}${path}`)
  await page.waitForTimeout(1200)
}
const button = (name, p = page) => p.getByRole("button", { name, exact: true }).first()
async function closeDialog() {
  await page.keyboard.press("Escape")
  await page.getByRole("dialog").first().waitFor({ state: "detached", timeout: 5000 }).catch(() => {})
  await page.waitForTimeout(400)
}

// Login screen, in a context without a session.
{
  const fresh = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: "light" })
  const p = await fresh.newPage()
  await p.goto(base)
  await p.getByText("Connect to Studio").waitFor()
  await shot("login", p)
  await fresh.close()
}

await go("")
await shot("overview")
await button("New sandbox").click()
await page.getByLabel("Name").fill("my-project")
await shot("new-sandbox")
await closeDialog()

await page.getByRole("button", { name: /Default/ }).first().click()
await shot("environments")
await closeDialog()

// A terminal with a shell, then a request to an unknown host that waits for approval.
await go(`/sandboxes/${webshop.id}`)
await page.locator(".xterm").first().click()
await page.keyboard.type("clear\n")
await page.waitForTimeout(500)
await page.keyboard.type("cd /workspace && uname -srm && docker --version\n", { delay: 15 })
await page.waitForTimeout(2500)
await shot("sandbox-terminal")
await page.keyboard.type("curl -sI https://example.com | head -1\n", { delay: 15 })
await page.getByText("example.com", { exact: false }).last().waitFor({ timeout: 30000 })
await page.waitForTimeout(1500)
await shot("approval")
await button("Ignore").click().catch(() => {})
await page.waitForTimeout(1000)

await button("Connections").click()
await page.waitForTimeout(1000)
await shot("connections")
await closeDialog()

await go(`/sandboxes/${storefront.id}`)
await button("Checkpoints").click()
await page.waitForTimeout(1000)
await shot("checkpoints")
await closeDialog()

await go("/network")
await shot("network")
await button("Add rule").click()
await page.getByLabel("Host").fill("api.openai.com")
await page.getByRole("radio", { name: "Proxy" }).click().catch(() => page.getByText("Proxy", { exact: true }).click())
await shot("rule-dialog")
await closeDialog()

await go("/secrets")
await shot("secrets")
await go("/providers")
await shot("providers")
await button("Add provider").click()
await page.waitForTimeout(500)
await shot("provider-dialog")
await closeDialog()

await go("/teammates")
await shot("teammates")
await page.getByRole("button", { name: /Add a teammate/ }).first().click()
await page.waitForTimeout(500)
await shot("teammate-dialog")
await closeDialog()

await go("/git")
await shot("git")
await button("Add forge").click()
await page.waitForTimeout(500)
await shot("forge-dialog")
await closeDialog()

await go("/memory")
await page.getByRole("tab", { name: "Facts" }).click()
await shot("memory")

await go("/builds")
await button("New build").click()
await page.waitForTimeout(500)
await shot("build-dialog")
await closeDialog()

await go("/browser")
await page.getByRole("tab", { name: "Sites" }).click()
await shot("browser")

await go("/settings")
await shot("settings")

// A dark hero for the landing page.
{
  const dark = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: "dark", baseURL: base, storageState: await context.storageState() })
  const p = await dark.newPage()
  await p.emulateMedia({ colorScheme: "dark" })
  await p.goto(`/e/${env}/sandboxes/${webshop.id}`)
  await p.waitForTimeout(2500)
  console.log(await p.evaluate(() => [matchMedia("(prefers-color-scheme: dark)").matches, document.documentElement.className, getComputedStyle(document.body).backgroundColor]))
  await shot("hero-dark", p)
  await dark.close()
}

await browser.close()
