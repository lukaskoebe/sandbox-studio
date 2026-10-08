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
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"

const namePattern = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/

export function CreateSandboxDialog({
  env,
  open,
  onOpenChange,
}: {
  env: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [name, setName] = useState("")
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const create = $api.useMutation("post", "/api/environments/{env}/sandboxes", {
    onSuccess: (sb) => {
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes"],
      })
      onOpenChange(false)
      setName("")
      navigate({ to: "/e/$env/sandboxes/$id", params: { env, id: sb.id } })
    },
    onError: (err) =>
      toast.error("Could not create the sandbox", {
        description: errorMessage(err),
      }),
  })
  const invalid = name !== "" && !namePattern.test(name)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault()
            if (!name || invalid) return
            create.mutate({ params: { path: { env } }, body: { name } })
          }}
        >
          <DialogHeader>
            <DialogTitle>New sandbox</DialogTitle>
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
              <FieldDescription>Also the sandbox's hostname.</FieldDescription>
            )}
          </Field>
          <DialogFooter>
            <Button
              type="submit"
              disabled={!name || invalid || create.isPending}
            >
              {create.isPending && <Spinner />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
