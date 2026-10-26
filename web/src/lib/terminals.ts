import type { Terminal } from "@/lib/api/client"

/** The keys that work the terminal's sessions while it has focus. Chrome keeps Ctrl+Shift+T,
 * Ctrl+Shift+W and Ctrl+PgUp/PgDn for its own tabs, so they go with Alt+Shift. */
export const sessionKeys = {
  open: "Alt+Shift+T",
  close: "Alt+Shift+W",
  previous: "Alt+Shift+[",
  next: "Alt+Shift+]",
}

/** Sessions in the order they were made; tmux counts in seconds, so main goes first. */
export function sortTerminals(list: Terminal[]): Terminal[] {
  return [...list].sort(
    (a, b) =>
      a.created - b.created ||
      Number(b.name === "main") - Number(a.name === "main") ||
      a.name.localeCompare(b.name, undefined, { numeric: true })
  )
}

/** The first word of a command line: npm for "npm run dev". */
export function programName(command: string): string {
  return command.split(" ")[0]
}

/** Whether what runs in a session would end with it: a program other than its shell. */
export function runsProgram(t: Terminal | undefined): boolean {
  return !!t?.program
}

/** How long ago a Unix time was, for a tooltip: just now, 12 min ago, 3 h ago, 2 days ago. */
export function startedAgo(unix: number, now = Date.now()): string {
  const minutes = Math.floor((now / 1000 - unix) / 60)
  if (minutes < 1) return "just now"
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} h ago`
  const days = Math.floor(hours / 24)
  return days === 1 ? "a day ago" : `${days} days ago`
}

/** What a session's tooltip says it runs: the whole command and when it started, or the shell. */
export function terminalCommand(t: Terminal): string {
  const command = t.foreground?.length ? t.foreground.join(" ") : t.program
  if (!command) return t.command ? `${t.command} at its prompt` : t.name
  return t.started ? `${command} · started ${startedAgo(t.started)}` : command
}
