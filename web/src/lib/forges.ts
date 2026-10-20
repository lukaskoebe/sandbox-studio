import type { Forge } from "@/lib/api/client"

/** The host sandboxes address their forges by; Studio answers for it inside each sandbox. */
export const remoteHost = "git.studio.internal"

export type Kind = Forge["kind"]
export type Auth = "agent" | "key"

export const kindLabels: Record<Kind, string> = {
  forgejo: "Forgejo",
  github: "GitHub",
  gitlab: "GitLab",
  https: "Other HTTPS",
  ssh: "Other SSH",
  local: "Folder on this computer",
}

export const authLabels: Record<Auth, string> = {
  agent: "My SSH agent",
  key: "A Studio key",
}

export const hasAPI = (kind: Kind) =>
  kind === "forgejo" || kind === "github" || kind === "gitlab"

/** Whether Studio can list a forge's repositories: through its API, or in its folder. */
export const canListRepositories = (kind: Kind) =>
  hasAPI(kind) || kind === "local"

/**
 * The name a forge gets in Studio when the user doesn't pick one, as the server names a forge
 * from a pasted repository URL: the host's main label (github.com and git.example.org become
 * github and example), local for a folder, and kind's own name until there is a host.
 */
export function suggestedName(kind: Kind, baseUrl: string) {
  if (kind === "local") return "local"
  const v = baseUrl.trim()
  const host =
    kind === "ssh"
      ? /^(?:ssh:\/\/)?(?:[^@/\s]+@)?([^:/\s]+)/.exec(v)?.[1]
      : /^https:\/\/(?:[^@/\s]+@)?([^:/\s]+)/.exec(v)?.[1]
  if (!host)
    return {
      forgejo: "codeberg",
      github: "github",
      gitlab: "gitlab",
      https: "git",
      ssh: "git",
    }[kind]
  if (/^[\d.]+$/.test(host) || host.includes(":")) return "git"
  const labels = host.toLowerCase().split(".")
  const n = labels.length
  const label =
    n >= 3 && labels[n - 2].length <= 2
      ? labels[n - 3]
      : n >= 2
        ? labels[n - 2]
        : labels[0]
  const name = label
    .replace(/[^a-z0-9-]/g, "")
    .replace(/^-+/, "")
    .slice(0, 24)
    .replace(/-+$/, "")
  return name || "git"
}

/** name, or name-2, name-3 and so on if a forge in taken has it. */
export function freeName(name: string, taken: { name: string }[]) {
  const used = new Set(taken.map((f) => f.name))
  let candidate = name
  for (let i = 2; used.has(candidate); i++) candidate = `${name}-${i}`
  return candidate
}
