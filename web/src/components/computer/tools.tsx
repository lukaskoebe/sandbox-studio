import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
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
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  errorStatus,
  type Sandbox,
  type SandboxHarness,
  type SandboxTool,
  type SandboxTools,
} from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"
import { cn } from "@/lib/utils"

/** How many mise tools show before "Show all". */
const folded = 8

const origins: Record<SandboxTool["origin"], { label: string; hint: string }> =
  {
    template: {
      label: "template",
      hint: "The computer's template pins it.",
    },
    added: {
      label: "added",
      hint: "Pinned in this computer, with mise use -g.",
    },
    kept: {
      label: "kept",
      hint: "Kept from before a rebase, in conf.d/studio-kept-tools.toml.",
    },
    project: {
      label: "project",
      hint: "A project's mise.toml asks for it.",
    },
    unpinned: {
      label: "unused",
      hint: "Installed, but no config asks for this version.",
    },
  }

/** Newer first: 2.1.340 before 2.1.99. */
export function compareVersions(a: string, b: string) {
  const pa = a.split(/[.-]/)
  const pb = b.split(/[.-]/)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] ?? ""
    const y = pb[i] ?? ""
    const nx = Number(x)
    const ny = Number(y)
    const d =
      x !== "" && y !== "" && !isNaN(nx) && !isNaN(ny)
        ? nx - ny
        : x.localeCompare(y)
    if (d) return d
  }
  return 0
}

/**
 * The Details tab's Tools: the harnesses' CLIs, with Claude Code's update, and what mise has,
 * each version with where it came from. The computer's guest agent says, so it has to run.
 */
export function ComputerTools({
  env,
  sandbox: sb,
}: {
  env: string
  sandbox: Sandbox
}) {
  const queryClient = useQueryClient()
  const running = sb.status === "running"
  const path = { params: { path: { env, id: sb.id } } }
  const tools = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/tools",
    path,
    { enabled: running, retry: false, staleTime: 30_000 }
  )
  // A restart may bring a newer guest agent, and the tools it had before are gone anyway.
  const { refetch } = tools
  useEffect(() => {
    if (running) void refetch()
  }, [running, sb.startedAt, refetch])
  const [asking, setAsking] = useState<SandboxHarness>()
  const [all, setAll] = useState(false)
  const update = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/harnesses/{harness}/update"
  )
  const startUpdate = (h: SandboxHarness, version: string) => {
    setAsking(undefined)
    const label = harnessLabels[h.harness]
    const id = toast.loading(`Updating ${label} on ${sb.name}…`, {
      description: "If Studio asks, allow registry.npmjs.org.",
    })
    update.mutate(
      {
        params: { path: { env, id: sb.id, harness: h.harness } },
        body: { version },
      },
      {
        onSuccess: (t) => {
          queryClient.setQueryData(
            [
              "get",
              "/api/environments/{env}/sandboxes/{id}/tools",
              { params: { path: { env, id: sb.id } } },
            ],
            t
          )
          toast.success(`${label} ${version} on ${sb.name}`, {
            id,
            description:
              "New terminals and chats run it; running ones keep theirs until they start again.",
          })
        },
        onError: (err) =>
          toast.error(`${label} wasn't updated`, {
            id,
            description: errorMessage(err),
          }),
      }
    )
  }

  if (!running) {
    return (
      <p className="text-muted-foreground">
        Start the computer to list its tools.
      </p>
    )
  }
  if (tools.isPending) {
    return <p className="text-muted-foreground">Asking the computer…</p>
  }
  if (tools.error) {
    const msg = errorMessage(tools.error)
    return (
      <p className="text-muted-foreground">
        {/older guest agent/.test(msg)
          ? "It started before Studio was upgraded. Restart the computer to list its tools."
          : errorStatus(tools.error) === 409
            ? "It is still starting."
            : `It didn't say: ${msg}`}
      </p>
    )
  }
  const t: SandboxTools = tools.data
  const mise = [...t.mise].sort(
    (a, b) =>
      a.name.localeCompare(b.name) ||
      Number(b.active) - Number(a.active) ||
      compareVersions(b.version, a.version)
  )
  const shown = all ? mise : mise.slice(0, folded)
  return (
    <>
      <ul aria-label="Harnesses" className="grid gap-1.5">
        {t.harnesses.map((h) => {
          const label = harnessLabels[h.harness]
          const busy =
            update.isPending &&
            update.variables.params.path.harness === h.harness
          return (
            <li key={h.harness} className="flex min-h-6 items-center gap-2">
              <span className="min-w-0 flex-1 truncate">{label}</span>
              <span className="shrink-0 font-mono text-muted-foreground tabular-nums">
                {h.version}
              </span>
              {h.updatable && (
                <Button
                  size="xs"
                  variant="outline"
                  disabled={update.isPending}
                  onClick={() => setAsking(h)}
                  aria-label={`Update ${label}…`}
                >
                  {busy && <Spinner />}
                  {busy ? "Updating…" : "Update…"}
                </Button>
              )}
            </li>
          )
        })}
      </ul>
      <div className="grid gap-1.5 border-t border-border/60 pt-3">
        <h4 className="text-meta text-muted-foreground">From mise</h4>
        {t.miseError ? (
          <p className="text-muted-foreground">
            mise didn&apos;t list its tools: {t.miseError}
          </p>
        ) : mise.length === 0 ? (
          <p className="text-muted-foreground">
            None yet. <span className="font-mono">mise use -g node@24</span>{" "}
            adds one for every project.
          </p>
        ) : (
          <ul aria-label="Tools from mise" className="grid gap-1.5">
            {shown.map((m) => (
              <ToolRow key={`${m.name}@${m.version}`} tool={m} />
            ))}
          </ul>
        )}
        {mise.length > folded && (
          <Button
            size="xs"
            variant="ghost"
            className="justify-self-start"
            onClick={() => setAll(!all)}
          >
            {all ? "Show fewer" : `Show all ${mise.length}`}
          </Button>
        )}
      </div>
      {asking && (
        <UpdateHarnessDialog
          harness={asking}
          computer={sb.name}
          onUpdate={(v) => startUpdate(asking, v)}
          onCancel={() => setAsking(undefined)}
        />
      )}
    </>
  )
}

function ToolRow({ tool: m }: { tool: SandboxTool }) {
  const o = origins[m.origin]
  const hint = [
    o.hint,
    m.requested && `It asks for ${m.requested}.`,
    m.config && m.origin === "project" && m.config,
    !m.installed && "This version isn't installed.",
  ]
    .filter(Boolean)
    .join(" ")
  return (
    <li
      className={cn(
        "flex min-w-0 items-baseline gap-2",
        !m.active && "text-muted-foreground"
      )}
    >
      <span className="min-w-0 flex-1 truncate font-mono">{m.name}</span>
      <span className="shrink-0 font-mono tabular-nums">{m.version}</span>
      <span
        title={hint}
        className={cn(
          "min-w-17 shrink-0 rounded-full border px-1.5 text-center text-meta leading-relaxed text-muted-foreground",
          !m.installed &&
            "border-(--st-waiting)/45 text-(--st-waiting-fg) dark:text-(--st-waiting)"
        )}
      >
        {m.installed ? o.label : "missing"}
      </span>
    </li>
  )
}

/**
 * Asks before updating a harness in a computer, with npm's newest stable release; the update
 * itself runs once the dialog is gone, so Needs you stays in reach for npm's download.
 */
function UpdateHarnessDialog({
  harness: h,
  computer,
  onUpdate,
  onCancel,
}: {
  harness: SandboxHarness
  computer: string
  onUpdate: (version: string) => void
  onCancel: () => void
}) {
  const label = harnessLabels[h.harness]
  const release = $api.useQuery(
    "get",
    "/api/harnesses/{harness}/release",
    { params: { path: { harness: h.harness } } },
    { retry: false, staleTime: 10 * 60_000 }
  )
  const v = release.data?.version
  const cmp = v ? compareVersions(v, h.version) : 0
  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        if (!open) onCancel()
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            Update {label} on {computer}?
          </AlertDialogTitle>
          <AlertDialogDescription>
            {release.isPending ? (
              "Asking npm for the newest release…"
            ) : release.error ? (
              <>
                npm didn&apos;t say what its newest release is:{" "}
                {errorMessage(release.error)}
              </>
            ) : cmp > 0 ? (
              <>
                Installs {label} <b className="font-mono">{v}</b>, the newest
                stable release, in place of{" "}
                <span className="font-mono">{h.version}</span>.
              </>
            ) : cmp === 0 ? (
              <>
                <span className="font-mono">{h.version}</span> is the newest
                stable release already.
              </>
            ) : (
              <>
                The newest stable release is{" "}
                <span className="font-mono">{v}</span>, older than this
                computer&apos;s <span className="font-mono">{h.version}</span>.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <ul className="grid list-disc gap-1 pl-5 text-sm text-muted-foreground">
          <li>
            The computer downloads it from{" "}
            <span className="font-mono">registry.npmjs.org</span>: if Studio
            asks, allow it.
          </li>
          <li>
            Terminals and chats already running {label} keep their version until
            they start again.
          </li>
          <li>
            A rebase or a disk resize brings back the base image&apos;s version.
          </li>
        </ul>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={!v || cmp === 0}
            onClick={() => v && onUpdate(v)}
          >
            {cmp < 0 && v ? `Install ${v}` : "Update"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
