import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BuildingsIcon,
  PlusIcon,
  TrashIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { EnvironmentLabel, PageHeader } from "@/components/page-header"
import { SectionHeader } from "@/components/section-header"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  errorStatus,
  type PrivateHost,
} from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/settings/network")({
  component: NetworkPage,
})

function NetworkPage() {
  const { env } = Route.useParams()
  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Network</h1>
        <EnvironmentLabel env={env} />
      </PageHeader>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="space-y-5 p-3 md:p-4"
      >
        <PrivateHostsSection key={env} env={env} />
      </ScrollArea>
    </>
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

function PrivateHostsSection({ env }: { env: string }) {
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
        setHost("")
        setPorts("")
        setFormError(undefined)
      },
      onError: (err) => {
        // A 422 says which host is wrong, so it stays in the form.
        if (errorStatus(err) === 422) setFormError(errorMessage(err))
        else
          toast.error("Could not save the private hosts", {
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
    <section className="grid max-w-3xl gap-3">
      <SectionHeader
        title="Private hosts"
        icon={BuildingsIcon}
        area="network"
        count={hosts.length || undefined}
      />
      <div
        role="note"
        className="flex gap-2 rounded-(--radius) border border-amber-500/40 bg-amber-500/10 p-3 text-xs/relaxed"
      >
        <WarningIcon className="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
        <p>
          Private hosts let sandboxes reach company hosts that resolve to
          private addresses, through this machine's VPN. Once listed, a host's
          private addresses are reachable from every sandbox of this environment
          that a rule allows the host for. Only list hosts you trust. Approvals,
          rules and imported environment files never add hosts here.{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/network/#private-hosts-company-vpn"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </p>
      </div>

      {/* One card: the hosts as rows, then the form to add one as its last row. */}
      <div className="divide-y overflow-hidden rounded-(--radius) border bg-card text-xs">
        {list.isPending ? (
          <div className="grid h-(--wb-row) place-items-center">
            <Spinner />
          </div>
        ) : list.isError && !list.data ? (
          <p role="alert" className="px-3 py-2.5 text-destructive">
            Could not load the private hosts: {errorMessage(list.error)}
          </p>
        ) : hosts.length === 0 ? (
          <p className="px-3 py-2.5 text-muted-foreground">
            No private hosts. Sandboxes reach public addresses only.
          </p>
        ) : (
          <ul className="divide-y" aria-label="Private hosts">
            {hosts.map((h) => (
              <li
                key={h.host}
                className="flex h-(--wb-row) items-center gap-3 px-3"
              >
                <span className="min-w-0 flex-1 truncate font-mono">
                  {h.host}
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
              placeholder="git.company.local or *.company.local"
              value={host}
              onChange={(e) => {
                setHost(e.target.value)
                setFormError(undefined)
              }}
            />
            {formError && <FieldError>{formError}</FieldError>}
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
            Add private host
          </Button>
        </form>
      </div>
    </section>
  )
}
