import { createFileRoute, useNavigate } from "@tanstack/react-router"
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
import { BackToSetup, fromSetup } from "@/components/back-to-setup"
import { BrowserSettings } from "@/components/browser-settings"
import { SaveBar } from "@/components/save-bar"
import { DisclosureCard } from "@/components/disclosure-card"
import {
  TeammateLevelSelect,
  securityLevels,
  useEnvironmentLevel,
} from "@/components/security-level"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { TeammateForm } from "@/components/teammate-form"
import { TeammateHeartbeat } from "@/components/teammate-heartbeat"
import { TeammateSecrets } from "@/components/teammate-secrets"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"
import { heartbeatSentence } from "@/lib/tasks"

export const Route = createFileRoute("/e/$env/teammates/$id/settings")({
  // The section that is open, so a link can open one: ?open=checkins. ?from=setup links back.
  validateSearch: (
    s: Record<string, unknown>
  ): { open?: string; from?: "setup" } => ({
    ...(typeof s.open === "string" && s.open !== "" ? { open: s.open } : {}),
    ...fromSetup(s),
  }),
  component: TeammateSettings,
})

/**
 * A teammate's settings as cards, each with a one-line summary, that open one at a time: who
 * it is, which agent it runs, the git identity it commits with, its approvals and soul, its
 * check-ins, network level, browser and secrets.
 */

function TeammateSettings() {
  const { env, id } = Route.useParams()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const { open, from } = Route.useSearch()
  const navigate = Route.useNavigate()
  const setOpen = (section: string | undefined) =>
    navigate({
      search: (prev) => ({ from: prev.from, open: section }),
      replace: true,
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
    <SaveBar>
      <div className="grid max-w-3xl grid-cols-1 gap-2.5">
        {from === "setup" && <BackToSetup step="teammate" />}
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
          <Field>
            <FieldLabel htmlFor="teammate-security-level">
              Security level
            </FieldLabel>
            <TeammateLevelSelect env={env} teammate={teammate} />
            <FieldDescription>
              The level of this teammate's sandboxes. Environment default
              follows the environment's level, set in Settings → Network.
            </FieldDescription>
          </Field>
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
function LevelSummary({ env, teammate }: { env: string; teammate: Teammate }) {
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
