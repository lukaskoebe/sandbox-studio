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
}

export const authLabels: Record<Auth, string> = {
  agent: "My SSH agent",
  key: "A Studio key",
}

export const hasAPI = (kind: Kind) =>
  kind === "forgejo" || kind === "github" || kind === "gitlab"
