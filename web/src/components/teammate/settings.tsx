import { useNavigate } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { TrashIcon } from "@phosphor-icons/react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { BackToSetup } from "@/components/back-to-setup"
import { BrowserSettings } from "@/components/browser-settings"
import { SaveBar } from "@/components/save-bar"
import { DisclosureCard } from "@/components/disclosure-card"
import {
  TeammateLevelPicker,
  securityLevels,
  useEnvironmentLevel,
} from "@/components/security-level"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { TeammateForm } from "@/components/teammate-form"
import { TeammateHeartbeat } from "@/components/teammate-heartbeat"
import { TeammateSecrets } from "@/components/teammate-secrets"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"
import { heartbeatSentence } from "@/lib/tasks"

/**
 * A teammate's settings as cards, each with a one-line summary, that open one at a time: who
 * it is, which agent it runs, the git identity it commits with, its approvals and soul, its
 * check-ins, network level, browser and secrets. The open card is in the URL: ?open=checkins.
 */
export function TeammateSettings({
  env,
  id,
  open,
  onOpen: setOpen,
  fromSetup,
}: {
  env: string
  id: string
  open?: string
  onOpen: (section: string | undefined) => void
  /** Opened from the setup steps, which it links back to. */
  fromSetup?: boolean
}) {
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const card = (section: string, title: string, summary: ReactNode) => ({
    id: `teammate-${section}`,
    title,
    summary,
    open: open === section,
    onOpenChange: (on: boolean) => setOpen(on ? section : undefined),
  })

  const teammate = teammates.data?.find((p) => p.id === id)
  if (!teammate || !providers.data) return null

  return (
    // Closing the sheet leaves the settings as going to another page does.
    <SaveBar leaves={(search) => search.profile !== "settings"}>
      <div className="grid max-w-3xl grid-cols-1 gap-2.5">
        {fromSetup && <BackToSetup step="teammate" />}
        <TeammateForm
          key={teammate.id}
          env={env}
          teammate={teammate}
          providers={providers.data}
          onSaved={() => toast.success("Teammate saved")}
          sections={{ open, onOpen: setOpen }}
        />
        <DisclosureCard
          {...card(
            "checkins",
            "Check-ins",
            teammate.heartbeat ? heartbeatSentence(teammate.heartbeat) : "Off"
          )}
        >
          <TeammateHeartbeat env={env} teammate={teammate} />
        </DisclosureCard>
        <DisclosureCard
          {...card(
            "network",
            "Network",
            <LevelSummary env={env} teammate={teammate} />
          )}
        >
          <p className="text-xs text-muted-foreground">
            The security level of this teammate's sandboxes: what happens when
            one connects to a host that no rule covers.
          </p>
          <TeammateLevelPicker env={env} teammate={teammate} />
        </DisclosureCard>
        <DisclosureCard
          {...card(
            "browser",
            "Browser",
            teammate.browserStealth ? "Stealth mode on" : "Stealth mode off"
          )}
        >
          <BrowserSettings env={env} teammate={teammate} />
        </DisclosureCard>
        <DisclosureCard
          {...card(
            "secrets",
            "Secrets",
            "Its own, its sandboxes' and the environment's"
          )}
        >
          <TeammateSecrets env={env} teammate={teammate} />
        </DisclosureCard>
        <DisclosureCard
          {...card(
            "delete",
            `Delete ${teammate.name}`,
            "Its sandboxes must be deleted first"
          )}
        >
          <p className="text-xs text-muted-foreground">
            Network rules scoped to it go with it.
          </p>
          <div>
            <DeleteTeammate env={env} teammate={teammate} />
          </div>
        </DisclosureCard>
      </div>
    </SaveBar>
  )
}

/** The teammate's security level, or the environment's it follows. */
export function LevelSummary({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const environment = useEnvironmentLevel(env)
  const own = securityLevels.find((l) => l.value === teammate.securityLevel)
  const inherited = securityLevels.find((l) => l.value === environment.level)
  if (own) return <>{own.label}</>
  return (
    <>
      {inherited
        ? `${inherited.label}, the environment's`
        : "The environment's level"}
    </>
  )
}

function DeleteTeammate({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/teammates/{id}",
    {
      onSuccess: () => navigate({ to: "/e/$env/teammates", params: { env } }),
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/teammates"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/rules"],
        })
      },
      onError: (err) =>
        toast.error("Could not delete the teammate", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog>
      <AlertDialogTrigger render={<Button variant="destructive" size="sm" />}>
        <TrashIcon />
        Delete teammate
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {teammate.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            Network rules scoped to {teammate.name} are deleted too. Sandboxes
            it owns must be deleted first.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: teammate.id } } })
            }
          >
            {remove.isPending && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
