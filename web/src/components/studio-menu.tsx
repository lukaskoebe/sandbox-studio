import { useState } from "react"
import { useNavigate, useParams } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowClockwiseIcon,
  ArrowCircleUpIcon,
  CaretUpIcon,
  PowerIcon,
  SparkleIcon,
} from "@phosphor-icons/react"
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
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Ident } from "@/components/ident"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import { quitApp } from "@/lib/desktop"

const releases = "https://github.com/lukaskoebe/sandbox-studio/releases"

/** The release notes of version, or the list of releases for a build that isn't one. */
function releaseNotes(version?: string) {
  return version && /^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/.test(version)
    ? `${releases}/tag/${version}`
    : releases
}

/**
 * Waits for Studio to go away and answer again after a restart, for at most a minute, and
 * reports whether it came back.
 */
async function awaitRestart(): Promise<boolean> {
  let wentAway = false
  const until = Date.now() + 60_000
  while (Date.now() < until) {
    await new Promise((r) => setTimeout(r, 1000))
    let up = false
    try {
      up = (await fetchClient.GET("/api/health")).response.ok
    } catch {
      up = false
    }
    if (!up) wentAway = true
    else if (wentAway) return true
  }
  return false
}

/**
 * The Studio menu, which the version line in the sidebar's footer opens: what's app-wide rather
 * than a setting, checking for updates, the release notes, and restarting or quitting Studio.
 */
export function StudioMenu({ version }: { version?: string }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { env } = useParams({ strict: false })
  const [asking, setAsking] = useState<"restart" | "quit" | null>(null)
  const [stopped, setStopped] = useState(false)
  const showUpdates = () =>
    void navigate({
      to: "/e/$env/settings/updates",
      params: { env: env ?? "" },
    })
  const check = $api.useMutation("post", "/api/system/updates/check", {
    onSuccess: (data) => {
      queryClient.setQueryData(["get", "/api/system/updates"], data)
      if (data.error)
        toast.error("Could not check for updates", { description: data.error })
      else if (data.available)
        toast.info(`Studio ${data.latest} is out`, {
          description: `This is ${data.current}.`,
          action: { label: "Update…", onClick: showUpdates },
        })
      else
        toast.success("Studio is up to date", {
          description: data.latest
            ? `${data.latest} is the newest release.`
            : undefined,
        })
    },
    onError: (err) =>
      toast.error("Could not check for updates", {
        description: errorMessage(err),
      }),
  })
  const restart = $api.useMutation("post", "/api/system/restart", {
    onSuccess: async () => {
      setAsking(null)
      const id = toast.loading("Restarting Studio…")
      if (await awaitRestart()) {
        toast.success("Studio restarted", { id })
        void queryClient.invalidateQueries()
      } else
        toast.error("Studio hasn't come back", {
          id,
          description: "Start it again with studio in a terminal.",
        })
    },
    onError: (err) =>
      toast.error("Could not restart Studio", {
        description: errorMessage(err),
      }),
  })
  const quit = $api.useMutation("post", "/api/system/shutdown", {
    onSuccess: () => {
      setAsking(null)
      setStopped(true)
      quitApp()
    },
    onError: (err) =>
      toast.error("Could not quit Studio", { description: errorMessage(err) }),
  })

  if (stopped) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-background p-6 text-center">
        <div className="max-w-sm space-y-2">
          <h1 className="font-heading text-base font-medium">
            Studio has stopped
          </h1>
          <p className="text-sm text-muted-foreground">
            The sandboxes keep running. To start it again, run{" "}
            <Ident>studio</Ident> or <Ident>studio app</Ident>.
          </p>
        </div>
      </div>
    )
  }
  const pending = restart.isPending || quit.isPending
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="xs"
              className="min-w-0 justify-start gap-1 px-1 text-meta font-normal text-muted-foreground"
            />
          }
        >
          <span className="truncate">
            Sandbox Studio <span className="font-mono">{version}</span>
          </span>
          <CaretUpIcon className="size-3 shrink-0" />
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="start" className="w-56">
          <DropdownMenuGroup>
            <DropdownMenuLabel>
              Sandbox Studio <span className="font-mono">{version}</span>
            </DropdownMenuLabel>
            <DropdownMenuItem
              closeOnClick={false}
              disabled={check.isPending}
              onClick={() => check.mutate({})}
            >
              <ArrowCircleUpIcon />
              {check.isPending ? "Checking…" : "Check for updates"}
            </DropdownMenuItem>
            <DropdownMenuItem
              render={
                <a
                  href={releaseNotes(version)}
                  target="_blank"
                  rel="noreferrer"
                />
              }
            >
              <SparkleIcon />
              What's new
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setAsking("restart")}>
              <ArrowClockwiseIcon />
              Restart Studio…
            </DropdownMenuItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => setAsking("quit")}>
            <PowerIcon />
            Quit Studio…
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Dialog
        open={asking !== null}
        onOpenChange={(open) => !open && setAsking(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {asking === "restart" ? "Restart Studio?" : "Quit Studio?"}
            </DialogTitle>
            <DialogDescription>
              {asking === "restart" ? (
                <>
                  Chats in the middle of a turn stop. The sandboxes keep
                  running, and this page reconnects once Studio is back.
                </>
              ) : (
                <>
                  This page and every other one stop working until you start it
                  again with <Ident>studio</Ident>. The sandboxes keep running.
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAsking(null)}>
              Cancel
            </Button>
            <Button
              disabled={pending}
              onClick={() =>
                asking === "restart" ? restart.mutate({}) : quit.mutate({})
              }
            >
              {asking === "restart" ? "Restart" : "Quit"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
