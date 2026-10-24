/**
 * The computer screen's state in the URL (docs/design/teammate-computer.md): which tab it shows,
 * which terminal session, which push review, and whether it is folded or takes the page. The
 * teammate's page and a plain sandbox's page read the same params.
 */
export const screenTabs = ["terminal", "browser", "preview", "changes"] as const
export type ScreenTab = (typeof screenTabs)[number]

export type ScreenSearch = {
  tab?: ScreenTab
  term?: string
  review?: string
  fold?: boolean
  /** The screen takes the page, and the conversation is a strip beside it. */
  full?: boolean
}

const text = (value: unknown) =>
  typeof value === "string" && value !== "" ? value : undefined

/**
 * The screen's params from a URL. ?terminal= and ?session= are a terminal's names in older links,
 * ?chat= opened a chat tab and ?panel= a side panel; they land on the matching tab.
 */
export function screenSearch(s: Record<string, unknown>): ScreenSearch {
  const term = text(s.term) ?? text(s.terminal) ?? text(s.session)
  const review = text(s.review)
  const panel = text(s.panel)
  const tab =
    screenTabs.find((t) => t === s.tab) ??
    (review || panel === "files"
      ? "changes"
      : panel === "browser"
        ? "browser"
        : term
          ? "terminal"
          : undefined)
  return {
    ...(tab ? { tab } : {}),
    ...(term ? { term } : {}),
    ...(review ? { review } : {}),
    ...(s.fold === true || s.fold === "1" || s.fold === 1
      ? { fold: true }
      : {}),
    ...(s.full === true || s.full === "1" || s.full === 1
      ? { full: true }
      : {}),
  }
}

/** The teammate's profile: the card, or one of its sheets. */
export const profileViews = ["card", "tasks", "memory", "settings"] as const
export type ProfileView = (typeof profileViews)[number]

/**
 * The teammate page's state: the thread open in the conversation, the computer on the screen
 * when it is not the thread's, the profile, the open settings card, and the screen's own.
 */
export type TeammateSearch = ScreenSearch & {
  thread?: string
  computer?: string
  profile?: ProfileView
  open?: string
  from?: "setup"
}

export function teammateSearch(s: Record<string, unknown>): TeammateSearch {
  const thread = text(s.thread) ?? text(s.chat)
  const computer = text(s.computer)
  const profile = profileViews.find((v) => v === s.profile)
  const open = text(s.open)
  return {
    ...screenSearch(s),
    ...(thread ? { thread } : {}),
    ...(computer ? { computer } : {}),
    ...(profile ? { profile } : {}),
    ...(open ? { open } : {}),
    ...(s.from === "setup" ? { from: "setup" as const } : {}),
  }
}
