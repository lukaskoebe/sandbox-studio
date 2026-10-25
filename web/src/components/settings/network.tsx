import { useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  DesktopIcon,
  PlusIcon,
  ShareNetworkIcon,
  TrashIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { SettingsPage } from "@/components/settings/page"
import { SectionHeader } from "@/components/section-header"
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

function LocalNetworkSection({ env }: { env: string }) {
  const queryClient = useQueryClient()
  const list = $api.useQuery("get", "/api/environments/{env}/private-hosts", {
    params: { path: { env } },
  })
  const [host, setHost] = useState("")
  const [ports, setPorts] = useState("")
  const [formError, setFormError] = useState<string>()
  const portList = parsePorts(ports)
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
        setHost("")
        setPorts("")
        setFormError(undefined)
      },
      onError: (err) => {
        // A 422 says which host is wrong, so it stays in the form.
        if (errorStatus(err) === 422) setFormError(errorMessage(err))
        else
          toast.error("Could not save the local network", {
            description: errorMessage(err),
          })
      },
    }
  )

  const write = (next: PrivateHost[]) =>
    save.mutate({
      params: { path: { env } },
      body: {
        hosts: next.map((h) => ({ host: h.host, ports: h.ports })),
      },
    })

  const add = () => {
    if (!host.trim() || portList === null || save.isPending) return
    setFormError(undefined)
    write([...hosts, { host: host.trim(), ports: portList }])
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
        Sandboxes reach public addresses only. List the devices on your local
        network or behind this computer's VPN that they may reach too, such as a
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
            Nothing listed. Sandboxes reach public addresses only.
          </p>
        ) : (
          <ul className="divide-y" aria-label="Local network">
            {hosts.map((h) => (
              <li
                key={h.host}
                className="flex min-h-(--wb-row) items-center gap-3 px-3 py-1.5"
              >
                <span className="grid min-w-0 flex-1 gap-0.5">
                  <span className="truncate font-mono">{h.host}</span>
                  <LANNameHint host={h.host} />
                </span>
                <span className="shrink-0 font-mono text-[0.6875rem] text-muted-foreground">
                  {h.ports?.length
                    ? `Ports ${h.ports.join(", ")}`
                    : "Ports 80, 443"}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  title="Remove"
                  aria-label={`Remove ${h.host}`}
                  disabled={save.isPending}
                  onClick={() => write(hosts.filter((x) => x.host !== h.host))}
                >
                  <TrashIcon />
                </Button>
              </li>
            ))}
          </ul>
        )}

        <form
          className="grid gap-3 bg-muted/30 p-3 sm:grid-cols-[minmax(0,1fr)_11rem_auto] sm:items-start"
          onSubmit={(e) => {
            e.preventDefault()
            add()
          }}
        >
          <Field>
            <FieldLabel htmlFor="private-host">Host</FieldLabel>
            <Input
              id="private-host"
              autoComplete="off"
              placeholder="192.168.178.20, 192.168.178.0/24 or git.company.local"
              value={host}
              onChange={(e) => {
                setHost(e.target.value)
                setFormError(undefined)
              }}
            />
            {formError ? (
              <FieldError>{formError}</FieldError>
            ) : (
              <FieldDescription>
                An address, a range, a name or a wildcard such as
                *.company.local.
              </FieldDescription>
            )}
          </Field>
          <Field data-invalid={portList === null || undefined}>
            <FieldLabel htmlFor="private-host-ports">Ports</FieldLabel>
            <Input
              id="private-host-ports"
              autoComplete="off"
              placeholder="80, 443"
              value={ports}
              aria-invalid={portList === null}
              onChange={(e) => {
                setPorts(e.target.value)
                setFormError(undefined)
              }}
            />
            {portList === null ? (
              <FieldError>
                Port numbers from 1 to 65535, with commas.
              </FieldError>
            ) : (
              <FieldDescription>
                List others than 80 and 443, such as 22 for SSH.
              </FieldDescription>
            )}
          </Field>
          <Button
            type="submit"
            variant="outline"
            className="sm:mt-[1.375rem]"
            disabled={!host.trim() || portList === null || save.isPending}
          >
            {save.isPending ? <Spinner /> : <PlusIcon />}
            Add
          </Button>
        </form>
      </div>
    </section>
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
    <span className="text-[0.6875rem] break-all text-muted-foreground">
      Sandboxes connect to <span className="font-mono">{name}</span>
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
        title="This computer"
        icon={DesktopIcon}
        area="network"
        count={ports.length || undefined}
      />
      <TrustNote
        anchor="this-computer"
        warning="Only open the ports of services you trust sandboxes with."
      >
        Sandboxes never reach the computer Studio runs on. Open a port to let
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
            No ports open. Sandboxes can't reach this computer.
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
              placeholder="22"
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
