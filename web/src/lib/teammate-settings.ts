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
        label: "Model",
        keywords: "agent harness provider claude codex",
      },
      {
        id: "git",
        label: "Commits as",
        keywords: "git identity author email signing",
      },
      {
        id: "soul",
        label: "Instructions",
        keywords: "soul prompt personality",
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
    title: "Network",
    rows: [
      { id: "network", label: "New hosts", keywords: "network level security" },
      {
        id: "approvals",
        label: "New rules cover",
        keywords: "approvals scope ask permissions",
      },
      { id: "rules", label: "Rules", keywords: "network allow deny hosts" },
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
