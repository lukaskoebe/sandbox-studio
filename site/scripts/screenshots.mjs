// Captures the screenshots in src/assets/screenshots from a running scratch Studio.
//
//   node scripts/screenshots.mjs '<login link>' [name ...]
//
// Run it against a throwaway Studio (its own SANDBOX_STUDIO_HOME and -addr), never your
// real one: it seeds demo data with fake keys, creates two sandboxes and leaves them for
// you to delete. It uses an installed Chromium (CHROMIUM, default /snap/bin/chromium) and
// never downloads a browser. No harness session is started, so no model API is called.
// Names after the link capture only those screenshots; the login link works once, so later
// runs pass the session cookie Studio's first run saved instead (see `session` below).
import { mkdir, readFile, writeFile } from "node:fs/promises"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright-core"

const link = process.argv[2]
if (!link?.includes("#studio-login=") && link !== "again") {
  console.error("usage: node scripts/screenshots.mjs '<login link printed by studio>' [name ...]")
  console.error("       node scripts/screenshots.mjs again [name ...]")
  process.exit(2)
}
const only = new Set(process.argv.slice(3))
const wanted = (name) => only.size === 0 || only.has(name)
const out = fileURLToPath(new URL("../src/assets/screenshots/", import.meta.url))
const session = fileURLToPath(new URL("../.screenshots-session.json", import.meta.url))
await mkdir(out, { recursive: true })

const saved = link === "again" ? JSON.parse(await readFile(session, "utf8")) : null
const base = saved?.base ?? new URL(link).origin
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM ?? "/snap/bin/chromium",
  headless: true,
})
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  colorScheme: "dark",
  // Granted, so Home doesn't ask for them in every shot.
  permissions: ["notifications"],
  baseURL: base,
  storageState: saved?.state,
})
const page = await context.newPage()
if (!saved) {
  // The first start opens the guided setup.
  await page.goto(link)
  await page.getByRole("button", { name: /Host check/ }).waitFor()
  await page.waitForTimeout(1500)
  if (wanted("setup")) await page.screenshot({ path: `${out}setup.jpg`, type: "jpeg", quality: 82 })
  await writeFile(session, JSON.stringify({ base, state: await context.storageState() }))
}

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
// The guided setup out of the way.
await api("PUT", `/api/setup?env=${env}`, { steps: {}, dismissed: true })
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
async function running(sb) {
  for (let i = 0; i < 300; i++) {
    const now = await api("GET", `${E}/sandboxes/${sb.id}`)
    if (now.status === "running") return
    if (now.status === "stopped") await api("POST", `${E}/sandboxes/${sb.id}/start`)
    await page.waitForTimeout(1000)
  }
  throw new Error(`${sb.name} did not start`)
}
/** Dismisses what is still waiting, such as the requests of an earlier step. */
async function clearApprovals() {
  for (const a of await list("/api/approvals?status=pending"))
    await api("POST", `/api/environments/${a.environmentId}/approvals/${a.id}/decide`, { action: "dismiss", scope: "environment" })
}
/** Runs one screenshot step; a failure is reported and the next step goes on. */
async function step(name, fn) {
  if (!wanted(name)) return
  try {
    await fn()
  } catch (err) {
    console.error(`failed ${name}: ${err.message.split("\n")[0]}`)
    await page.keyboard.press("Escape").catch(() => {})
  }
}

// The first run captures the setup as it opens; later runs open it again.
await step("setup", async () => {
  if (!saved) return
  await page.goto("/setup")
  await page.getByRole("button", { name: /Host check/ }).waitFor()
  await page.waitForTimeout(1500)
  await shot("setup")
})
await step("new-sandbox", async () => {
  await go("")
  await page.getByLabel("More to create").click()
  await page.getByRole("menuitem", { name: /Sandbox without a teammate/ }).click()
  await page.getByLabel("Name").fill("my-project")
  await shot("new-sandbox")
  await closeDialog()
})
await step("environments", async () => {
  await go("")
  await page.getByRole("button", { name: /Default/ }).first().click()
  await shot("environments")
  await closeDialog()
})

// A terminal with a shell, then a request to an unknown host that waits for approval.
await step("sandbox-terminal", async () => {
  await running(webshop)
  await go(`/sandboxes/${webshop.id}/work`)
  await page.locator(".xterm").first().click()
  await page.keyboard.type("clear\n")
  await page.waitForTimeout(500)
  await page.keyboard.type("cd /workspace && uname -srm && docker --version\n", { delay: 15 })
  // The first docker call in a fresh VM takes seconds; xterm draws on a canvas, so there
  // is no text to wait for.
  await page.waitForTimeout(8000)
  await shot("sandbox-terminal")
})
await step("approval", async () => {
  // An earlier run's Deny left a rule for the host, so nothing would ask.
  for (const r of await list(`${E}/rules`))
    if (r.host === "example.com") await api("DELETE", `${E}/rules/${r.id}`)
  await running(webshop)
  await go(`/sandboxes/${webshop.id}/work`)
  await page.locator(".xterm").first().click()
  await page.keyboard.type("clear\n")
  await page.keyboard.type("curl -sI https://example.com | head -1\n", { delay: 15 })
  await page.getByTestId("approval-card").first().waitFor({ timeout: 30000 })
  await page.waitForTimeout(1500)
  await shot("approval")
  await page.getByTestId("approval-card").first().getByRole("button", { name: "Deny", exact: true }).click()
})

await step("checkpoints", async () => {
  // One VM at a time: the terminal's sandbox stops first.
  await api("POST", `${E}/sandboxes/${webshop.id}/stop`).catch(() => {})
  await running(storefront)
  const checkpoints = await list(`${E}/sandboxes/${storefront.id}/checkpoints`)
  if (!checkpoints.some?.((c) => c.name === "before-upgrade"))
    await api("POST", `${E}/sandboxes/${storefront.id}/checkpoints`, { name: "before-upgrade" })
  // A sandbox's checkpoints are in the sheet its header's ⋯ menu opens.
  await go(`/sandboxes/${storefront.id}`)
  await page.getByLabel("Sandbox options").click()
  await page.getByRole("menuitem", { name: /History and checkpoints/ }).click()
  await page.getByRole("region", { name: "Checkpoints" }).waitFor()
  await shot("checkpoints")
})

await step("rule-dialog", async () => {
  await go("/network/rules")
  await button("Add rule").click()
  await page.getByRole("dialog").getByLabel("Host", { exact: true }).fill("api.openai.com")
  await page.getByRole("dialog").getByText("Proxy", { exact: true }).click()
  await shot("rule-dialog")
  await closeDialog()
})
await step("secrets", async () => {
  await go("/secrets")
  await shot("secrets")
})
await step("providers", async () => {
  await go("?settings=providers")
  await shot("providers")
})
await step("teammates", async () => {
  await go("/teammates")
  await shot("teammates")
})
await step("git", async () => {
  await go("?settings=git")
  await shot("git")
})
await step("memory", async () => {
  await go("/memory")
  await page.getByRole("tab", { name: "Facts" }).click()
  await shot("memory")
})
await step("build-dialog", async () => {
  await go("?settings=templates")
  await button("New build").click()
  await page.getByRole("dialog", { name: /build/i }).waitFor()
  await page.waitForTimeout(500)
  await shot("build-dialog")
  await closeDialog()
})

// Ada's browser on a real page, which you drive from the address bar, with her sites beside
// it. It comes last: the sandboxes stop first, to leave memory for the browser VM.
await step("browser", async () => {
  for (const sb of [webshop, storefront]) await api("POST", `${E}/sandboxes/${sb.id}/stop`).catch(() => {})
  for (const host of ["*.wikipedia.org", "*.wikimedia.org"])
    await ensure(`${E}/rules`, (r) => r.host === host, { host, action: "allow", ports: [443], teammateId: ada.id })
  await go(`/teammates/${ada.id}/browser`)
  // A sleeping browser has one field to open a page; a running one has the address bar.
  const address = page.getByLabel("Open a page").or(page.getByLabel("Browser address"))
  await address.fill("en.wikipedia.org/wiki/Sandbox_(computer_security)")
  await address.press("Enter")
  await page.getByLabel("Live view of the browser").waitFor({ timeout: 180000 })
  await page.getByLabel("Browser address").and(page.locator('[value="en.wikipedia.org/wiki/Sandbox_(computer_security)"]')).waitFor({ timeout: 60000 })
  await page.waitForTimeout(5000)
  await clearApprovals()
  await page.getByRole("tab", { name: "Sites" }).click()
  await shot("browser")
  await button(/Hand back/).click().catch(() => {})
})

await browser.close()
