import { Badge } from "@/components/ui/badge"
import type { Provider, Teammate } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

/**
 * Whether the teammate's agent sessions can start: its harness is in the base image, so it
 * comes down to the provider being able to run that harness with a key.
 */
export function Readiness({
  teammate,
  provider,
  problemsOnly,
}: {
  teammate: Teammate
  provider?: Provider
  /** Shows nothing when the teammate is ready, as a header that only flags what blocks it. */
  problemsOnly?: boolean
}) {
  if (!provider) {
    // A teammate whose provider was not copied along has none; before the providers load it has one.
    return teammate.providerId ? null : (
      <Badge variant="outline">No provider</Badge>
    )
  }
  if (provider.state === "key_required") {
    return (
      <Badge
        variant="outline"
        title={`${provider.name} needs its API key, added in Settings → AI providers`}
      >
        Key required
      </Badge>
    )
  }
  if (provider.state === "login_required" || provider.state === "expired") {
    return (
      <Badge
        variant="outline"
        title={`${provider.name} needs a sign-in, in Settings → AI providers`}
      >
        {provider.state === "expired" ? "Sign-in expired" : "Sign-in needed"}
      </Badge>
    )
  }
  if (!(provider.harnesses ?? []).includes(teammate.harness)) {
    return (
      <Badge variant="outline">
        {provider.name} can't run {harnessLabels[teammate.harness]}
      </Badge>
    )
  }
  if (problemsOnly) return null
  return (
    <Badge
      variant="secondary"
      title={`${harnessLabels[teammate.harness]} reads the key from ${provider.envVar}`}
    >
      Ready
    </Badge>
  )
}
