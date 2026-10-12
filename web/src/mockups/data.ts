// Static fake data for the design mockups. Nothing here talks to Studio.

export type Tone = "network" | "git" | "browser" | "memory" | "agent"
export type Status = "running" | "waiting" | "failed" | "stopped"
export type Presence = "working" | "waiting" | "idle"

export const environment = { name: "Client project", slug: "client-project" }

export type Teammate = {
  id: string
  name: string
  role: string
  presence: Presence
  /** What they are doing right now, as one line. */
  activity: string
  harness: string
  sandbox?: string
}

export const teammates: Teammate[] = [
  {
    id: "robin",
    name: "Robin",
    role: "Generalist engineer",
    presence: "working",
    activity: "Wiring Stripe into the checkout",
    harness: "Claude Code · sonnet-4.6",
    sandbox: "shop-api",
  },
  {
    id: "mika",
    name: "Mika Laine",
    role: "Frontend",
    presence: "waiting",
    activity: "Waiting for you: click “Place order”",
    harness: "Codex · gpt-5.4",
    sandbox: "shop-web",
  },
  {
    id: "juno",
    name: "Juno",
    role: "Research",
    presence: "idle",
    activity: "Idle since 09:40",
    harness: "OpenCode · qwen3-coder",
    sandbox: "docs-crawler",
  },
]

export type Sandbox = {
  id: string
  name: string
  owner?: string
  status: Status
  template: string
  cpus: number
  memGiB: number
  cpuPct: number
  memUsedGiB: number
  ports: number[]
  approvals: number
  uptime: string
  note?: string
}

export const sandboxes: Sandbox[] = [
  {
    id: "sb_7f3a2c",
    name: "shop-api",
    owner: "Robin",
    status: "running",
    template: "web-app@3",
    cpus: 4,
    memGiB: 8,
    cpuPct: 38,
    memUsedGiB: 3.1,
    ports: [5173, 8080],
    approvals: 2,
    uptime: "2h 14m",
  },
  {
    id: "sb_19be04",
    name: "shop-web",
    owner: "Mika Laine",
    status: "waiting",
    template: "web-app@3",
    cpus: 2,
    memGiB: 4,
    cpuPct: 6,
    memUsedGiB: 1.4,
    ports: [3000],
    approvals: 2,
    uptime: "48m",
    note: "Waiting on a browser action",
  },
  {
    id: "sb_c0d911",
    name: "docs-crawler",
    owner: "Juno",
    status: "stopped",
    template: "python-3.13@1",
    cpus: 2,
    memGiB: 2,
    cpuPct: 0,
    memUsedGiB: 0,
    ports: [],
    approvals: 0,
    uptime: "stopped 3 d ago",
  },
  {
    id: "sb_4e0f87",
    name: "scratch",
    status: "failed",
    template: "base@12",
    cpus: 1,
    memGiB: 1,
    cpuPct: 0,
    memUsedGiB: 0,
    ports: [],
    approvals: 0,
    uptime: "crashed 12 min ago",
    note: "Out of memory during boot",
  },
]

export const setupSteps = [
  { title: "Host check", done: true },
  { title: "Work mode", done: true },
  { title: "AI provider", done: true },
  { title: "Teammate", done: true },
  { title: "Git", done: false, hint: "Use your SSH agent for github.com" },
  { title: "Network level", done: false, hint: "Guarded is recommended" },
  { title: "First sandbox", done: true },
]

export const longHost =
  "telemetry.eu-central-1.observability.internal-metrics.example-cloud.com"

export const groupedHosts = [
  "docs.example.com",
  "cdn.docs.example.com",
  "fonts.googleapis.com",
  "fonts.gstatic.com",
  "www.googletagmanager.com",
  "consent.usercentrics.eu",
  "static.licdn.com",
  "upload.wikimedia.org",
  "api.segment.io",
]

export const gitPush = {
  sandbox: "shop-api",
  teammate: "Robin",
  branch: "feat/checkout",
  remote: "github/acme/shop",
  files: 6,
  additions: 128,
  deletions: 14,
  commits: [
    { sha: "a41c9e2", message: "Add Stripe payment intent endpoint" },
    { sha: "6be0d17", message: "Validate cart totals server-side" },
    { sha: "f02a5b3", message: "Show order summary before payment" },
  ],
}

export const browserAction = {
  sandbox: "shop-web",
  teammate: "Mika Laine",
  action: "click",
  label: "Place order",
  origin: "shop.example.com",
  url: "https://shop.example.com/checkout/review?cart=8812",
}

export type Connection = {
  host: string
  port: number
  verdict: "allowed" | "held" | "denied" | "open"
  requests: number
  last: string
  rule?: string
  tone?: Tone
}

export const connections: Connection[] = [
  {
    host: "js.stripe.com",
    port: 443,
    verdict: "held",
    requests: 1,
    last: "12 s",
  },
  {
    host: "registry.npmjs.org",
    port: 443,
    verdict: "allowed",
    requests: 214,
    last: "40 s",
    rule: "curated",
  },
  {
    host: "api.github.com",
    port: 443,
    verdict: "allowed",
    requests: 9,
    last: "3 min",
    rule: "*.github.com",
  },
  {
    host: "api.stripe.com",
    port: 443,
    verdict: "allowed",
    requests: 31,
    last: "4 min",
    rule: "manual",
  },
  {
    host: longHost,
    port: 443,
    verdict: "held",
    requests: 4,
    last: "6 min",
  },
  {
    host: "fonts.gstatic.com",
    port: 443,
    verdict: "allowed",
    requests: 12,
    last: "8 min",
    rule: "curated",
  },
  {
    host: "10.0.4.12",
    port: 5432,
    verdict: "denied",
    requests: 3,
    last: "21 min",
    rule: "private network",
  },
  {
    host: "localhost",
    port: 5173,
    verdict: "open",
    requests: 88,
    last: "now",
  },
]

export const sessions = [
  { name: "dev server", kind: "terminal" as const, active: true },
  { name: "tests", kind: "terminal" as const },
  { name: "Robin · checkout", kind: "chat" as const },
]

export const previews = [
  { port: 5173, label: "Vite" },
  { port: 8080, label: "API" },
]

/** One terminal line: segments with an optional color role. */
export type TermSegment = {
  text: string
  role?: "prompt" | "path" | "dim" | "ok" | "warn" | "accent" | "add" | "del"
}

const prompt = (cmd: string): TermSegment[] => [
  { text: "agent@shop-api", role: "prompt" },
  { text: ":" },
  { text: "/workspace/shop", role: "path" },
  { text: "$ " },
  { text: cmd },
]

export const terminal: TermSegment[][] = [
  prompt("pnpm dev"),
  [],
  [
    { text: "  VITE v8.3.3", role: "ok" },
    { text: "  ready in ", role: "dim" },
    { text: "412 ms" },
  ],
  [],
  [
    { text: "  ➜  Local:   ", role: "dim" },
    { text: "http://localhost:5173/", role: "accent" },
  ],
  [{ text: "  ➜  Network: use --host to expose", role: "dim" }],
  [],
  prompt("git status -sb"),
  [
    { text: "## feat/checkout", role: "ok" },
    { text: "...origin/feat/checkout " },
    { text: "[ahead 3]", role: "warn" },
  ],
  [{ text: " M", role: "del" }, { text: " src/routes/checkout.tsx" }],
  [{ text: " M", role: "del" }, { text: " src/lib/cart.ts" }],
  [{ text: "??", role: "add" }, { text: " src/components/order-summary.tsx" }],
  prompt("pnpm add @stripe/stripe-js"),
  [
    { text: "Packages: ", role: "dim" },
    { text: "+1", role: "add" },
  ],
  [
    { text: "⠏ ", role: "warn" },
    { text: "Waiting for approval: ", role: "warn" },
    { text: "js.stripe.com:443" },
  ],
]
