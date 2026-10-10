import { DownloadSimpleIcon } from "@phosphor-icons/react"
import { Button, buttonVariants } from "@/components/ui/button"
import type { Sandbox } from "@/lib/api/client"

/**
 * Downloads the sandbox as a .studio-sandbox file. The browser streams the file to disk;
 * a running sandbox is exported live, a stopped one is started briefly without services.
 */
export function ExportSandboxButton({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const exportable =
    sandbox.status === "running" || sandbox.status === "stopped"
  const title =
    "Download the template, resources and /workspace. Secrets, checkpoints and Docker state are not included." +
    (sandbox.status === "running"
      ? " Files written during the export may be inconsistent."
      : "")
  if (!exportable) {
    return (
      <Button variant="ghost" size="sm" disabled>
        <DownloadSimpleIcon />
        Export
      </Button>
    )
  }
  const href = `/api/environments/${encodeURIComponent(env)}/sandboxes/${encodeURIComponent(sandbox.id)}/export`
  return (
    <a
      className={buttonVariants({ variant: "ghost", size: "sm" })}
      href={href}
      download={`${sandbox.name}.studio-sandbox`}
      title={title}
    >
      <DownloadSimpleIcon />
      Export
    </a>
  )
}
