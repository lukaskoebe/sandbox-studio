import {
  BrainIcon,
  ChatCircleDotsIcon,
  CursorClickIcon,
  EyeIcon,
  GitBranchIcon,
  GitPullRequestIcon,
  GlobeIcon,
  HandIcon,
  KeyIcon,
  ScalesIcon,
  ShieldIcon,
  type Icon,
} from "@phosphor-icons/react"
import type { Approval } from "@/lib/api/client"
import type { Area } from "@/lib/accents"

/**
 * How an approval kind is described wherever an approval shows up: the stack's cards, its
 * "+N more" link and browser notifications. Summaries are built from the decoded payload and
 * never include a secret value: a credential approval names the vault secret, not its value.
 */
export type ApprovalKind = {
  /** Short name of the kind, such as "Browser action". */
  title: string
  /** One line saying what is asked. */
  summary: (a: Approval) => string
  icon: Icon
  /** The area whose hue marks the kind. Unknown kinds have none. */
  area?: Area
  /** Whether the summary is a host or similar and reads best in monospace. */
  mono?: boolean
  /** The environment page that lists approvals of this kind. */
  page: "/e/$env" | "/e/$env/teammates/$id/browser" | "/e/$env/chats"
}

/**
 * Whether an approval is a network request: a connection (network) or a request on a connection
 * the gateway terminates (network.request). Both show on the Network page and the stack.
 */
export function isNetworkApproval(a: Approval): boolean {
  return a.kind === "network" || a.kind === "network.request"
}

/** What a git approval asks for. */
export function gitSummary(a: Approval): string {
  const git = a.git
  if (!git) return a.subject
  const where = [git.push.forgeName, git.push.owner, git.push.repo]
    .filter(Boolean)
    .join("/")
  if (git.pullRequest) {
    return `Open a pull request ${git.pullRequest.head} → ${git.pullRequest.base} on ${where}`
  }
  const commits =
    (git.review?.commits?.length ?? 0) + (git.review?.moreCommits ?? 0)
  const branch =
    git.review?.branch ?? git.push.ref.replace(/^refs\/heads\//, "")
  return `Push ${commits} ${commits === 1 ? "commit" : "commits"} to ${branch} on ${where}`
}

const conflictVerdicts: Record<string, string> = {
  contradiction: "Two memory facts contradict each other",
  temporal_supersession: "A memory fact may replace an older one",
  context_dependent: "Two memory facts disagree depending on context",
  duplicate: "Two memory facts look like duplicates",
}

const kinds: Record<string, ApprovalKind> = {
  network: {
    title: "Network access",
    summary: (a) =>
      a.network ? `${a.network.host}:${a.network.port}` : a.subject,
    icon: GlobeIcon,
    area: "network",
    mono: true,
    page: "/e/$env",
  },
  "git.push": {
    title: "Git push",
    summary: gitSummary,
    icon: GitBranchIcon,
    area: "git",
    page: "/e/$env",
  },
  "git.pr": {
    title: "Pull request",
    summary: gitSummary,
    icon: GitPullRequestIcon,
    area: "git",
    page: "/e/$env",
  },
  "memory.share": {
    title: "Memory share",
    summary: (a) =>
      `${a.memoryShare?.teammateName ?? "A teammate"} wants to share a fact with every teammate`,
    icon: BrainIcon,
    area: "memory",
    page: "/e/$env",
  },
  "memory.conflict": {
    title: "Memory conflict",
    summary: (a) =>
      conflictVerdicts[a.memoryConflict?.verdict ?? ""] ??
      "Two memory facts disagree",
    icon: ScalesIcon,
    area: "memory",
    page: "/e/$env",
  },
  "browser.action": {
    title: "Browser action",
    summary: (a) => {
      const act = a.browserAction
      if (!act) return a.subject
      // The text to type stays in the card's body: the summary also goes into notifications.
      const target = act.label
        ? ` “${act.label}”`
        : act.role
          ? ` ${act.role}`
          : ""
      return `${act.action}${target} on ${act.origin}`
    },
    icon: CursorClickIcon,
    area: "browser",
    page: "/e/$env/teammates/$id/browser",
  },
  "browser.credential": {
    title: "Browser credential",
    summary: (a) => {
      const cred = a.browserCredential
      return cred
        ? `Fill ${cred.credential} on ${cred.origin}`
        : "Fill a credential"
    },
    icon: KeyIcon,
    area: "browser",
    page: "/e/$env/teammates/$id/browser",
  },
  "browser.control": {
    title: "Browser handback",
    summary: (a) => {
      const ask = a.browserControl
      return ask
        ? `${ask.teammateName} asks for the browser back: ${ask.reason}`
        : a.subject
    },
    icon: HandIcon,
    area: "browser",
    page: "/e/$env/teammates/$id/browser",
  },
  "browser.show": {
    title: "Browser",
    summary: (a) => {
      const show = a.browserShow
      return show
        ? `${show.teammateName} wants to show you: ${show.title || show.url}`
        : a.subject
    },
    icon: EyeIcon,
    area: "browser",
    page: "/e/$env/teammates/$id/browser",
  },
  "chat.permission": {
    title: "Agent permission",
    summary: (a) => a.chatPermission?.title ?? a.subject,
    icon: ChatCircleDotsIcon,
    area: "chat",
    page: "/e/$env/chats",
  },
}

kinds["network.request"] = kinds.network

/** A kind this UI does not know yet: the server's subject line describes it. */
const unknownKind: ApprovalKind = {
  title: "Approval",
  summary: (a) => a.subject,
  icon: ShieldIcon,
  page: "/e/$env",
}

export function approvalKind(a: Approval): ApprovalKind {
  return kinds[a.kind] ?? unknownKind
}

/** The sandbox or teammate that asked, when the payload names one. */
/** The teammate whose browser asked, for the browser kinds that link to it. */
export function approvalTeammateId(a: Approval): string | undefined {
  return (
    a.browserAction?.teammateId ??
    a.browserCredential?.teammateId ??
    a.browserControl?.teammateId ??
    a.browserShow?.teammateId
  )
}

/** Whether an approval is a question the teammate's browser asks on its page. */
export function isBrowserAsk(a: Approval, teammate: string) {
  return (
    (a.kind === "browser.action" ||
      a.kind === "browser.credential" ||
      a.kind === "browser.control") &&
    approvalTeammateId(a) === teammate
  )
}

export function approvalSource(a: Approval): string | undefined {
  return (
    a.network?.sandboxName ??
    a.git?.review?.sandbox ??
    a.memoryShare?.teammateName ??
    a.browserAction?.sandboxName ??
    a.browserCredential?.sandboxName ??
    a.browserControl?.sandboxName ??
    a.browserShow?.sandboxName ??
    a.chatPermission?.sandboxName
  )
}

const decisionWords: Record<string, string> = {
  approved: "Approved",
  denied: "Denied",
  dismissed: "Dismissed",
}

/**
 * How a decided request is named: "Approved", or "Denied once" for one connection only, "Denied ·
 * tracker", "Denied · Chromium's own", "Allowed · curated list", "Allowed · level Open", or the
 * reviewer's decision.
 */
export function approvalDecision(a: Approval): string {
  if (a.kind === "browser.show")
    return a.status === "approved" ? "Took the wheel" : "Seen"
  if (a.network?.tracker) return "Denied · tracker"
  if (a.reverted) return "Reverted · reviewer"
  switch (a.network?.decided) {
    case "open":
      return "Allowed · level Open"
    case "curated":
      return "Allowed · curated list"
    case "reviewer":
      return `${a.status === "denied" ? "Denied" : "Allowed"} · reviewer`
    case "chromium":
      return "Denied · Chromium's own"
  }
  const word = decisionWords[a.status] ?? a.status
  return a.once ? `${word} once` : word
}
