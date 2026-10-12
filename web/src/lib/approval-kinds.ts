import {
  BrainIcon,
  ChatCircleDotsIcon,
  CursorClickIcon,
  GitBranchIcon,
  GitPullRequestIcon,
  GlobeIcon,
  KeyIcon,
  ScalesIcon,
  ShieldIcon,
  type Icon,
} from "@phosphor-icons/react"
import type { Approval } from "@/lib/api/client"

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
  /** Whether the summary is a host or similar and reads best in monospace. */
  mono?: boolean
  /** The environment page that lists approvals of this kind. */
  page:
    | "/e/$env"
    | "/e/$env/network"
    | "/e/$env/settings/git"
    | "/e/$env/memory"
    | "/e/$env/teammates/$id/browser"
    | "/e/$env/chats"
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
    mono: true,
    page: "/e/$env/network",
  },
  "git.push": {
    title: "Git push",
    summary: gitSummary,
    icon: GitBranchIcon,
    page: "/e/$env/settings/git",
  },
  "git.pr": {
    title: "Pull request",
    summary: gitSummary,
    icon: GitPullRequestIcon,
    page: "/e/$env/settings/git",
  },
  "memory.share": {
    title: "Memory share",
    summary: (a) =>
      `${a.memoryShare?.teammateName ?? "A teammate"} wants to share a fact with every teammate`,
    icon: BrainIcon,
    page: "/e/$env/memory",
  },
  "memory.conflict": {
    title: "Memory conflict",
    summary: (a) =>
      conflictVerdicts[a.memoryConflict?.verdict ?? ""] ??
      "Two memory facts disagree",
    icon: ScalesIcon,
    page: "/e/$env/memory",
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
    page: "/e/$env/teammates/$id/browser",
  },
  "chat.permission": {
    title: "Agent permission",
    summary: (a) => a.chatPermission?.title ?? a.subject,
    icon: ChatCircleDotsIcon,
    page: "/e/$env/chats",
  },
}

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
  return a.browserAction?.teammateId ?? a.browserCredential?.teammateId
}

export function approvalSource(a: Approval): string | undefined {
  return (
    a.network?.sandboxName ??
    a.git?.review?.sandbox ??
    a.memoryShare?.teammateName ??
    a.browserAction?.sandboxName ??
    a.browserCredential?.sandboxName ??
    a.chatPermission?.sandboxName
  )
}
