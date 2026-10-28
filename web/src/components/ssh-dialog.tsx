import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { ArrowSquareOutIcon, CopyIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Ident } from "@/components/ident"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

/** SSH access, and its switch, shared by Settings and the SSH dialog. */
export function useSSHAccess() {
  const queryClient = useQueryClient()
  const status = $api.useQuery("get", "/api/system/ssh")
  const set = $api.useMutation("put", "/api/system/ssh", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/ssh"], data),
    onError: (err) =>
      toast.error("Could not change SSH access", {
        description: errorMessage(err),
        duration: 20_000,
      }),
  })
  return { status, set }
}

/**
 * How to reach a sandbox from an SSH client: its host in Studio's SSH config, the commands
 * and the editors that open it. With SSH access off, it offers to turn it on.
 */
export function SSHDialog({
  sandbox: sb,
  open,
  onOpenChange,
}: {
  sandbox: Sandbox
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { status, set } = useSSHAccess()
  const st = status.data
  const alias =
    st?.hosts?.find((h) => h.sandboxId === sb.id)?.alias ?? `${sb.id}.studio`
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Connect to {sb.name} over SSH</DialogTitle>
          <DialogDescription>
            Any SSH client reaches this computer as{" "}
            <code className="font-mono">{alias}</code>: ssh, scp and rsync, and
            editors that work over SSH, such as VS Code, Cursor, Zed and
            JetBrains Gateway. Studio starts or resumes it when you connect.
          </DialogDescription>
        </DialogHeader>
        {!st ? (
          <Spinner />
        ) : !st.enabled ? (
          <section
            aria-label="SSH access is off"
            className="grid gap-2 border p-3 text-xs"
          >
            <p>
              SSH access is off. Turning it on adds one{" "}
              <code className="font-mono">Include</code> line at the top of{" "}
              <Ident>{st.userConfig}</Ident>, for Studio&apos;s config with a
              host per computer. Settings → General turns it off again.
            </p>
            <Button
              size="sm"
              className="justify-self-start"
              disabled={set.isPending}
              onClick={() => set.mutate({ body: { enabled: true } })}
            >
              {set.isPending && <Spinner />}
              Turn on SSH access
            </Button>
          </section>
        ) : (
          <div className="grid gap-3">
            <CommandRow label="Shell" command={`ssh ${alias}`} />
            <CommandRow
              label="Login shell"
              command={`ssh -t ${alias} bash -l`}
              note="With the terminal's environment, such as the CA variables; plain ssh runs /bin/sh."
            />
            <CommandRow
              label="Copy files"
              command={`scp -r ./src ${alias}:/workspace/`}
            />
            <CommandRow label="Zed" command={`zed ssh://${alias}/workspace`} />
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="outline"
                render={
                  <a
                    href={`vscode://vscode-remote/ssh-remote+${alias}/workspace`}
                  />
                }
              >
                <ArrowSquareOutIcon />
                Open in VS Code
              </Button>
              <Button
                size="sm"
                variant="outline"
                render={
                  <a
                    href={`cursor://vscode-remote/ssh-remote+${alias}/workspace`}
                  />
                }
              >
                <ArrowSquareOutIcon />
                Open in Cursor
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              An editor that installs its server in the computer, as VS Code
              does, downloads it through the network rules: allow the request if
              Studio asks.
            </p>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CommandRow({
  label,
  command,
  note,
}: {
  label: string
  command: string
  note?: string
}) {
  return (
    <div className="grid gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <div className="flex min-w-0 items-center gap-2 border bg-muted/40 px-2 py-1">
        <code className="min-w-0 flex-1 truncate font-mono text-xs select-all">
          {command}
        </code>
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label={`Copy ${label.toLowerCase()} command`}
          onClick={() => {
            void navigator.clipboard.writeText(command)
            toast.success("Copied")
          }}
        >
          <CopyIcon />
        </Button>
      </div>
      {note && (
        <span className="text-caption text-muted-foreground">{note}</span>
      )}
    </div>
  )
}
