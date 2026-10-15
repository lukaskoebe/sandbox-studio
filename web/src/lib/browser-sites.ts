import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

type BrowserPattern = components["schemas"]["BrowserPattern"]

/**
 * What a teammate's agents may do on a site, in plain words. "Ask first" is no rule; "Allowed"
 * and "Blocked" are an allow or deny rule for every action on the whole origin. Narrower rules
 * (one action, one element) stay as they are and show next to it.
 */
export type SitePolicy = "ask" | "allow" | "block"

export const policyWords: Record<SitePolicy, string> = {
  ask: "Ask first",
  allow: "Allowed",
  block: "Blocked",
}

const patternsPath =
  "/api/environments/{env}/teammates/{teammate}/browser/patterns"

/** The page's origin, as browser rules name it; "" for a page without one. */
export function originOf(url?: string) {
  if (!url) return ""
  try {
    const u = new URL(url)
    return u.protocol === "http:" || u.protocol === "https:" ? u.origin : ""
  } catch {
    return ""
  }
}

export function hostOf(url?: string) {
  if (!url) return ""
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

/** A rule for every action on the whole origin: the one the policy picker sets. */
function isSiteWide(p: BrowserPattern, origin: string) {
  return (
    p.origin === origin &&
    p.verdict !== "sensitive" &&
    p.action === "*" &&
    !p.role &&
    (!p.label || p.label === "*")
  )
}

export function sitePolicy(
  patterns: readonly BrowserPattern[],
  origin: string
): SitePolicy {
  const rule = patterns.find((p) => isSiteWide(p, origin))
  return rule ? (rule.verdict === "allow" ? "allow" : "block") : "ask"
}

/** The rules on an origin other than the site-wide one: single actions and hidden fields. */
export function siteRules(patterns: readonly BrowserPattern[], origin: string) {
  const own = patterns.filter((p) => p.origin === origin)
  return {
    actions: own.filter(
      (p) => p.verdict !== "sensitive" && !isSiteWide(p, origin)
    ),
    hidden: own.filter((p) => p.verdict === "sensitive"),
  }
}

/** A hidden field or narrow rule in words: `textbox “Card number”`. */
export function elementWords(p: BrowserPattern) {
  return (
    [p.role, p.label && p.label !== "*" && `“${p.label}”`]
      .filter(Boolean)
      .join(" ") || "any element"
  )
}

export function useBrowserPatterns(env: string, teammate: string) {
  return $api.useQuery("get", patternsPath, {
    params: { path: { env, teammate } },
  })
}

/** Sets a site's policy: replaces its site-wide rule, or removes it for "Ask first". */
export function useSetSitePolicy(env: string, teammate: string) {
  const queryClient = useQueryClient()
  const patterns = useBrowserPatterns(env, teammate)
  return async (origin: string, policy: SitePolicy) => {
    const path = { env, teammate }
    try {
      for (const rule of (patterns.data ?? []).filter((p) =>
        isSiteWide(p, origin)
      )) {
        const { error } = await fetchClient.DELETE(`${patternsPath}/{id}`, {
          params: { path: { ...path, id: rule.id } },
        })
        if (error) throw error
      }
      if (policy !== "ask") {
        const { error } = await fetchClient.POST(patternsPath, {
          params: { path },
          body: {
            origin,
            action: "*",
            verdict: policy === "allow" ? "allow" : "deny",
          },
        })
        if (error) throw error
      }
    } catch (err) {
      toast.error("Could not change the site's rule", {
        description: errorMessage(err),
      })
    } finally {
      await queryClient.invalidateQueries({ queryKey: ["get", patternsPath] })
    }
  }
}
