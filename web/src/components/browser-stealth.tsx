import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { GlobeIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { Switch } from "@/components/ui/switch"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"

/**
 * The teammate's switch for stealth mode (docs/browser.md#stealth-mode): its browser looks
 * like a desktop Chrome to the sites it visits, for sites that turn automated browsers away.
 */
export function BrowserStealth({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const queryClient = useQueryClient()
  const set = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{teammate}/browser/stealth",
    {
      onSuccess: (_, vars) => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/teammates"],
        })
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
  return (
    <section className="space-y-3">
      <SectionHeader title="Browser" icon={GlobeIcon} area="browser" />
      <label className="flex items-center gap-2 text-sm">
        <Switch
          checked={teammate.browserStealth}
          disabled={set.isPending}
          onCheckedChange={(on) =>
            set.mutate({
              params: { path: { env, teammate: teammate.id } },
              body: { on },
            })
          }
        />
        Stealth mode
      </label>
      <p className="text-xs text-muted-foreground">
        For sites that turn automated browsers away. The browser presents a
        desktop Chrome: its user agent and screen, without the automation flag,
        and the agents' pointer moves to a click the way a hand does, which
        makes clicks a little slower. It does not solve CAPTCHAs, and sites that
        look deeper can still tell.
      </p>
    </section>
  )
}
