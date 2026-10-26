import { useRef, useState } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowCircleUpIcon,
  BellIcon,
  BroadcastIcon,
  DownloadSimpleIcon,
  ListChecksIcon,
  PaletteIcon,
  PowerIcon,
  TerminalWindowIcon,
  UploadSimpleIcon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { SettingsLink } from "@/components/settings/link"
import { SettingsPage } from "@/components/settings/page"
import { Spinner } from "@/components/ui/spinner"
import { Ident } from "@/components/ident"
import { SettingRow, SettingsGroup } from "@/components/setting-row"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useNotificationPermission } from "@/lib/notifications"
import { usePreviewSetting } from "@/lib/preview-setting"
import { setupOver, useSetup } from "@/lib/setup"
import { setStyleName, useStyleName, type StyleName } from "@/lib/theme"
import {
  $api,
  errorMessage,
  fetchClient,
  type ImportedEnvironment,
  type InstallProgress,
  type UpdateMode,
  type UpdateState,
} from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

// Most settings apply to the whole installation; the environment file is this environment's.
export function GeneralSettings({ env }: { env: string }) {
  return (
    <SettingsPage title="General">
      <SettingsGroup label="Studio">
        <AutostartSection />
        <NotificationsSection />
        <PreviewsSection />
        <TerminalShellSection />
        <SetupSection env={env} />
      </SettingsGroup>
      <SettingsGroup label="This environment">
        <EnvironmentFilesSection env={env} />
      </SettingsGroup>
      <SettingsGroup label="This browser">
        <StyleSection />
      </SettingsGroup>
    </SettingsPage>
  )
}

/** Studio's version and how it updates, for the whole installation. */
export function UpdatesSettings() {
  return (
    <SettingsPage title="Updates">
      <SettingsGroup label="Studio">
        <UpdatesSection />
      </SettingsGroup>
    </SettingsPage>
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
    <SettingRow
      icon={DownloadSimpleIcon}
      title="Environment file"
      description="A file of this environment's providers, teammates, rules, secret names and template specs, without keys, secret values, sandboxes, memory or approvals. Read it before you share it: template setup and soul text are included."
      control={
        <>
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
        </>
      }
    >
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
    </SettingRow>
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
              <SettingsLink
                section="providers"
                className="underline underline-offset-4"
              >
                Add the API key of {item.name}
              </SettingsLink>
            </li>
          ))}
          {secrets.map((item) => (
            <li key={`secret-${item.name}-${item.teammate ?? ""}`}>
              <Link
                to="/e/$env/settings/secrets"
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
              <SettingsLink
                section="templates"
                className="underline underline-offset-4"
              >
                Build {templates.length} template
                {templates.length === 1 ? "" : "s"}
              </SettingsLink>
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

/** The browser's permission to notify about approvals while Studio's window is not in front. */
function NotificationsSection() {
  const { permission, request } = useNotificationPermission()
  return (
    <SettingRow
      icon={BellIcon}
      title="Notifications"
      description="Tells you when an approval waits while Studio's window is not in front. This browser keeps the choice for this address."
      control={
        permission === "default" ? (
          <Button size="sm" variant="outline" onClick={request}>
            Enable notifications
          </Button>
        ) : (
          <span className="text-xs text-muted-foreground">
            {permission === "granted"
              ? "On"
              : permission === "denied"
                ? "Blocked"
                : "Not supported in this browser"}
          </span>
        )
      }
    >
      {permission === "denied" && (
        <p className="text-muted-foreground">
          The browser blocks them for this site. Allow notifications in its site
          settings, next to the address bar.
        </p>
      )}
    </SettingRow>
  )
}

const styles: { value: StyleName; label: string }[] = [
  { value: "pixel", label: "Pixel" },
  { value: "quiet", label: "Quiet" },
]

/** How Studio looks in this browser; lib/theme.ts stores it beside light and dark. */
function StyleSection() {
  const style = useStyleName()
  return (
    <SettingRow
      icon={PaletteIcon}
      title="Style"
      description="Pixel is square, with hard edges and titles and labels in mono. Quiet is soft and rounded, in sans. Light and dark are at the bottom of the sidebar."
      control={
        <ToggleGroup
          aria-label="Style"
          variant="outline"
          size="sm"
          value={[style]}
          onValueChange={(v) => {
            const next = styles.find((s) => s.value === v[0])
            if (next) setStyleName(next.value)
          }}
        >
          {styles.map((s) => (
            <ToggleGroupItem key={s.value} value={s.value}>
              {s.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      }
    />
  )
}

const shells = [
  { value: "bash", label: "bash" },
  { value: "zsh", label: "zsh" },
] as const

/** The shell sandboxes' new terminals start; agents' sessions run bash either way. */
function TerminalShellSection() {
  const queryClient = useQueryClient()
  const setting = $api.useQuery("get", "/api/system/terminal")
  const set = $api.useMutation("put", "/api/system/terminal", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/terminal"], data),
    onError: (err) =>
      toast.error("Could not change the terminal shell", {
        description: errorMessage(err),
      }),
  })
  const shell = set.isPending ? set.variables.body.shell : setting.data?.shell
  return (
    <SettingRow
      icon={TerminalWindowIcon}
      title="Terminal shell"
      description="The shell a new terminal starts in every sandbox. Terminals that are open keep theirs, agents run bash either way, and a sandbox whose image has no zsh starts bash."
      control={
        <ToggleGroup
          aria-label="Terminal shell"
          variant="outline"
          size="sm"
          value={shell ? [shell] : []}
          disabled={!setting.data || set.isPending}
          onValueChange={(v) => {
            const next = shells.find((s) => s.value === v[0])
            if (next && next.value !== shell)
              set.mutate({ body: { shell: next.value } })
          }}
        >
          {shells.map((s) => (
            <ToggleGroupItem key={s.value} value={s.value}>
              {s.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      }
    />
  )
}

/** Whether the pages offer previews of the ports a sandbox listens on. */
function PreviewsSection() {
  const previews = usePreviewSetting()
  return (
    <SettingRow
      icon={BroadcastIcon}
      title="Previews"
      description="Open a sandbox's ports in a tab of this browser, from a sandbox's Previews menu, the Preview tab next to a chat and the links to ports in an agent's messages; the terminal UI shows their addresses. When this is off, all of these are hidden and the ports are only listed."
      control={
        <Switch
          aria-label="Offer previews of sandbox ports"
          checked={previews.enabled}
          disabled={!previews.loaded || previews.pending}
          onCheckedChange={previews.setEnabled}
        />
      }
    />
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
    <SettingRow
      icon={PowerIcon}
      title="Start at login"
      description="Starts Studio when you log in, using a user-level systemd unit, LaunchAgent or Run registry entry. It takes effect at your next login."
      control={
        st && !st.supported ? (
          <span className="text-xs text-muted-foreground">
            Not supported on this platform
          </span>
        ) : (
          <Switch
            aria-label="Start Sandbox Studio at login"
            checked={!!st?.enabled}
            disabled={!st || set.isPending}
            onCheckedChange={(enabled) => set.mutate({ body: { enabled } })}
          />
        )
      }
    >
      {st?.enabled && st.path && (
        <p className="text-muted-foreground">
          Entry: <Ident>{st.path}</Ident>
        </p>
      )}
      {st?.stale && (
        <p className="flex items-center gap-2">
          The entry is out of date.
          <Button
            size="xs"
            variant="outline"
            onClick={() => set.mutate({ body: { enabled: true } })}
          >
            Update it
          </Button>
        </p>
      )}
    </SettingRow>
  )
}

const updateModes: Record<UpdateMode, string> = {
  off: "Off",
  tell: "Tell me",
  install: "Install automatically",
}

function UpdatesSection() {
  const queryClient = useQueryClient()
  const state = $api.useQuery("get", "/api/system/updates", undefined, {
    // An install under way moves through its phases; poll while it does.
    refetchInterval: (q) => (installing(q.state.data) ? 3000 : false),
  })
  const set = $api.useMutation("put", "/api/system/updates", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/updates"], data),
    onError: (err) =>
      toast.error("Could not change updates", {
        description: errorMessage(err),
      }),
  })
  const st = state.data
  const modes = st?.canInstall
    ? updateModes
    : { off: updateModes.off, tell: updateModes.tell }
  return (
    <SettingRow
      icon={ArrowCircleUpIcon}
      title="Updates"
      description={
        st?.mode === "install"
          ? "Checks GitHub for a newer release once a day. Studio downloads it, verifies its build attestation, and restarts into it once no chat, template build or sign-in is running. If the new version doesn't start, the old one and its database come back."
          : "Checks GitHub for a newer release once a day and tells you here. Install automatically also downloads, verifies and installs it when Studio is idle."
      }
      control={
        <Select
          value={st?.mode ?? "tell"}
          onValueChange={(v) => v && set.mutate({ body: { mode: v } })}
          items={modes}
          disabled={!st || set.isPending}
        >
          <SelectTrigger size="sm" aria-label="Updates" className="w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {Object.entries(modes).map(([v, text]) => (
              <SelectItem key={v} value={v}>
                {text}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      }
    >
      {st && (
        <p className="text-muted-foreground">
          Running <span className="font-mono">{st.current}</span>
          {st.enabled && st.checkedAt && (
            <> · checked {formatAge(st.checkedAt)}</>
          )}
        </p>
      )}
      {st && !st.canInstall && (
        <p className="text-muted-foreground">
          {st.cannotInstall ??
            "This Studio can't install releases itself, like a development build. Install them with install.sh or from the release page."}
        </p>
      )}
      {st?.enabled && st.error && (
        <p className="text-destructive">Last check failed: {st.error}</p>
      )}
      {st?.available && !installing(st) && (
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
      {st?.install && <InstallLine install={st.install} />}
    </SettingRow>
  )
}

function installing(st: UpdateState | undefined) {
  const phase = st?.install?.phase
  return (
    phase === "downloading" || phase === "waiting" || phase === "restarting"
  )
}

/** The automatic install under way, or how the last one ended. */
function InstallLine({ install }: { install: InstallProgress }) {
  const v = <span className="font-mono">{install.version}</span>
  switch (install.phase) {
    case "downloading":
      return (
        <p className="flex items-center gap-2 text-muted-foreground">
          <Spinner /> Downloading and verifying {v}…
        </p>
      )
    case "waiting":
      return (
        <p className="flex items-center gap-2 text-muted-foreground">
          <Spinner /> {v} is ready. It installs once Studio is idle
          {install.detail ? `; now ${install.detail}` : ""}.
        </p>
      )
    case "restarting":
      return (
        <p className="flex items-center gap-2 text-muted-foreground">
          <Spinner /> Restarting into {v}…
        </p>
      )
    case "installed":
      return (
        <p className="text-muted-foreground">
          Updated to {v}
          {install.from && <> from {install.from}</>} {formatAge(install.at)}.
        </p>
      )
    case "failed":
      return (
        <p className="text-destructive">
          Installing {v} failed: {install.error}.
          {install.from && <> Studio went back to {install.from}.</>}
        </p>
      )
  }
}

/** The way back to the guided setup once Home's card for it is gone (dismissed or complete). */
function SetupSection({ env }: { env: string }) {
  const { setup } = useSetup(env)
  if (!setup || !setupOver(setup)) return null
  return (
    <SettingRow
      icon={ListChecksIcon}
      title="Guided setup"
      description="The setup card on Home is hidden. The guided setup walks through the same steps whenever you open it."
      control={
        <Button size="sm" variant="outline" render={<Link to="/setup" />}>
          Open setup
        </Button>
      }
    />
  )
}
