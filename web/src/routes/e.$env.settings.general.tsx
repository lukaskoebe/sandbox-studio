import { useRef, useState } from "react"
import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowCircleUpIcon,
  DownloadSimpleIcon,
  ListChecksIcon,
  PowerIcon,
  TerminalWindowIcon,
  UploadSimpleIcon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { AgentViewToggle } from "@/components/agent-view"
import { Ident } from "@/components/ident"
import { PageHeader } from "@/components/page-header"
import { SectionHeader } from "@/components/section-header"
import { setupOver, useSetup } from "@/lib/setup"
import {
  $api,
  errorMessage,
  fetchClient,
  type ImportedEnvironment,
} from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings/general")({
  component: GeneralPage,
})

// Settings apply to the whole installation, not to one environment.
function GeneralPage() {
  const { env } = Route.useParams()
  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">General</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-6 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          These settings apply to this Studio installation and every
          environment.
        </p>
        <AgentsSection />
        <AutostartSection />
        <UpdatesSection />
        <SetupSection />
        <EnvironmentFilesSection env={env} />
      </ScrollArea>
    </>
  )
}

/** Exports this environment's configuration as a file, and imports one as a new environment. */
function EnvironmentFilesSection({ env }: { env: string }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const picker = useRef<HTMLInputElement>(null)
  const [importing, setImporting] = useState(false)
  const [imported, setImported] = useState<ImportedEnvironment>()

  const importFile = async (file: File) => {
    setImporting(true)
    try {
      // The server checks the file again; parsing here only turns it into the request body.
      const body = JSON.parse(await file.text())
      const result = await fetchClient.POST("/api/environment-imports", {
        body,
      })
      if (result.error) {
        toast.error("Could not import the environment", {
          description: errorMessage(result.error),
        })
        return
      }
      queryClient.invalidateQueries({ queryKey: ["get", "/api/environments"] })
      setImported(result.data)
    } catch (err) {
      toast.error("Could not import the environment", {
        description:
          err instanceof SyntaxError
            ? "The file is not JSON."
            : errorMessage(err),
      })
    } finally {
      setImporting(false)
      if (picker.current) picker.current.value = ""
    }
  }

  return (
    <section className="grid max-w-3xl gap-2 text-xs">
      <SectionHeader title="Environment files" icon={DownloadSimpleIcon} />
      <p className="text-xs/relaxed text-muted-foreground">
        A file of this environment's providers, teammates, rules, secret names
        and template specs, without keys, secret values, sandboxes, memory or
        approvals. Read it before you share it: template setup and soul text are
        included.
      </p>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="outline"
          size="sm"
          render={
            <a
              href={`/api/environments/${encodeURIComponent(env)}/export`}
              download
            />
          }
        >
          <DownloadSimpleIcon />
          Export environment
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={importing}
          onClick={() => picker.current?.click()}
        >
          {importing ? <Spinner /> : <UploadSimpleIcon />}
          Import environment
        </Button>
        <input
          ref={picker}
          type="file"
          accept=".studio-environment,application/json"
          className="hidden"
          aria-label="Environment file"
          onChange={(e) => {
            const file = e.target.files?.[0]
            if (file) void importFile(file)
          }}
        />
      </div>
      {imported && (
        <ImportReport
          result={imported}
          onOpenChange={(open) => {
            if (!open) setImported(undefined)
          }}
          onOpen={() => {
            const id = imported.environment.id
            setImported(undefined)
            navigate({ to: "/e/$env", params: { env: id } })
          }}
        />
      )}
    </section>
  )
}

/** What an import created, and what the file could not carry: keys, secret values and templates. */
function ImportReport({
  result,
  onOpenChange,
  onOpen,
}: {
  result: ImportedEnvironment
  onOpenChange: (open: boolean) => void
  onOpen: () => void
}) {
  const env = result.environment.id
  const toAdd = result.toAdd ?? []
  const templates = result.templates ?? []
  const keys = toAdd.filter((item) => item.kind === "providerKey")
  const secrets = toAdd.filter((item) => item.kind === "secret")
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{result.environment.name} is ready</DialogTitle>
          <DialogDescription>
            The file's providers, teammates, rules, secret names and template
            specs are in place. Add what the file leaves out:
          </DialogDescription>
        </DialogHeader>
        <ul className="grid gap-2 text-xs">
          {keys.map((item) => (
            <li key={`key-${item.providerId}`}>
              <Link
                to="/e/$env/settings/providers"
                params={{ env }}
                className="underline underline-offset-4"
              >
                Add the API key of {item.name}
              </Link>
            </li>
          ))}
          {secrets.map((item) => (
            <li key={`secret-${item.name}-${item.teammate ?? ""}`}>
              <Link
                to="/e/$env/secrets"
                params={{ env }}
                className="underline underline-offset-4"
              >
                Add the value of {item.name}
              </Link>
              <span className="text-muted-foreground">
                {" "}
                for {item.hosts?.join(", ")}
                {item.teammate ? `, ${item.teammate}'s sandboxes` : ""}
              </span>
            </li>
          ))}
          {templates.length > 0 && (
            <li>
              <Link
                to="/e/$env/builds"
                params={{ env }}
                className="underline underline-offset-4"
              >
                Build {templates.length} template
                {templates.length === 1 ? "" : "s"}
              </Link>
              <span className="text-muted-foreground">
                {" "}
                from the specs in the file
              </span>
            </li>
          )}
          {toAdd.length === 0 && templates.length === 0 && (
            <li className="text-muted-foreground">Nothing else is needed.</li>
          )}
        </ul>
        <DialogFooter>
          <Button onClick={onOpen}>Open {result.environment.name}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function AgentsSection() {
  return (
    <section className="grid max-w-3xl gap-2">
      <SectionHeader title="Work mode" icon={TerminalWindowIcon} />
      <p className="text-xs/relaxed text-muted-foreground">
        How you work with your teammates' agents by default: chatting, or in the
        agent's own terminal app. The other way stays one click away on each
        sandbox.
      </p>
      <AgentViewToggle />
    </section>
  )
}

function AutostartSection() {
  const queryClient = useQueryClient()
  const status = $api.useQuery("get", "/api/system/autostart")
  const set = $api.useMutation("put", "/api/system/autostart", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/autostart"], data),
    onError: (err) =>
      toast.error("Could not change autostart", {
        description: errorMessage(err),
      }),
  })
  const st = status.data
  return (
    <section className="grid max-w-3xl gap-2 text-xs">
      <SectionHeader title="Start at login" icon={PowerIcon} />
      <p className="text-xs/relaxed text-muted-foreground">
        Starts Studio when you log in, using a user-level systemd unit,
        LaunchAgent or Run registry entry. It takes effect at your next login.
      </p>
      {st && !st.supported ? (
        <p className="text-muted-foreground">Not supported on this platform.</p>
      ) : (
        <label className="flex items-center gap-2">
          <Checkbox
            checked={!!st?.enabled}
            disabled={!st || set.isPending}
            onCheckedChange={(enabled) => set.mutate({ body: { enabled } })}
          />
          Start Sandbox Studio at login
        </label>
      )}
      {st?.enabled && st.path && (
        <p className="text-muted-foreground">
          Entry: <Ident>{st.path}</Ident>
        </p>
      )}
      {st?.stale && (
        <p className="flex items-center gap-2">
          The entry starts a different Studio binary.
          <Button
            size="sm"
            variant="outline"
            onClick={() => set.mutate({ body: { enabled: true } })}
          >
            Update it
          </Button>
        </p>
      )}
    </section>
  )
}

function UpdatesSection() {
  const queryClient = useQueryClient()
  const state = $api.useQuery("get", "/api/system/updates")
  const set = $api.useMutation("put", "/api/system/updates", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/updates"], data),
    onError: (err) =>
      toast.error("Could not change the update check", {
        description: errorMessage(err),
      }),
  })
  const st = state.data
  return (
    <section className="grid max-w-3xl gap-2 text-xs">
      <SectionHeader title="Updates" icon={ArrowCircleUpIcon} />
      <p className="text-xs/relaxed text-muted-foreground">
        Checks GitHub for a newer release at most once a day. Studio never
        downloads or installs anything by itself.
      </p>
      <label className="flex items-center gap-2">
        <Checkbox
          checked={!!st?.enabled}
          disabled={!st || set.isPending}
          onCheckedChange={(enabled) => set.mutate({ body: { enabled } })}
        />
        Check for updates
      </label>
      {st && (
        <p className="text-muted-foreground">
          Running {st.current}
          {st.enabled && st.checkedAt && (
            <> · checked {formatAge(st.checkedAt)}</>
          )}
        </p>
      )}
      {st?.enabled && st.error && (
        <p className="text-destructive">Last check failed: {st.error}</p>
      )}
      {st?.available && (
        <p className="flex items-center gap-2">
          <Badge>Update available</Badge>
          {st.url ? (
            <a
              href={st.url}
              target="_blank"
              rel="noreferrer"
              className="underline underline-offset-4"
            >
              {st.latest}
            </a>
          ) : (
            st.latest
          )}
        </p>
      )}
    </section>
  )
}

/** The way back to the guided setup once Home's card for it is gone (dismissed or complete). */
function SetupSection() {
  const { env } = Route.useParams()
  const { setup } = useSetup(env)
  if (!setup || !setupOver(setup)) return null
  return (
    <section className="grid max-w-3xl gap-2 text-xs">
      <SectionHeader title="Guided setup" icon={ListChecksIcon} />
      <p className="text-xs/relaxed text-muted-foreground">
        The setup card on Home is hidden. The guided setup walks through the
        same steps whenever you open it.
      </p>
      <p>
        <Button size="sm" variant="outline" render={<Link to="/setup" />}>
          Open setup
        </Button>
      </p>
    </section>
  )
}
