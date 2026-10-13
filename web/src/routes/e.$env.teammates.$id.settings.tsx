import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { ShieldCheckIcon, TrashIcon } from "@phosphor-icons/react"
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
import { SectionHeader } from "@/components/section-header"
import { TeammateLevelSelect } from "@/components/security-level"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { TeammateForm } from "@/components/teammate-form"
import { TeammateHeartbeat } from "@/components/teammate-heartbeat"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/teammates/$id/settings")({
  component: TeammateSettings,
})

function TeammateSettings() {
  const { env, id } = Route.useParams()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const teammate = teammates.data?.find((p) => p.id === id)
  if (!teammate || !providers.data) return null

  return (
    <div className="max-w-2xl space-y-8">
      <TeammateForm
        key={teammate.id}
        env={env}
        teammate={teammate}
        providers={providers.data}
        onSaved={() => toast.success("Teammate saved")}
      />
      <div className="border-t pt-6">
        <TeammateHeartbeat env={env} teammate={teammate} />
      </div>
      <section className="space-y-3 border-t pt-6">
        <SectionHeader
          title="Network security level"
          icon={ShieldCheckIcon}
          area="network"
        />
        <Field>
          <FieldLabel htmlFor="teammate-security-level">Level</FieldLabel>
          <TeammateLevelSelect env={env} teammate={teammate} />
          <FieldDescription>
            The level of this teammate's sandboxes. Environment default follows
            the environment's level, set under Network.
          </FieldDescription>
        </Field>
      </section>
      <section className="space-y-3 border-t pt-6">
        <SectionHeader title={`Delete ${teammate.name}`} icon={TrashIcon} />
        <p className="text-xs text-muted-foreground">
          Its sandboxes must be deleted first. Network rules scoped to it go
          with it.
        </p>
        <DeleteTeammate env={env} teammate={teammate} />
      </section>
    </div>
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
