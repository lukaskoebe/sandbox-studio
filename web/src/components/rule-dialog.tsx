import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { KeyIcon, PlusIcon, XIcon } from "@phosphor-icons/react"
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
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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

type Action = "allow" | "proxy" | "deny"
const actions: Action[] = ["allow", "proxy", "deny"]

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

/** A header row in the form; the id keys the row while it is edited. */
type HeaderRow = { id: number; name: string; value: string }

let headerRowIds = 0
function headerRow(name = "", value = ""): HeaderRow {
  headerRowIds += 1
  return { id: headerRowIds, name, value }
}

/** The rows of an existing rule's headers, or one empty row to start from. */
function initialHeaders(rule?: Rule): HeaderRow[] {
  const rows = Object.entries(rule?.config.headers ?? {}).map(([name, value]) =>
    headerRow(name, value)
  )
  return rows.length ? rows : [headerRow()]
}

const headerToken = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

/** Why each row cannot be sent, by index; blank rows are ignored. */
function headerProblems(rows: HeaderRow[]): (string | undefined)[] {
  const names = rows.map((r) => r.name.trim().toLowerCase())
  return rows.map((row, i) => {
    const name = row.name.trim()
    if (!name) {
      return row.value.trim() ? "Each value needs a header name." : undefined
    }
    if (!headerToken.test(name)) return `${name} is not a valid header name.`
    if (names.indexOf(names[i]) !== i) return `${name} is set more than once.`
    return undefined
  })
}

/** The headers to send, trimmed, with blank rows dropped. */
function headerPayload(rows: HeaderRow[]): Record<string, string> | undefined {
  const entries = rows
    .map((r) => [r.name.trim(), r.value.trim()] as const)
    .filter(([name, value]) => name || value)
  return entries.length ? Object.fromEntries(entries) : undefined
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
  const [action, setAction] = useState<Action>(
    rule?.action === "deny" || rule?.action === "proxy" ? rule.action : "allow"
  )
  const [headers, setHeaders] = useState(() => initialHeaders(rule))
  const [scope, setScope] = useState(rule?.sandboxId ?? "environment")
  const [note, setNote] = useState(rule?.note ?? "")
  const [formError, setFormError] = useState<string>()
  const portList = parsePorts(ports)
  const problems = headerProblems(headers)
  // Headers are only sent for proxy rules, so only proxy rules are checked.
  const headerError = action === "proxy" ? problems.find(Boolean) : undefined

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
  // A 422 explains which pattern, sandbox or header is wrong, so it stays in the form.
  const onFailed = (err: unknown) => {
    if (errorStatus(err) === 422) setFormError(errorMessage(err))
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
  const valid = host.trim() !== "" && portList !== null && !headerError

  const submit = () => {
    if (portList === null || !host.trim() || headerError || saving) return
    setFormError(undefined)
    const headerValues = action === "proxy" ? headerPayload(headers) : undefined
    const body = {
      host: host.trim(),
      ports: portList,
      action,
      sandboxId: scope === "environment" ? undefined : scope,
      note: note.trim() || undefined,
      config: headerValues ? { headers: headerValues } : undefined,
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
          Allow, proxy or deny connections to a host. A proxy rule lets Studio
          handle the HTTPS requests to set headers, such as an API key from a
          secret. An environment rule covers every sandbox.
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="rule-host">Host</FieldLabel>
        <Input
          id="rule-host"
          autoFocus
          autoComplete="off"
          placeholder="example.com or *.example.com"
          value={host}
          onChange={(e) => setHost(e.target.value)}
        />
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
            const next = actions.find((a) => a === v[0])
            if (next) setAction(next)
          }}
          className="w-full"
        >
          <ToggleGroupItem value="allow" className="flex-1">
            Allow
          </ToggleGroupItem>
          <ToggleGroupItem value="proxy" className="flex-1">
            Proxy
          </ToggleGroupItem>
          <ToggleGroupItem value="deny" className="flex-1">
            Deny
          </ToggleGroupItem>
        </ToggleGroup>
      </Field>
      {action === "proxy" && (
        <HeadersField
          env={env}
          rows={headers}
          problems={problems}
          onChange={setHeaders}
        />
      )}
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
      {formError && <FieldError>{formError}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={!valid || saving}>
          {saving && <Spinner />}
          {rule ? "Save" : "Add rule"}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** The headers a proxy rule sets on every request, one row each. */
function HeadersField({
  env,
  rows,
  problems,
  onChange,
}: {
  env: string
  rows: HeaderRow[]
  problems: (string | undefined)[]
  onChange: (rows: HeaderRow[]) => void
}) {
  const secrets = $api.useQuery("get", "/api/environments/{env}/secrets", {
    params: { path: { env } },
  })
  const list = secrets.data ?? []
  const error = problems.find(Boolean)
  const change = (id: number, patch: Partial<HeaderRow>) =>
    onChange(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)))

  return (
    <Field data-invalid={!!error || undefined}>
      <FieldLabel>Headers</FieldLabel>
      <div className="-mx-0.5 flex max-h-48 flex-col gap-2 overflow-y-auto px-0.5 py-0.5">
        {rows.map((row, i) => (
          <div key={row.id} className="flex items-center gap-1">
            <Input
              aria-label="Header name"
              autoComplete="off"
              placeholder="Header"
              className="w-2/5 shrink-0 font-mono"
              value={row.name}
              aria-invalid={!!problems[i]}
              onChange={(e) => change(row.id, { name: e.target.value })}
            />
            <Input
              aria-label="Header value"
              autoComplete="off"
              placeholder="Value"
              className="min-w-0 flex-1 font-mono"
              value={row.value}
              onChange={(e) => change(row.id, { value: e.target.value })}
            />
            {list.length > 0 && (
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      title="Insert secret"
                      aria-label="Insert secret"
                    />
                  }
                >
                  <KeyIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="min-w-40">
                  <DropdownMenuGroup>
                    <DropdownMenuLabel>Insert a secret</DropdownMenuLabel>
                    {list.map((s) => (
                      <DropdownMenuItem
                        key={s.id}
                        className="font-mono"
                        onClick={() =>
                          change(row.id, {
                            value: `${row.value}{secret.${s.name}}`,
                          })
                        }
                      >
                        {s.name}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            )}
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              title="Remove header"
              aria-label="Remove header"
              onClick={() => onChange(rows.filter((r) => r.id !== row.id))}
            >
              <XIcon />
            </Button>
          </div>
        ))}
      </div>
      <div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...rows, headerRow()])}
        >
          <PlusIcon />
          Add header
        </Button>
      </div>
      {error ? (
        <FieldError>{error}</FieldError>
      ) : (
        <FieldDescription>
          Set on every request, replacing the sandbox's own.{" "}
          <code>{"{secret.NAME}"}</code> is replaced by the secret's value, but
          only for hosts the secret is bound to.
        </FieldDescription>
      )}
    </Field>
  )
}
