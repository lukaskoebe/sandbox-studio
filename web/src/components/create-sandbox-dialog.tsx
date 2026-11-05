import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { TeammateField } from "@/components/teammate-field"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

const namePattern = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/
const emptyStart = "empty"

export function CreateSandboxDialog({
  env,
  teammate = "",
  open,
  onOpenChange,
}: {
  env: string
  /** The teammate the new sandbox is for, until the user picks another. */
  teammate?: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [name, setName] = useState("")
  const [teammateId, setTeammateId] = useState(teammate)
  const [startFrom, setStartFrom] = useState(emptyStart)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const bases = (sandboxes.data ?? []).filter((sb) => sb.forkDefaults)
  const base = bases.find((sb) => sb.id === startFrom)
  const created = {
    onSuccess: (sb: Sandbox) => {
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes"],
      })
      onOpenChange(false)
      setName("")
      setTeammateId("")
      setStartFrom(emptyStart)
      navigate({ to: "/e/$env/sandboxes/$id", params: { env, id: sb.id } })
    },
    onError: (err: unknown) =>
      toast.error("Could not create the computer", {
        description: errorMessage(err),
      }),
  }
  const create = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes",
    created
  )
  const fork = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/fork",
    created
  )
  const pending = create.isPending || fork.isPending
  const invalid = name !== "" && !namePattern.test(name)
  const blocked = base && baseBlocked(base)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault()
            if (!name || invalid || blocked) return
            if (base) {
              // No options: a base's fork defaults say what it takes.
              fork.mutate({
                params: { path: { env, id: base.id } },
                body: { name },
              })
              return
            }
            create.mutate({
              params: { path: { env } },
              body: { name, teammateId: teammateId || undefined },
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>New computer</DialogTitle>
            <DialogDescription>
              A Linux VM with Docker, a persistent /workspace and 2 CPUs, 4 GiB
              memory.
            </DialogDescription>
          </DialogHeader>
          <Field data-invalid={invalid || undefined}>
            <FieldLabel htmlFor="sandbox-name">Name</FieldLabel>
            <Input
              id="sandbox-name"
              autoFocus
              autoComplete="off"
              placeholder="my-project"
              value={name}
              aria-invalid={invalid}
              onChange={(e) => setName(e.target.value.toLowerCase())}
            />
            {invalid ? (
              <FieldError>
                Lowercase letters, digits and dashes; up to 40 characters.
              </FieldError>
            ) : (
              <FieldDescription>Also the computer's hostname.</FieldDescription>
            )}
          </Field>
          {bases.length > 0 && (
            <Field>
              <FieldLabel htmlFor="sandbox-start-from">Start from</FieldLabel>
              <Select
                value={base ? base.id : emptyStart}
                onValueChange={(v) => setStartFrom(v ?? emptyStart)}
                items={{
                  [emptyStart]: "An empty computer",
                  ...Object.fromEntries(bases.map((sb) => [sb.id, sb.name])),
                }}
              >
                <SelectTrigger id="sandbox-start-from" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={emptyStart}>An empty computer</SelectItem>
                  {bases.map((sb) => (
                    <SelectItem key={sb.id} value={sb.id}>
                      {sb.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {base &&
                (blocked ? (
                  <FieldError>{blocked}</FieldError>
                ) : (
                  <FieldDescription>{baseCopies(base)}</FieldDescription>
                ))}
            </Field>
          )}
          {!base && (
            <TeammateField
              env={env}
              id="sandbox-teammate"
              value={teammateId}
              onChange={setTeammateId}
            />
          )}
          <DialogFooter>
            <Button
              type="submit"
              disabled={!name || invalid || !!blocked || pending}
            >
              {pending && <Spinner />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Why a base can't be copied right now; nothing when it can. */
function baseBlocked(base: Sandbox) {
  if (base.status !== "running" && base.status !== "stopped")
    return `Only a running or stopped computer can be copied, and ${base.name} is ${base.status}.`
  if (base.forkDefaults?.docker && base.status === "running")
    return `Stop ${base.name} to copy its Docker images.`
}

/** What starting from a base copies, and what comes with it. */
function baseCopies(base: Sandbox) {
  const copied = ["/workspace", "the home"]
  if (base.forkDefaults?.tools) copied.push("mise tools")
  if (base.forkDefaults?.docker) copied.push("Docker's data")
  const last = copied.pop()
  const script = base.forkDefaults?.script
    ? " Then it runs ~/.config/studio/on-fork.sh in a terminal."
    : ""
  return `Copies ${copied.join(", ")} and ${last}, with ${base.name}'s template, resources and teammate.${script}`
}
