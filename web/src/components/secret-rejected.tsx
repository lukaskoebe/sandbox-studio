import { useNavigate } from "@tanstack/react-router"
import { useDecideApproval } from "@/components/approval-decision"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { Approval } from "@/lib/api/client"

/**
 * Answers a secret.rejected card: a host kept answering 401 to requests Studio swapped the
 * secret's value into. Update clears the card and opens the secret, or the AI provider whose
 * key it is; Not now keeps Studio from asking about the secret and host for a day.
 */
export function SecretRejectedDecision({ approval }: { approval: Approval }) {
  const r = approval.secretRejected
  const navigate = useNavigate()
  const decide = useDecideApproval()
  const env = approval.environmentId
  const send = (action: "allow" | "dismiss") =>
    decide.mutate(
      {
        params: { path: { env, id: approval.id } },
        body: { action, scope: "environment", remember: false },
      },
      {
        onSuccess: () => {
          if (action !== "allow" || !r) return
          if (r.providerId)
            void navigate({ to: "/e/$env/settings/providers", params: { env } })
          else
            void navigate({
              to: "/e/$env/settings/secrets",
              params: { env },
              search: { edit: r.secretId },
            })
        },
      }
    )
  return (
    <div className="grid gap-2">
      {r && (
        <p className="text-xs text-muted-foreground">
          <span className="font-mono break-all text-foreground">{r.host}</span>{" "}
          answered 401 to requests that carried{" "}
          {r.providerName ? (
            <>
              the key of the provider{" "}
              <span className="text-foreground">{r.providerName}</span>
            </>
          ) : (
            <>
              the secret{" "}
              <span className="font-mono text-foreground">{r.name}</span>
            </>
          )}
          . The value is probably wrong or expired; the sandbox can't tell,
          since it only sees the placeholder.
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
          disabled={decide.isPending || !r}
          onClick={() => send("allow")}
        >
          {decide.isPending && <Spinner />}
          {r?.providerId ? "Update key" : "Update secret"}
        </Button>
      </div>
    </div>
  )
}
