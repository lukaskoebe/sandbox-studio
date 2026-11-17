/**
 * A teammate's settings page by its groups, as the page lists them; each row opens by its id
 * (?profile=settings&open=). The breadcrumb's menu and the palette list the same rows; the
 * keywords are more words the palette finds a row by.
 */
export const teammateSettings: {
  title?: string
  rows: { id: string; label: string; keywords: string }[]
}[] = [
  {
    title: "Profile",
    rows: [
      {
        id: "identity",
        label: "Identity",
        keywords: "name role avatar description",
      },
      {
        id: "agent",
        label: "Agent",
        keywords: "harness model provider claude codex",
      },
      {
        id: "git",
        label: "Git identity",
        keywords: "author email commits signing",
      },
      {
        id: "approvals",
        label: "Approvals",
        keywords: "ask permissions tools",
      },
      {
        id: "soul",
        label: "Soul",
        keywords: "instructions prompt personality",
      },
    ],
  },
  {
    title: "Knowledge",
    rows: [
      { id: "memory", label: "Memory", keywords: "facts knowledge learned" },
      { id: "tasks", label: "Tasks", keywords: "todo backlog" },
    ],
  },
  {
    title: "Routine and access",
    rows: [
      {
        id: "checkins",
        label: "Check-ins",
        keywords: "heartbeat schedule routine",
      },
      { id: "network", label: "New hosts", keywords: "network level security" },
      { id: "rules", label: "Network rules", keywords: "allow deny hosts" },
      { id: "browser", label: "Browser", keywords: "stealth" },
      { id: "secrets", label: "Secrets", keywords: "keys tokens credentials" },
      {
        id: "copy",
        label: "Copy to another environment",
        keywords: "duplicate clone",
      },
    ],
  },
  { rows: [{ id: "delete", label: "Delete", keywords: "remove" }] },
]
