import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
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
            "/api/environments/{env}/teammates/{teammate}/browser/patterns",
          ],
        })
        // A handback changes who drives.
        queryClient.invalidateQueries({
          queryKey: [
            "get",
            "/api/environments/{env}/teammates/{teammate}/browser",
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
 * teammate's browser has no rule for. "For this origin" stores a pattern for the action.
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
        scope: "teammate",
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
        <p
          className="truncate font-mono text-xs text-muted-foreground"
          title={act.url}
        >
          {act.url}
        </p>
      )}
      {act?.session && (
        <p className="text-xs text-muted-foreground">
          Ephemeral session {act.session}
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
  const send = (action: "allow" | "deny" | "dismiss") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: { action, scope: "teammate" },
    })
  const busy = (action: "allow" | "deny" | "dismiss") =>
    decide.isPending && decide.variables.body.action === action
  return (
    <div className="grid gap-2">
      <p className="text-xs text-muted-foreground">
        {`Studio fills the secret into ${cred?.label ? `“${cred.label}”` : "the field"}; ${cred?.teammateName ?? "the agent"} sees only its name.`}
      </p>
      {cred?.url && (
        <p className="truncate font-mono text-xs" title={cred.url}>
          {cred.url}
        </p>
      )}
      {cred?.session && (
        <p className="text-xs text-muted-foreground">
          Ephemeral session {cred.session}
        </p>
      )}
      <div className="flex items-center gap-1.5">
        <Button
          variant="ghost"
          size="xs"
          disabled={decide.isPending}
          onClick={() => send("dismiss")}
        >
          {busy("dismiss") && <Spinner className="size-3" />}
          Ignore
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={decide.isPending}
          onClick={() => send("deny")}
        >
          {busy("deny") && <Spinner />}
          Deny
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => send("allow")}
        >
          {busy("allow") && <Spinner />}
          Fill
        </Button>
      </div>
    </div>
  )
}

/**
 * Decides a browser.control approval: an agent asks the user, who drives the browser, for it
 * back. Hand back ends the user's turn; Keep driving tells the agent the user keeps it.
 */
export function BrowserControlDecision({ approval }: { approval: Approval }) {
  const ask = approval.browserControl
  const decide = useDecide("request for the browser")
  const send = (action: "allow" | "deny" | "dismiss") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: { action, scope: "teammate", remember: false },
    })
  return (
    <div className="grid gap-2">
      {ask?.reason && <p className="text-xs">{ask.reason}</p>}
      {ask?.session && (
        <p className="text-xs text-muted-foreground">
          Ephemeral session {ask.session}
        </p>
      )}
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
          Keep driving
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => send("allow")}
        >
          {decide.isPending && <Spinner />}
          Hand back
        </Button>
      </div>
    </div>
  )
}

/**
 * Answers a browser.show card: an agent opened a page in its browser for the user to see.
 * Watch opens the Browser page and clears the card; Take the wheel also hands the user
 * control. The full address shows first, so the user sees where the browser is.
 */
export function BrowserShowDecision({ approval }: { approval: Approval }) {
  const show = approval.browserShow
  const navigate = useNavigate()
  const decide = useDecide("card")
  const send = (action: "allow" | "dismiss", open: boolean) => {
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: { action, scope: "teammate", remember: false },
    })
    if (open && show)
      void navigate({
        to: "/e/$env/teammates/$id/browser",
        params: { env: approval.environmentId, id: show.teammateId },
      })
  }
  return (
    <div className="grid gap-2">
      {show && <p className="font-mono text-xs break-all">{show.url}</p>}
      {show?.note && <p className="text-xs">{show.note}</p>}
      <div className="flex items-center gap-1.5">
        <Button
          variant="ghost"
          size="xs"
          disabled={decide.isPending}
          onClick={() => send("dismiss", false)}
        >
          Ignore
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={decide.isPending || !show}
          onClick={() => send("dismiss", true)}
        >
          Watch
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending || !show}
          onClick={() => send("allow", true)}
        >
          {decide.isPending && <Spinner />}
          Take the wheel
        </Button>
      </div>
    </div>
  )
}
