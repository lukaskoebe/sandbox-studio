import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PlusIcon, TrashIcon, WarningIcon } from "@phosphor-icons/react"
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
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-5 p-4">
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
    <section className="grid max-w-2xl gap-4">
      <SectionHeader title="Private hosts" />
      <div
        role="note"
        className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs/relaxed"
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

      {list.isPending ? (
        <Spinner className="mx-auto block" />
      ) : list.isError && !list.data ? (
        <p role="alert" className="text-xs text-destructive">
          Could not load the private hosts: {errorMessage(list.error)}
        </p>
      ) : hosts.length === 0 ? (
        <p className="text-xs/relaxed text-muted-foreground">
          No private hosts. Sandboxes reach public addresses only.
        </p>
      ) : (
        <ul className="grid gap-2" aria-label="Private hosts">
          {hosts.map((h) => (
            <li
              key={h.host}
              className="flex items-center gap-3 rounded-md border px-3 py-2 text-sm"
            >
              <span className="min-w-0 flex-1 truncate font-mono">
                {h.host}
              </span>
              <span className="shrink-0 text-xs text-muted-foreground">
                {h.ports?.length
                  ? `Ports ${h.ports.join(", ")}`
                  : "Ports 80, 443"}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
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
        className="grid gap-3"
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
        </Field>
        <Field data-invalid={portList === null || undefined}>
          <FieldLabel htmlFor="private-host-ports">Ports</FieldLabel>
          <Input
            id="private-host-ports"
            autoComplete="off"
            placeholder="80, 443 by default"
            value={ports}
            aria-invalid={portList === null}
            onChange={(e) => {
              setPorts(e.target.value)
              setFormError(undefined)
            }}
          />
          {portList === null ? (
            <FieldError>
              Use port numbers from 1 to 65535, separated by commas.
            </FieldError>
          ) : (
            <FieldDescription>
              Other ports than 80 and 443 must be listed, such as 22 for SSH.
            </FieldDescription>
          )}
        </Field>
        {formError && <FieldError>{formError}</FieldError>}
        <div>
          <Button
            type="submit"
            variant="outline"
            disabled={!host.trim() || portList === null || save.isPending}
          >
            {save.isPending ? <Spinner /> : <PlusIcon />}
            Add private host
          </Button>
        </div>
      </form>
    </section>
  )
}
