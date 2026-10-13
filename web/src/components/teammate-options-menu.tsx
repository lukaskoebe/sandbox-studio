import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { CopyIcon, DotsThreeIcon } from "@phosphor-icons/react"
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  fetchClient,
  type Teammate,
} from "@/lib/api/client"

/** A teammate's options: copying it to another environment. */
export function TeammateOptionsMenu({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const [copying, setCopying] = useState(false)
  return (
    <>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Teammate options"
              title="Teammate options"
            />
          }
        >
          <DotsThreeIcon weight="bold" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
          <DropdownMenuGroup>
            <DropdownMenuItem onClick={() => setCopying(true)}>
              <CopyIcon />
              Copy to environment…
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      <CopyTeammateDialog
        env={env}
        teammate={teammate}
        open={copying}
        onOpenChange={setCopying}
      />
    </>
  )
}

/**
 * Copies a teammate into another environment. The copy keeps its settings, not its sandboxes,
 * chats, memory, secrets or keys; it takes the target's provider of the same kind if there is one.
 */
function CopyTeammateDialog({
  env,
  teammate,
  open,
  onOpenChange,
}: {
  env: string
  teammate: Teammate
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const navigate = useNavigate()
  const environments = $api.useQuery("get", "/api/environments")
  const others = (environments.data ?? []).filter((e) => e.id !== env)
  const [target, setTarget] = useState("")
  const [copying, setCopying] = useState(false)

  const copy = async () => {
    if (!target || copying) return
    setCopying(true)
    try {
      const result = await fetchClient.POST(
        "/api/environments/{env}/teammates/{id}/copy",
        {
          params: { path: { env, id: teammate.id } },
          body: { targetEnvironmentId: target },
        }
      )
      if (result.error) {
        toast.error("Could not copy the teammate", {
          description: errorMessage(result.error),
        })
        return
      }
      const copied = result.data
      const targetName = others.find((e) => e.id === target)?.name ?? "it"
      onOpenChange(false)
      toast.success(`Copied ${teammate.name} to ${targetName}`, {
        description: copied.providerId
          ? undefined
          : "It has no provider there yet: choose one in its settings.",
        action: {
          label: "Open",
          onClick: () =>
            navigate({
              to: "/e/$env/teammates/$id",
              params: { env: target, id: copied.id },
            }),
        },
      })
    } finally {
      setCopying(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Copy {teammate.name} to another environment</DialogTitle>
          <DialogDescription>
            The copy keeps the name (with a number if it is taken), role,
            avatar, soul, harness, model, git identity and approval setting.
            Sandboxes, chats, memory, secrets and keys stay here.
          </DialogDescription>
        </DialogHeader>
        {others.length ? (
          <Field>
            <FieldLabel htmlFor="copy-teammate-target">Environment</FieldLabel>
            <Select
              value={target}
              onValueChange={(v) => setTarget(v ?? "")}
              items={Object.fromEntries(others.map((e) => [e.id, e.name]))}
            >
              <SelectTrigger id="copy-teammate-target" className="w-full">
                <SelectValue placeholder="Choose an environment" />
              </SelectTrigger>
              <SelectContent>
                {others.map((e) => (
                  <SelectItem key={e.id} value={e.id}>
                    {e.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldDescription>
              It uses that environment's provider of the same kind, if it has
              one.
            </FieldDescription>
          </Field>
        ) : (
          <p className="text-xs text-muted-foreground">
            There is no other environment. Create one from the environment
            switcher first.
          </p>
        )}
        <DialogFooter>
          <Button type="button" disabled={!target || copying} onClick={copy}>
            {copying && <Spinner />}
            Copy teammate
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
