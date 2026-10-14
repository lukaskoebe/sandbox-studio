/**
 * Static data for the browser revamp mockup: Finn's browser, one task it works on, and the
 * sites its profile knows. The site is made up; nothing here talks to Studio.
 */

export const teammate = { name: "Finn", avatar: "🔥" }

/** The sandboxes Finn owns: the agent drives the browser from one of them. */
export const sandboxes = ["api-refactor", "docs-site"]

/** The pages of the fake site, plus the gateway's hold page and a page for any other site. */
export type PageKind =
  "login" | "overview" | "webhooks" | "changelog" | "held" | "other"

/** What the viewport shows: a page, its address and what has happened on it. */
export type View = {
  page: PageKind
  url: string
  email?: boolean
  password?: boolean
  testSent?: boolean
}

export const blankLogin: View = {
  page: "login",
  url: "https://console.pebble.dev/login",
}

/** An approval the agent waits on, shown on the page next to the element it is about. */
export type Ask =
  | { kind: "credential"; secret: string; field: string }
  | { kind: "action"; verb: string; name: string }
  | { kind: "download"; file: string; size: string }

export type Step = {
  /** The tool call as the log names it. */
  tool: string
  /** One sentence, past tense. */
  text: string
  /** The element it acts on: a `data-target` in the fake page. */
  target?: string
  ask?: Ask
  /** The view after the step. */
  apply: (v: View) => View
}

/** Finn's task in this mockup, from a chat in api-refactor. */
export const task = {
  title: "Send a test webhook to staging and grab the event log",
  sandbox: "api-refactor",
}

export const script: Step[] = [
  {
    tool: "open",
    text: "Opened console.pebble.dev",
    apply: () => blankLogin,
  },
  {
    tool: "type",
    text: "Typed the email",
    target: "email",
    apply: (v) => ({ ...v, email: true }),
  },
  {
    tool: "fill_credential",
    text: "Filled the password from PEBBLE_LOGIN",
    target: "password",
    ask: { kind: "credential", secret: "PEBBLE_LOGIN", field: "Password" },
    apply: (v) => ({ ...v, password: true }),
  },
  {
    tool: "click",
    text: "Clicked “Sign in”",
    target: "signin",
    apply: () => ({
      page: "overview",
      url: "https://console.pebble.dev/overview",
    }),
  },
  {
    tool: "click",
    text: "Clicked “Webhooks”",
    target: "nav-webhooks",
    apply: () => ({
      page: "webhooks",
      url: "https://console.pebble.dev/webhooks",
    }),
  },
  {
    tool: "click",
    text: "Clicked “Send test event” on staging",
    target: "test-staging",
    ask: { kind: "action", verb: "click", name: "Send test event" },
    apply: (v) => ({ ...v, testSent: true }),
  },
  {
    tool: "snapshot",
    text: "Read the page",
    apply: (v) => v,
  },
  {
    tool: "download",
    text: "Downloaded events-staging.csv",
    target: "export",
    ask: { kind: "download", file: "events-staging.csv", size: "18 KB" },
    apply: (v) => v,
  },
]

/** A clock for the log: the task starts at 10:41 and takes about a minute a step. */
export function stepTime(i: number) {
  const m = 10 * 60 + 41 + i
  return `${Math.floor(m / 60)}:${String(m % 60).padStart(2, "0")}`
}

/** An earlier session in the log, collapsed. */
export const earlier = {
  when: "Yesterday",
  title: "Check the Vite 7.3 changelog for breaking changes",
  sandbox: "docs-site",
  steps: 6,
}

/** A throwaway session an agent has open next to the profile (N11). */
export const throwaway = {
  sandbox: "docs-site",
  view: {
    page: "changelog",
    url: "https://pebble.dev/changelog",
  } satisfies View,
}

export type ActionsPolicy = "ask" | "allow" | "block"

export type Site = {
  host: string
  /** Clicks, typing and selects on the site. */
  actions: ActionsPolicy
  /** Fields the agent never reads, like password fields. */
  hidden: string[]
  /** A vault login Studio fills on approval. */
  login?: string
  signedIn: boolean
  lastUsed: string
}

export const sites: Site[] = [
  {
    host: "console.pebble.dev",
    actions: "ask",
    hidden: ["API key", "Signing secret"],
    login: "PEBBLE_LOGIN",
    signedIn: true,
    lastUsed: "now",
  },
  {
    host: "github.com",
    actions: "allow",
    hidden: [],
    login: "GITHUB_FINN",
    signedIn: true,
    lastUsed: "2 h ago",
  },
  {
    host: "pebble.dev",
    actions: "allow",
    hidden: [],
    signedIn: false,
    lastUsed: "yesterday",
  },
  {
    host: "billing.pebble.dev",
    actions: "block",
    hidden: ["Card number", "CVC"],
    signedIn: true,
    lastUsed: "3 days ago",
  },
]

export type Download = {
  name: string
  size: string
  when: string
  sentTo?: string
}

export const earlierDownloads: Download[] = [
  {
    name: "vite-7.3-migration.pdf",
    size: "212 KB",
    when: "yesterday",
    sentTo: "docs-site",
  },
]

/** What the terminal beside the panel shows in the workspace. */
export const terminalLines = [
  "$ pnpm test webhooks",
  " ✓ verifies the signature (12 ms)",
  " ✓ rejects a stale timestamp (3 ms)",
  " ✘ handles the staging test event",
  "   Expected event type payment.succeeded, got ping",
  "",
  "finn › I'll send a real test event from the Pebble console",
  "       and pull the event log to compare.",
  "",
  "$ studio-agent browser open https://console.pebble.dev",
]
