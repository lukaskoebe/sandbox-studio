import { useNavigate } from "@tanstack/react-router"
import { useDecideApproval } from "@/components/approval-decision"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { Approval } from "@/lib/api/client"

/**
 * Answers a git.credential card: git in a sandbox wanted credentials for a host no forge
 * covers. Add forge clears the card and opens the Add forge dialog filled in from the
 * repository's URL; Not now keeps Studio from asking about the host for a day.
 */
export function GitCredentialDecision({ approval }: { approval: Approval }) {
  const cred = approval.gitCredential
  const navigate = useNavigate()
  const decide = useDecideApproval()
  const send = (action: "allow" | "dismiss") =>
    decide.mutate(
      {
        params: { path: { env: approval.environmentId, id: approval.id } },
        body: { action, scope: "environment", remember: false },
      },
      {
        onSuccess: () => {
          if (action === "allow" && cred)
            void navigate({
              to: "/e/$env/settings/forges",
              params: { env: approval.environmentId },
              search: { add: cred.url },
            })
        },
      }
    )
  return (
    <div className="grid gap-2">
      {cred && (
        <p className="text-xs text-muted-foreground">
          git found no forge for{" "}
          <span className="font-mono break-all text-foreground">
            {cred.url}
          </span>{" "}
          and has no credentials for it. Add the forge, and the agent's next try
          goes through Studio.
        </p>
      )}
      <div className="flex items-center gap-1.5">
        <Button
          variant="ghost"
          size="xs"
          disabled={decide.isPending}
          onClick={() => send("dismiss")}
        >
          Not now
        </Button>
        <Button
          size="sm"
          className="ml-auto"
          disabled={decide.isPending || !cred}
          onClick={() => send("allow")}
        >
          {decide.isPending && <Spinner />}
          Add forge
        </Button>
      </div>
    </div>
  )
}
