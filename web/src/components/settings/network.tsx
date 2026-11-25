import { useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  DesktopIcon,
  PencilSimpleIcon,
  PlusIcon,
  ShareNetworkIcon,
  TrashIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { SettingsPage } from "@/components/settings/page"
import { SectionHeader } from "@/components/section-header"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  errorStatus,
  type PrivateHost,
} from "@/lib/api/client"

export function NetworkSettings({ env }: { env: string }) {
  return (
    <SettingsPage title="Ports and LAN">
      <HostPortsSection key={`host-${env}`} env={env} />
      <LocalNetworkSection key={`lan-${env}`} env={env} />
    </SettingsPage>
  )
}

/** Comma-separated ports, or null when one is not a number from 1 to 65535. Empty is no ports. */
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

/** The ports a host may be reached on: none listed means 80 and 443. */
function portsOf(h: PrivateHost) {
  return h.ports?.length ? h.ports : [80, 443]
}

/** Whether after lets computers reach more than before did: a new host, or another port. */
function widens(before: PrivateHost | undefined, after: PrivateHost) {
  if (!before || before.host.toLowerCase() !== after.host.toLowerCase())
    return true
  const old = portsOf(before)
  return portsOf(after).some((p) => !old.includes(p))
}

/** A change to the list, waiting for the user to type the host it opens up. */
type Proposal = {
  next: PrivateHost[]
  host: PrivateHost
  done: () => void
  fail: (message: string) => void
}

function LocalNetworkSection({ env }: { env: string }) {
  const queryClient = useQueryClient()
  const list = $api.useQuery("get", "/api/environments/{env}/private-hosts", {
    params: { path: { env } },
  })
  // The add form remounts empty under a new key once its host is saved.
  const [addKey, setAddKey] = useState(0)
  const [addError, setAddError] = useState<string>()
  const [editing, setEditing] = useState<string>()
  const [editError, setEditError] = useState<string>()
  const [proposal, setProposal] = useState<Proposal>()
  const hosts = list.data ?? []

  const save = $api.useMutation(
    "put",
    "/api/environments/{env}/private-hosts",
    {
      onSuccess: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/private-hosts"],
        })
        // A rule's warning depends on the list.
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/rules"],
        })
      },
    }
  )

  const write = (
    next: PrivateHost[],
    done?: () => void,
    fail?: (message: string) => void
  ) =>
    save.mutate(
      {
        params: { path: { env } },
        body: {
          hosts: next.map((h) => ({ host: h.host, ports: h.ports })),
        },
      },
      {
        onSuccess: () => done?.(),
        onError: (err) => {
          // A 422 says which host is wrong, so it stays in its form.
          if (errorStatus(err) === 422 && fail) fail(errorMessage(err))
          else
            toast.error("Could not save the local network", {
              description: errorMessage(err),
            })
        },
      }
    )

  // A change that lets computers reach more waits for the user to type the host.
  const propose = (
    next: PrivateHost[],
    host: PrivateHost,
    before: PrivateHost | undefined,
    done: () => void,
    fail: (message: string) => void
  ) => {
    if (widens(before, host)) setProposal({ next, host, done, fail })
    else write(next, done, fail)
  }

  const add = (h: PrivateHost) => {
    setAddError(undefined)
    propose(
      [...hosts, h],
      h,
      undefined,
      () => setAddKey((k) => k + 1),
      setAddError
    )
  }

  const update = (before: PrivateHost, h: PrivateHost) => {
    setEditError(undefined)
    propose(
      hosts.map((x) => (x.host === before.host ? h : x)),
      h,
      before,
      () => setEditing(undefined),
      setEditError
    )
  }

  return (
    <section className="grid gap-2">
      <SectionHeader
        title="Local network"
        icon={ShareNetworkIcon}
        area="network"
        count={hosts.length || undefined}
      />
      <TrustNote
        anchor="local-network"
        warning="Only list hosts you trust: approvals, rules and imported environment files never add them here."
      >
        Computers reach public addresses only. List the devices on your local
        network or behind this machine's VPN that they may reach too, such as a
        NAS at 192.168.178.20 or git.company.local. Each connection still needs
        a rule or an approval.
      </TrustNote>

      {/* One card: the hosts as rows, then the form to add one as its last row. */}
      <div className="divide-y overflow-hidden rounded-(--radius) border bg-card text-xs">
        {list.isPending ? (
          <div className="grid h-(--wb-row) place-items-center">
            <Spinner />
          </div>
        ) : list.isError && !list.data ? (
          <p role="alert" className="px-3 py-2.5 text-destructive">
            Could not load the local network: {errorMessage(list.error)}
          </p>
        ) : hosts.length === 0 ? (
          <p className="px-3 py-2.5 text-muted-foreground">
            Nothing listed. Computers reach public addresses only.
          </p>
        ) : (
          <ul className="divide-y" aria-label="Local network">
            {hosts.map((h) =>
              editing === h.host ? (
                <li key={h.host} aria-label={`Edit ${h.host}`}>
                  <HostForm
                    id="private-host-edit"
                    initial={h}
                    label="Save"
                    pending={save.isPending}
                    error={editError}
                    onChange={() => setEditError(undefined)}
                    onSubmit={(next) => update(h, next)}
                    onCancel={() => {
                      setEditing(undefined)
                      setEditError(undefined)
                    }}
                  />
                </li>
              ) : (
                <li
                  key={h.host}
                  className="flex min-h-(--wb-row) items-center gap-3 px-3 py-1.5"
                >
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="truncate font-mono">{h.host}</span>
                    <LANNameHint host={h.host} />
                  </span>
                  <span className="shrink-0 font-mono text-caption text-muted-foreground">
                    Ports {portsOf(h).join(", ")}
                  </span>
                  <span className="flex shrink-0 items-center">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      title="Edit"
                      aria-label={`Edit ${h.host}`}
                      disabled={save.isPending}
                      onClick={() => {
                        setEditing(h.host)
                        setEditError(undefined)
                      }}
                    >
                      <PencilSimpleIcon />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      title="Remove"
                      aria-label={`Remove ${h.host}`}
                      disabled={save.isPending}
                      onClick={() =>
                        write(hosts.filter((x) => x.host !== h.host))
                      }
                    >
                      <TrashIcon />
                    </Button>
                  </span>
                </li>
              )
            )}
          </ul>
        )}

        <HostForm
          key={addKey}
          id="private-host"
          label="Add"
          icon={<PlusIcon />}
          pending={save.isPending}
          error={addError}
          onChange={() => setAddError(undefined)}
          onSubmit={add}
        />
      </div>
      {proposal && (
        <ConfirmHostDialog
          host={proposal.host}
          pending={save.isPending}
          onCancel={() => setProposal(undefined)}
          onConfirm={() =>
            write(
              proposal.next,
              () => {
                setProposal(undefined)
                proposal.done()
              },
              (message) => {
                setProposal(undefined)
                proposal.fail(message)
              }
            )
          }
        />
      )}
    </section>
  )
}

/** The host and its ports, to add a host or to change one. */
function HostForm({
  id,
  initial,
  label,
  icon,
  pending,
  error,
  onChange,
  onSubmit,
  onCancel,
}: {
  id: string
  initial?: PrivateHost
  label: string
  icon?: ReactNode
  pending: boolean
  error?: string
  onChange: () => void
  onSubmit: (host: PrivateHost) => void
  onCancel?: () => void
}) {
  const [host, setHost] = useState(initial?.host ?? "")
  const [ports, setPorts] = useState(initial?.ports?.join(", ") ?? "")
  const portList = parsePorts(ports)
  const ready = !!host.trim() && portList !== null && !pending
  return (
    <form
      className="grid gap-3 bg-muted/30 p-3 sm:grid-cols-[minmax(0,1fr)_11rem_auto] sm:items-start"
      onSubmit={(e) => {
        e.preventDefault()
        if (ready) onSubmit({ host: host.trim(), ports: portList })
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape" && onCancel) {
          e.preventDefault()
          onCancel()
        }
      }}
    >
      <Field>
        <FieldLabel htmlFor={id}>Host</FieldLabel>
        <Input
          id={id}
          autoComplete="off"
          spellCheck={false}
          autoFocus={!!initial}
          placeholder="e.g. 192.168.178.20, 192.168.178.0/24 or git.company.local"
          value={host}
          onChange={(e) => {
            setHost(e.target.value)
            onChange()
          }}
        />
        {error ? (
          <FieldError>{error}</FieldError>
        ) : (
          <FieldDescription>
            An address, a range, a name or a wildcard such as *.company.local.
          </FieldDescription>
        )}
      </Field>
      <Field data-invalid={portList === null || undefined}>
        <FieldLabel htmlFor={`${id}-ports`}>Ports</FieldLabel>
        <Input
          id={`${id}-ports`}
          autoComplete="off"
          placeholder="e.g. 80, 443"
          value={ports}
          aria-invalid={portList === null}
          onChange={(e) => {
            setPorts(e.target.value)
            onChange()
          }}
        />
        {portList === null ? (
          <FieldError>Port numbers from 1 to 65535, with commas.</FieldError>
        ) : (
          <FieldDescription>
            List others than 80 and 443, such as 22 for SSH.
          </FieldDescription>
        )}
      </Field>
      <div className="flex gap-2 sm:mt-[1.375rem]">
        <Button type="submit" variant="outline" disabled={!ready}>
          {pending ? <Spinner /> : icon}
          {label}
        </Button>
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
      </div>
    </form>
  )
}

/**
 * Asks the user to type the host a change opens up before it is saved, so a host isn't listed
 * by a slip or by someone else's instructions.
 */
function ConfirmHostDialog({
  host,
  pending,
  onConfirm,
  onCancel,
}: {
  host: PrivateHost
  pending: boolean
  onConfirm: () => void
  onCancel: () => void
}) {
  const [typed, setTyped] = useState("")
  const ok = typed.trim().toLowerCase() === host.host.toLowerCase()
  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        if (!open) onCancel()
      }}
    >
      <AlertDialogContent>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (ok && !pending) onConfirm()
          }}
        >
          <AlertDialogHeader>
            <AlertDialogTitle>Let computers reach this host?</AlertDialogTitle>
            <AlertDialogDescription>
              Computers can then connect to{" "}
              <span className="font-mono break-all text-foreground">
                {host.host}
              </span>{" "}
              on ports {portsOf(host).join(", ")}, once a rule or an approval
              allows it. Only list hosts you trust.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Field>
            <FieldLabel htmlFor="confirm-private-host">
              Type the host to confirm
            </FieldLabel>
            <Input
              id="confirm-private-host"
              autoFocus
              autoComplete="off"
              spellCheck={false}
              placeholder={host.host}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
          </Field>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction type="submit" disabled={!ok || pending}>
              {pending && <Spinner />}
              Allow
            </AlertDialogAction>
          </AlertDialogFooter>
        </form>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/**
 * What a section opens up, with a link to its docs, then the warning every opt-in of this page
 * carries.
 */
function TrustNote({
  anchor,
  warning,
  children,
}: {
  anchor: string
  warning: string
  children: ReactNode
}) {
  return (
    <div className="grid gap-1.5 text-xs/relaxed">
      <p className="max-w-3xl text-muted-foreground">
        {children}{" "}
        <a
          href={`https://lukaskoebe.github.io/sandbox-studio/network/#${anchor}`}
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-4"
        >
          Learn more
        </a>
      </p>
      <p role="note" className="flex gap-1.5">
        <WarningIcon
          className="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-400"
          aria-hidden
        />
        {warning}
      </p>
    </div>
  )
}

const ipv4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(\/\d{1,2})?$/

/**
 * The name sandboxes use for a listed address: microsandbox refuses private addresses, so a
 * sandbox connects to 192-168-178-20.lan.studio.internal, which the gateway turns back into
 * 192.168.178.20. A listed name is used as it is, so it gets no hint.
 */
function LANNameHint({ host }: { host: string }) {
  const m = ipv4.exec(host)
  if (!m) return null
  const name = m[5]
    ? `${m[1]}-${m[2]}-${m[3]}-N.lan.studio.internal`
    : `${host.replaceAll(".", "-")}.lan.studio.internal`
  return (
    <span className="text-caption break-all text-muted-foreground">
      Computers connect to <span className="font-mono">{name}</span>
    </span>
  )
}

/** The ports of this computer open to sandboxes, as host.studio.internal. */
function HostPortsSection({ env }: { env: string }) {
  const queryClient = useQueryClient()
  const list = $api.useQuery("get", "/api/environments/{env}/host-ports", {
    params: { path: { env } },
  })
  const [port, setPort] = useState("")
  const ports = list.data ?? []
  const parsed = parsePorts(port)
  const save = $api.useMutation("put", "/api/environments/{env}/host-ports", {
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/host-ports"],
      })
      // A rule's warning depends on the open ports.
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/rules"],
      })
      setPort("")
    },
    onError: (err) =>
      toast.error("Could not save the open ports", {
        description: errorMessage(err),
      }),
  })
  const write = (next: number[]) =>
    save.mutate({ params: { path: { env } }, body: { ports: next } })
  const add = () => {
    if (!parsed?.length || save.isPending) return
    write([...ports, ...parsed])
  }

  return (
    <section className="grid gap-2">
      <SectionHeader
        title="This machine"
        icon={DesktopIcon}
        area="network"
        count={ports.length || undefined}
      />
      <TrustNote
        anchor="this-machine"
        warning="Only open the ports of services you trust computers with."
      >
        Computers never reach the machine Studio runs on. Open a port to let
        them connect to a service here, such as SSH on 22 or a database, at{" "}
        <span className="font-mono">host.studio.internal</span>. Each connection
        still needs a rule or an approval, and Studio's own ports stay closed.
      </TrustNote>

      <div className="divide-y overflow-hidden rounded-(--radius) border bg-card text-xs">
        {list.isPending ? (
          <div className="grid h-(--wb-row) place-items-center">
            <Spinner />
          </div>
        ) : list.isError && !list.data ? (
          <p role="alert" className="px-3 py-2.5 text-destructive">
            Could not load the open ports: {errorMessage(list.error)}
          </p>
        ) : ports.length === 0 ? (
          <p className="px-3 py-2.5 text-muted-foreground">
            No ports open. Computers can't reach this machine.
          </p>
        ) : (
          <ul className="divide-y" aria-label="Open ports">
            {ports.map((p) => (
              <li key={p} className="flex h-(--wb-row) items-center gap-3 px-3">
                <span className="min-w-0 flex-1 truncate font-mono">
                  host.studio.internal:{p}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  title="Close"
                  aria-label={`Close port ${p}`}
                  disabled={save.isPending}
                  onClick={() => write(ports.filter((x) => x !== p))}
                >
                  <TrashIcon />
                </Button>
              </li>
            ))}
          </ul>
        )}
        <form
          className="grid gap-3 bg-muted/30 p-3 sm:grid-cols-[11rem_auto] sm:items-start"
          onSubmit={(e) => {
            e.preventDefault()
            add()
          }}
        >
          <Field data-invalid={parsed === null || undefined}>
            <FieldLabel htmlFor="host-port">Port</FieldLabel>
            <Input
              id="host-port"
              autoComplete="off"
              inputMode="numeric"
              placeholder="e.g. 22"
              value={port}
              aria-invalid={parsed === null}
              onChange={(e) => setPort(e.target.value)}
            />
            {parsed === null && (
              <FieldError>
                Port numbers from 1 to 65535, with commas.
              </FieldError>
            )}
          </Field>
          <Button
            type="submit"
            variant="outline"
            className="sm:mt-[1.375rem] sm:justify-self-start"
            disabled={!parsed?.length || save.isPending}
          >
            {save.isPending ? <Spinner /> : <PlusIcon />}
            Open port
          </Button>
        </form>
      </div>
    </section>
  )
}
