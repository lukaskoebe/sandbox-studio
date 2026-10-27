import type { ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { GlobeIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { Switch } from "@/components/ui/switch"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"

/**
 * The teammate's browser switches: stealth mode (docs/browser.md#stealth-mode), for sites
 * that turn automated browsers away, and whether its agents hear which pages the browser was
 * on while the user drove it.
 */
export function BrowserSettings({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const queryClient = useQueryClient()
  const refresh = () =>
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/teammates"],
    })
  const stealth = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{teammate}/browser/stealth",
    {
      onSuccess: (_, vars) => {
        refresh()
        toast.success(vars.body.on ? "Stealth mode on" : "Stealth mode off", {
          description:
            "The browser restarts with its next step and keeps its logins.",
        })
      },
      onError: (err) =>
        toast.error("Could not change stealth mode", {
          description: errorMessage(err),
        }),
    }
  )
  const share = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{teammate}/browser/share-pages",
    {
      onSuccess: refresh,
      onError: (err) =>
        toast.error("Could not change what agents hear", {
          description: errorMessage(err),
        }),
    }
  )
  const path = { env, teammate: teammate.id }
  return (
    <section className="space-y-3">
      <SectionHeader title="Browser" icon={GlobeIcon} area="browser" />
      <Toggle
        label="Stealth mode"
        checked={teammate.browserStealth}
        disabled={stealth.isPending}
        onChange={(on) => stealth.mutate({ params: { path }, body: { on } })}
      >
        For sites that turn automated browsers away. The browser presents a
        desktop Chrome: its user agent and screen, without the automation flag,
        and the agents' pointer moves to a click the way a hand does, which
        makes clicks a little slower. It does not solve CAPTCHAs, and sites that
        look deeper can still tell.
      </Toggle>
      <Toggle
        label="Tell agents where you browsed"
        checked={teammate.browserSharePages}
        disabled={share.isPending}
        onChange={(on) => share.mutate({ params: { path }, body: { on } })}
      >
        While you drive the browser, agents can't use or see it. When you hand
        it back they hear how long you drove, the downloads you sent them and
        the page it is on now. With this on, they also hear the pages it was on
        while you drove. What you click or type is never passed on.
      </Toggle>
    </section>
  )
}

function Toggle({
  label,
  checked,
  disabled,
  onChange,
  children,
}: {
  label: string
  checked: boolean
  disabled: boolean
  onChange: (on: boolean) => void
  children: ReactNode
}) {
  return (
    <div className="space-y-1.5">
      <label className="flex items-center gap-2 text-xs">
        <Switch
          checked={checked}
          disabled={disabled}
          onCheckedChange={onChange}
        />
        {label}
      </label>
      <p className="text-xs text-muted-foreground">{children}</p>
    </div>
  )
}
