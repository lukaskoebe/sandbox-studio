import { useDecideApproval } from "@/components/approval-decision"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { Approval } from "@/lib/api/client"

/** A Go duration such as 1h0m0s, as a short word: 1h, 15m. */
function short(d: string) {
  return d.replace(/0s$/, "").replace(/h0m$/, "h") || d
}

/**
 * Answers a credential.issue card: a tool in a sandbox asks, through its credential helper, for
 * credentials made from a secret with a kind. Allow lets the sandbox have them until it stops;
 * Always lets every sandbox of the teammate (or this sandbox, if no teammate owns it) have them
 * from now on. The secret itself never reaches the sandbox either way.
 */
export function CredentialIssueDecision({ approval }: { approval: Approval }) {
  const p = approval.credentialIssue
  const decide = useDecideApproval()
  const env = approval.environmentId
  const send = (action: "allow" | "deny", remember = false) =>
    decide.mutate({
      params: { path: { env, id: approval.id } },
      body: { action, scope: "environment", remember },
    })
  return (
    <div className="grid gap-2">
      {p && (
        <p className="text-xs text-muted-foreground">
          A tool in <span className="text-foreground">{p.sandboxName}</span>{" "}
          asks for credentials made from the {p.kindTitle}{" "}
          <span className="font-mono text-foreground">{p.name}</span>. Each
          lasts {short(p.lifetime)}; the key itself stays in Studio.
        </p>
      )}
      <div className="flex flex-wrap items-center gap-1.5">
        <Button
          variant="ghost"
          size="xs"
          disabled={decide.isPending}
          onClick={() => send("deny")}
        >
          Deny
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={decide.isPending || !p}
          title={
            p?.teammateName
              ? `Every sandbox of ${p.teammateName} gets them without asking`
              : "This sandbox gets them without asking"
          }
          onClick={() => send("allow", true)}
        >
          {p?.teammateName ? `Always for ${p.teammateName}` : "Always"}
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending || !p}
          onClick={() => send("allow")}
        >
          {decide.isPending && <Spinner />}
          Allow until it stops
        </Button>
      </div>
    </div>
  )
}
