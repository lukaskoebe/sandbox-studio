import type { Rule } from "@/lib/api/client"

export const ruleScopes = ["environment", "teammate", "sandbox"] as const
export type RuleScope = (typeof ruleScopes)[number]

export const ruleActions = ["allow", "proxy", "caddy", "deny"] as const

/** The Rules tab's filters, kept in the URL; an unset one matches everything. */
export type RuleFilter = {
  /** Text the host must contain. */
  q?: string
  scope?: RuleScope
  action?: (typeof ruleActions)[number]
}

/** Two-label public suffixes, under which a domain has three labels: example.co.uk. */
const twoLabelSuffixes = new Set([
  "ac.uk",
  "co.jp",
  "co.kr",
  "co.nz",
  "co.uk",
  "co.za",
  "com.au",
  "com.br",
  "com.cn",
  "com.hk",
  "com.mx",
  "com.sg",
  "com.tr",
  "com.tw",
  "gov.uk",
  "net.au",
  "org.au",
  "org.uk",
])

/**
 * The registrable domain of a rule's host, by a short list instead of the full public suffix
 * list: the last two labels, or three under a suffix in the list above. A wildcard is
 * dropped, and an IP address stands for itself.
 */
export function registrableDomain(host: string): string {
  const name = host.toLowerCase().replace(/^\*\./, "").replace(/\.$/, "")
  if (/^[\d.]+$/.test(name) || name.includes(":")) return name
  const labels = name.split(".")
  if (labels.length <= 2) return name
  const suffix = labels.slice(-2).join(".")
  return labels.slice(twoLabelSuffixes.has(suffix) ? -3 : -2).join(".")
}

/** The scope a rule applies to: one sandbox, one teammate's sandboxes, or the environment. */
export function ruleScope(rule: Rule): RuleScope {
  if (rule.sandboxId) return "sandbox"
  return rule.teammateId ? "teammate" : "environment"
}

/** The rules that pass every filter. */
export function filterRules(rules: Rule[], filter: RuleFilter): Rule[] {
  const q = filter.q?.trim().toLowerCase() ?? ""
  return rules.filter(
    (r) =>
      r.host.toLowerCase().includes(q) &&
      (!filter.scope || ruleScope(r) === filter.scope) &&
      (!filter.action || r.action === filter.action)
  )
}

/** The rules by registrable domain, domains and hosts in alphabetical order. */
export function groupRules(rules: Rule[]): { domain: string; rules: Rule[] }[] {
  const byDomain = new Map<string, Rule[]>()
  for (const r of rules) {
    const domain = registrableDomain(r.host)
    byDomain.set(domain, [...(byDomain.get(domain) ?? []), r])
  }
  return [...byDomain]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([domain, list]) => ({
      domain,
      rules: list.sort((a, b) => a.host.localeCompare(b.host)),
    }))
}
