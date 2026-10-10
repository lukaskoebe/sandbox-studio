import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"

export function isBrowserApproval(a: Approval) {
  return a.kind === "browser.action" || a.kind === "browser.credential"
}

/** One line for a browser approval: what the agent wants to do where. */
export function browserSummary(a: Approval) {
  const act = a.browserAction
  if (act) {
    const target = act.label
      ? ` “${act.label}”`
      : act.role
        ? ` ${act.role}`
        : ""
    return `${act.action}${target} on ${act.origin}`
  }
  const cred = a.browserCredential
  if (cred) return `Fill ${cred.credential} on ${cred.origin}`
  return a.subject
}

function useDecide(what: string) {
  const queryClient = useQueryClient()
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: () => {
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/approvals"],
        })
        queryClient.invalidateQueries({
          queryKey: [
            "get",
            "/api/environments/{env}/personas/{persona}/browser/patterns",
          ],
        })
      },
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error(`Could not decide the ${what}`, {
          description: errorMessage(err),
        })
      },
    }
  )
}

/**
 * Decides a browser.action approval: an agent wants to click, fill or type on an origin its
 * persona's browser has no rule for. "For this origin" stores a pattern for the action.
 */
export function BrowserActionDecision({ approval }: { approval: Approval }) {
  const act = approval.browserAction
  const [remember, setRemember] = useState(false)
  const decide = useDecide("browser action")
  const send = (action: "allow" | "deny" | "dismiss") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: {
        action,
        scope: "persona",
        remember: action !== "dismiss" && remember,
      },
    })
  return (
    <div className="grid gap-2">
      {act?.text && (
        <p className="truncate font-mono text-xs" title={act.text}>
          {act.text}
        </p>
      )}
      {act?.url && (
        <p className="truncate text-xs text-muted-foreground" title={act.url}>
          {act.url}
        </p>
      )}
      <label className="flex items-center gap-2 text-xs">
        <Checkbox checked={remember} onCheckedChange={setRemember} />
        {`Always for ${act?.action ?? "this action"} on this origin`}
      </label>
      <div className="flex items-center gap-1.5">
        <Button
          variant="ghost"
          size="xs"
          disabled={decide.isPending}
          onClick={() => send("dismiss")}
        >
          Ignore
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={decide.isPending}
          onClick={() => send("deny")}
        >
          Deny
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => send("allow")}
        >
          {decide.isPending && <Spinner />}
          Allow
        </Button>
      </div>
    </div>
  )
}

/**
 * Decides a browser.credential approval. Allowing it makes Studio fill the vault secret into
 * the field; the agent never sees the value. There is no "always": each fill is asked.
 */
export function BrowserCredentialDecision({
  approval,
}: {
  approval: Approval
}) {
  const cred = approval.browserCredential
  const decide = useDecide("credential request")
  const send = (action: "allow" | "deny") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: { action, scope: "persona" },
    })
  return (
    <div className="grid gap-2">
      <p className="text-xs text-muted-foreground">
        {`Studio fills the secret into ${cred?.label ? `“${cred.label}”` : "the field"}; ${cred?.personaName ?? "the agent"} sees only its name.`}
      </p>
      {cred?.url && (
        <p className="truncate text-xs" title={cred.url}>
          {cred.url}
        </p>
      )}
      <div className="flex items-center gap-1.5">
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={decide.isPending}
          onClick={() => send("deny")}
        >
          Deny
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => send("allow")}
        >
          {decide.isPending && <Spinner />}
          Fill
        </Button>
      </div>
    </div>
  )
}
