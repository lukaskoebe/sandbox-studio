import type { Secret } from "@/lib/api/client"

/**
 * The scope a secret applies in: the whole environment, one teammate's sandboxes or one sandbox,
 * or one teammate's browser, for a browser login, which no sandbox sees.
 */
export type SecretScope =
  | { kind: "environment" }
  | { kind: "teammate"; id: string }
  | { kind: "sandbox"; id: string }
  | { kind: "browser"; id: string }

export function scopeOf(s: Secret): SecretScope {
  if (s.browserOnly && s.teammateId)
    return { kind: "browser", id: s.teammateId }
  if (s.sandboxId) return { kind: "sandbox", id: s.sandboxId }
  if (s.teammateId) return { kind: "teammate", id: s.teammateId }
  return { kind: "environment" }
}

/** The value the secret dialog's scope select uses for a scope. */
export function scopeValue(scope: SecretScope) {
  if (scope.kind === "teammate") return `teammate:${scope.id}`
  if (scope.kind === "sandbox") return scope.id
  if (scope.kind === "browser") return `browser:${scope.id}`
  return "environment"
}

export type SecretGroup = {
  key: string
  scope: SecretScope
  /** The teammate's or sandbox's name; empty for the environment. */
  name: string
  secrets: Secret[]
}

const rank = { environment: 0, teammate: 1, sandbox: 2, browser: 3 } as const

/**
 * Secrets by scope: the environment's first, then each teammate's, then each sandbox's, the
 * owners by name and the secrets by name within a group. Only scopes that hold a secret appear.
 */
export function groupByScope(
  list: Secret[],
  teammates: Map<string, string>,
  sandboxes: Map<string, string>
): SecretGroup[] {
  const groups = new Map<string, SecretGroup>()
  for (const s of list) {
    const scope = scopeOf(s)
    const key = scopeValue(scope)
    let group = groups.get(key)
    if (!group) {
      const name =
        scope.kind === "teammate" || scope.kind === "browser"
          ? (teammates.get(scope.id) ?? "")
          : scope.kind === "sandbox"
            ? (sandboxes.get(scope.id) ?? "")
            : ""
      group = { key, scope, name, secrets: [] }
      groups.set(key, group)
    }
    group.secrets.push(s)
  }
  const out = [...groups.values()]
  for (const g of out) g.secrets.sort((a, b) => a.name.localeCompare(b.name))
  return out.sort(
    (a, b) =>
      rank[a.scope.kind] - rank[b.scope.kind] ||
      a.name.localeCompare(b.name) ||
      a.key.localeCompare(b.key)
  )
}

/** One secret as a teammate's sandboxes see it, with how it relates to the others of its name. */
export type TeammateSecret = {
  secret: Secret
  scope: SecretScope
  /** An environment secret: the teammate inherits it rather than owning it. */
  inherited: boolean
  /** A teammate secret of the same name replaces this environment secret in its sandboxes. */
  overridden: boolean
  /** The broader secret of the same name this teammate or sandbox secret replaces, if any. */
  overrides?: "environment" | "teammate"
}

/**
 * The secrets a teammate's sandboxes see: its own, its sandboxes', and the environment's it
 * inherits. Studio-only secrets never reach a sandbox, so they are left out. The teammate's
 * come first, then its sandboxes', then the inherited ones, each by name.
 */
export function teammateSecrets(
  list: Secret[],
  teammateId: string,
  sandboxIds: Set<string>
): TeammateSecret[] {
  const own = list.filter(
    (s) => s.teammateId === teammateId && !s.sandboxId && !s.browserOnly
  )
  const sandbox = list.filter((s) => s.sandboxId && sandboxIds.has(s.sandboxId))
  const env = list.filter((s) => !s.teammateId && !s.sandboxId && !s.studioOnly)
  const ownNames = new Set(own.map((s) => s.name))
  const envNames = new Set(env.map((s) => s.name))
  const byName = (a: Secret, b: Secret) => a.name.localeCompare(b.name)
  return [
    ...own.sort(byName).map((s) => ({
      secret: s,
      scope: scopeOf(s),
      inherited: false,
      overridden: false,
      overrides: envNames.has(s.name) ? ("environment" as const) : undefined,
    })),
    ...sandbox.sort(byName).map((s) => ({
      secret: s,
      scope: scopeOf(s),
      inherited: false,
      overridden: false,
      overrides: ownNames.has(s.name)
        ? ("teammate" as const)
        : envNames.has(s.name)
          ? ("environment" as const)
          : undefined,
    })),
    ...env.sort(byName).map((s) => ({
      secret: s,
      scope: scopeOf(s),
      inherited: true,
      overridden: ownNames.has(s.name),
    })),
  ]
}
