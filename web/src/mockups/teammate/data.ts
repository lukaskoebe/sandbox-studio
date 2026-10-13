/** Static data for the teammate page concepts. One teammate, one owner, one week of work. */

export type MockPresence = "idle" | "working" | "waiting"

export const finn = {
  name: "Finn",
  avatar: "🔥",
  role: "Colleague developer and buddy",
  soul: "A friendly and chill software developer, coworker and buddy at work. Always open to new ideas and improving the quality of work. Talks casually and has a cheeky sense of humor.",
  harness: "OpenCode",
  model: "vllm/coding",
  provider: "Local vLLM",
  sandboxes: [
    { name: "finn", status: "running" as const, port: 4096 },
    { name: "docs-site", status: "stopped" as const },
  ],
}

export const owner = { name: "Lukas" }

/** What the teammate is doing right now, step by step, when it works. */
export const currentWork = {
  title: "Fix the flaky login test",
  sandbox: "finn",
  started: "6 min ago",
  steps: [
    { label: "Read e2e/login.spec.ts", done: true },
    { label: "Ran the test 20 times: 3 failures", done: true },
    { label: "Found a race on the session cookie", done: true },
    { label: "Patching fixtures.ts", done: false },
    { label: "Re-run 20×, then push for review", done: false },
  ],
}

export const terminalTicker = [
  "$ pnpm exec playwright test e2e/login.spec.ts --repeat-each=20",
  "Running 20 tests using 4 workers",
  "  ✓ 1 login › signs in with a fresh link (1.2s)",
  "  ✓ 2 login › signs in with a fresh link (1.1s)",
  "  ✘ 3 login › signs in with a fresh link (5.0s)",
  "    Error: expect(page).toHaveURL — cookie not set yet",
  "$ rg -n 'setCookie' e2e/fixtures.ts",
  "42:  await ctx.addCookies([session]) // before goto?",
  "$ git diff --stat",
  " e2e/fixtures.ts | 6 ++++--",
  "  ✓ 20 passed (24.1s)",
]

export type ThreadItem =
  | { kind: "day"; label: string }
  | { kind: "you"; text: string; time: string }
  | { kind: "them"; text: string; time: string }
  | {
      kind: "activity"
      area: "browser" | "git" | "network" | "chat" | "memory"
      text: string
      detail?: string
      sandbox?: string
      time: string
    }
  | { kind: "where"; sandbox: string; note: string }
  | { kind: "learned"; fact: string; time: string; state?: "kept" | "forgot" }
  | {
      kind: "ask"
      text: string
      detail: string
      time: string
      state?: "allowed" | "denied"
    }
  | { kind: "checkin"; title: string; text: string; time: string }

export const thread: ThreadItem[] = [
  { kind: "day", label: "Yesterday" },
  {
    kind: "checkin",
    title: "Morning check-in",
    text: "Two dependency updates are waiting: vite 7.3.2 and @xterm/xterm 6.0.1. Both minor; I can open a branch when you want.",
    time: "09:00",
  },
  {
    kind: "you",
    text: "Can you check the english wikipedia landing page?",
    time: "20:31",
  },
  {
    kind: "activity",
    area: "browser",
    text: "Opened en.wikipedia.org in its browser",
    detail: "read the accessibility tree, 412 nodes",
    time: "20:31",
  },
  {
    kind: "them",
    text: "Loaded fine. Today's featured article is about Sarah Goodridge, and the featured picture is a humpback whale. Nothing out of the ordinary.",
    time: "20:32",
  },
  {
    kind: "you",
    text: "Woah, the bubble-net thing is awesome. Remember that.",
    time: "20:40",
  },
  {
    kind: "learned",
    fact: "Lukas finds humpback whale bubble-net feeding fascinating.",
    time: "20:40",
    state: "kept",
  },
  {
    kind: "you",
    text: "Could you also fix the broken links on the docs site?",
    time: "20:52",
  },
  {
    kind: "where",
    sandbox: "docs-site",
    note: "The site's repository lives there",
  },
  {
    kind: "activity",
    area: "git",
    text: "Pushed docs/broken-links",
    detail: "12 links, 4 files",
    sandbox: "docs-site",
    time: "21:05",
  },
  {
    kind: "them",
    text: "Done, twelve links fixed and pushed as docs/broken-links.",
    time: "21:06",
  },
  { kind: "day", label: "Today" },
  {
    kind: "where",
    sandbox: "finn",
    note: "Back in the main project",
  },
  {
    kind: "you",
    text: "The login e2e test fails every few runs on CI. Can you find out why and fix it?",
    time: "10:12",
  },
  {
    kind: "activity",
    area: "chat",
    text: "Started on the task",
    detail: "Fix the flaky login test",
    sandbox: "finn",
    time: "10:12",
  },
  {
    kind: "ask",
    text: "Wants to reach registry.npmjs.org:443",
    detail: "to install @playwright/test in finn",
    time: "10:13",
  },
  {
    kind: "learned",
    fact: "In this repo, e2e tests run with `pnpm exec playwright test --workers=1`; parallel runs share one Studio.",
    time: "10:15",
  },
  {
    kind: "them",
    text: "Found it, I think: the session cookie is added after the first navigation in fixtures.ts, so a fast page beats it. Patching now, then I'll hammer it 20× before pushing.",
    time: "10:18",
  },
  {
    kind: "you",
    text: "Is yesterday's link fix in here too?",
    time: "10:20",
  },
  {
    kind: "them",
    text: "No, that was in docs-site, and finn doesn't have that branch: I can't see that sandbox from here. I can pull docs/broken-links from GitHub into finn, or you continue in docs-site. Which do you prefer?",
    time: "10:20",
  },
]

/** The board of the desk concept: everything between the owner and the teammate. */
export type Card = {
  id: string
  lane: "asked" | "doing" | "needs" | "done"
  kind: "chat" | "task" | "approval" | "push" | "checkin"
  title: string
  meta: string
}

export const board: Card[] = [
  {
    id: "c1",
    lane: "asked",
    kind: "task",
    title: "Bump vite and xterm",
    meta: "from the morning check-in",
  },
  {
    id: "c2",
    lane: "doing",
    kind: "task",
    title: "Fix the flaky login test",
    meta: "in finn · 6 min",
  },
  {
    id: "c3",
    lane: "needs",
    kind: "approval",
    title: "registry.npmjs.org:443",
    meta: "to install @playwright/test",
  },
  {
    id: "c4",
    lane: "needs",
    kind: "push",
    title: "Push docs/typo-fixes",
    meta: "3 files · +12 −9",
  },
  {
    id: "c5",
    lane: "done",
    kind: "chat",
    title: "Checked the Wikipedia landing page",
    meta: "yesterday",
  },
  {
    id: "c6",
    lane: "done",
    kind: "checkin",
    title: "Dependency check",
    meta: "yesterday 09:00 · 2 updates",
  },
]

/** Activity per day for the last two weeks, oldest first: turns and things learned. */
export const activity = [
  { turns: 4, learned: 0 },
  { turns: 0, learned: 0 },
  { turns: 12, learned: 2 },
  { turns: 7, learned: 1 },
  { turns: 0, learned: 0 },
  { turns: 0, learned: 0 },
  { turns: 3, learned: 0 },
  { turns: 18, learned: 3 },
  { turns: 9, learned: 1 },
  { turns: 2, learned: 0 },
  { turns: 0, learned: 0 },
  { turns: 6, learned: 1 },
  { turns: 11, learned: 2 },
  { turns: 8, learned: 2 },
]

/** The journal concept: one entry per day, written by the teammate from its sessions. */
export type JournalDay = {
  day: string
  date: string
  summary: string
  shipped: string[]
  learned: string[]
  stuck?: string
}

export const journal: JournalDay[] = [
  {
    day: "Today",
    date: "Tue 13 Oct",
    summary:
      "Spent the morning on the flaky login test. The session cookie was set after the first page load, so fast runs beat it. The fix is in, and 20 runs in a row pass. Waiting on you for the push.",
    shipped: ["fix/flaky-login (waiting for review)"],
    learned: [
      "e2e tests run with --workers=1 here; parallel runs share one Studio.",
      "CI's Chromium is the snap one at /snap/bin/chromium.",
    ],
    stuck:
      "May I add registry.npmjs.org to the allowed hosts for good? I ask for it every time I install a dev dependency.",
  },
  {
    day: "Yesterday",
    date: "Mon 12 Oct",
    summary:
      "Quiet day. The morning check-in found two minor dependency updates. In the evening you asked me to look at Wikipedia, and we nerded out about humpback whales.",
    shipped: [],
    learned: ["Lukas finds bubble-net feeding fascinating."],
  },
  {
    day: "Sunday",
    date: "Sun 11 Oct",
    summary:
      "Set up the docs-site sandbox and fixed twelve broken links. The Astro build takes 40 s; most of it is image processing.",
    shipped: ["docs/broken-links (merged)"],
    learned: [
      "The docs site builds with `cd site && pnpm build`.",
      "Lukas prefers single-line commit messages.",
    ],
  },
]

/** The memory map: topics with how much the teammate knows about each. */
export const topics = [
  {
    id: "you",
    label: "Lukas",
    facts: [
      "Prefers single-line commit messages.",
      "Finds bubble-net feeding fascinating.",
      "Reviews pushes in the evening.",
    ],
  },
  {
    id: "repo",
    label: "sandbox-studio",
    facts: [
      "e2e: pnpm exec playwright test --workers=1.",
      "Go checks need the nobadger,nomysql,nopgx tags.",
      "Never run claude or codex on the host.",
      "Migrations are numbered; the last is 033.",
      "The UI style guide is docs/design/ui-style.md.",
    ],
  },
  {
    id: "site",
    label: "docs site",
    facts: [
      "Builds with cd site && pnpm build.",
      "Image processing is most of the build time.",
    ],
  },
  {
    id: "tools",
    label: "tooling",
    facts: [
      "CI's Chromium is /snap/bin/chromium.",
      "pnpm keeps minimumReleaseAge 1440.",
    ],
  },
  { id: "web", label: "the web", facts: ["Wikipedia's main page is light."] },
]
