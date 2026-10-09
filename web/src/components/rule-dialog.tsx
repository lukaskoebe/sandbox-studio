import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  $api,
  errorMessage,
  errorStatus,
  type Rule,
  type Sandbox,
} from "@/lib/api/client"

export function RuleDialog({
  env,
  rule,
  sandboxes,
  open,
  onOpenChange,
}: {
  env: string
  /** The rule to edit; absent for a new rule. */
  rule?: Rule
  sandboxes: Sandbox[]
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <RuleForm
          key={rule?.id ?? "new"}
          env={env}
          rule={rule}
          sandboxes={sandboxes}
          onDone={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

/** Comma-separated ports; empty means any port, null when one is not a number from 1 to 65535. */
function parsePorts(text: string): number[] | null {
  const ports = text
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean)
    .map(Number)
  return ports.every((p) => Number.isInteger(p) && p >= 1 && p <= 65535)
    ? ports
    : null
}

function RuleForm({
  env,
  rule,
  sandboxes,
  onDone,
}: {
  env: string
  rule?: Rule
  sandboxes: Sandbox[]
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const [host, setHost] = useState(rule?.host ?? "")
  const [ports, setPorts] = useState(rule?.ports?.join(", ") ?? "")
  const [action, setAction] = useState<"allow" | "deny">(
    rule?.action === "deny" ? "deny" : "allow"
  )
  const [scope, setScope] = useState(rule?.sandboxId ?? "environment")
  const [note, setNote] = useState(rule?.note ?? "")
  const [hostError, setHostError] = useState<string>()
  const portList = parsePorts(ports)

  const onSaved = () => {
    queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/approvals"],
    })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/rules"],
    })
    onDone()
  }
  // A 422 explains which pattern or sandbox is wrong, so it stays in the form.
  const onFailed = (err: unknown) => {
    if (errorStatus(err) === 422) setHostError(errorMessage(err))
    else
      toast.error("Could not save the rule", { description: errorMessage(err) })
  }
  const create = $api.useMutation("post", "/api/environments/{env}/rules", {
    onSuccess: onSaved,
    onError: onFailed,
  })
  const update = $api.useMutation("put", "/api/environments/{env}/rules/{id}", {
    onSuccess: onSaved,
    onError: onFailed,
  })
  const saving = create.isPending || update.isPending
  const valid = host.trim() !== "" && portList !== null

  const submit = () => {
    if (portList === null || !host.trim() || saving) return
    setHostError(undefined)
    const body = {
      host: host.trim(),
      ports: portList,
      action,
      sandboxId: scope === "environment" ? undefined : scope,
      note: note.trim() || undefined,
    }
    if (rule) update.mutate({ params: { path: { env, id: rule.id } }, body })
    else create.mutate({ params: { path: { env } }, body })
  }

  return (
    <form
      className="contents"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <DialogHeader>
        <DialogTitle>{rule ? "Edit rule" : "Add rule"}</DialogTitle>
        <DialogDescription>
          Allow or deny connections to a host. An environment rule covers every
          sandbox.
        </DialogDescription>
      </DialogHeader>
      <Field data-invalid={!!hostError || undefined}>
        <FieldLabel htmlFor="rule-host">Host</FieldLabel>
        <Input
          id="rule-host"
          autoFocus
          autoComplete="off"
          placeholder="example.com or *.example.com"
          value={host}
          aria-invalid={!!hostError}
          onChange={(e) => setHost(e.target.value)}
        />
        {hostError && <FieldError>{hostError}</FieldError>}
      </Field>
      <Field data-invalid={portList === null || undefined}>
        <FieldLabel htmlFor="rule-ports">Ports</FieldLabel>
        <Input
          id="rule-ports"
          autoComplete="off"
          placeholder="Any port"
          value={ports}
          aria-invalid={portList === null}
          onChange={(e) => setPorts(e.target.value)}
        />
        {portList === null ? (
          <FieldError>
            Use port numbers from 1 to 65535, separated by commas.
          </FieldError>
        ) : (
          <FieldDescription>
            Comma-separated. Empty means any port.
          </FieldDescription>
        )}
      </Field>
      <Field>
        <FieldLabel>Action</FieldLabel>
        <ToggleGroup
          aria-label="Action"
          variant="outline"
          value={[action]}
          onValueChange={(v) => {
            if (v[0]) setAction(v[0] === "deny" ? "deny" : "allow")
          }}
          className="w-full"
        >
          <ToggleGroupItem value="allow" className="flex-1">
            Allow
          </ToggleGroupItem>
          <ToggleGroupItem value="deny" className="flex-1">
            Deny
          </ToggleGroupItem>
        </ToggleGroup>
      </Field>
      <Field>
        <FieldLabel htmlFor="rule-scope">Scope</FieldLabel>
        <Select
          value={scope}
          onValueChange={(v) => setScope(v ?? "environment")}
          items={{
            environment: "Environment",
            ...Object.fromEntries(sandboxes.map((sb) => [sb.id, sb.name])),
          }}
        >
          <SelectTrigger id="rule-scope" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="environment">Environment</SelectItem>
            {sandboxes.map((sb) => (
              <SelectItem key={sb.id} value={sb.id}>
                {sb.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel htmlFor="rule-note">Note</FieldLabel>
        <Input
          id="rule-note"
          autoComplete="off"
          maxLength={500}
          placeholder="Optional"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </Field>
      <DialogFooter>
        <Button type="submit" disabled={!valid || saving}>
          {saving && <Spinner />}
          {rule ? "Save" : "Add rule"}
        </Button>
      </DialogFooter>
    </form>
  )
}
