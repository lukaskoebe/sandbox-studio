import { Link } from "@tanstack/react-router"
import { Badge } from "@/components/ui/badge"
import type { Provider, Teammate } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

/**
 * Whether the teammate's agent sessions can start: its harness is in the base image, so it
 * comes down to the provider being able to run that harness with a key. A provider that needs
 * a key or a sign-in links to it, in Settings → AI providers.
 */
export function Readiness({
  env,
  teammate,
  provider,
  problemsOnly,
}: {
  env: string
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
  if (
    provider.state === "key_required" ||
    provider.state === "login_required" ||
    provider.state === "expired"
  ) {
    const key = provider.state === "key_required"
    return (
      <Badge
        variant="outline"
        title={
          key
            ? `Add ${provider.name}'s API key, in Settings → AI providers`
            : `Sign in to ${provider.name}, in Settings → AI providers`
        }
        render={
          <Link
            to="/e/$env/settings/providers"
            params={{ env }}
            search={{ signIn: provider.id }}
          />
        }
      >
        {key
          ? "Key required"
          : provider.state === "expired"
            ? "Sign-in expired"
            : "Sign-in needed"}
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
