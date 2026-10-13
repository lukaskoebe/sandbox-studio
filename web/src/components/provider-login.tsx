import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Terminal } from "@/components/terminal"
import { $api, errorMessage, type Provider } from "@/lib/api/client"

const loginPath = "/api/environments/{env}/providers/{id}/login"

// The shape of a Claude Code token, as `claude setup-token` prints it. verify (S8).
const claudeToken = /^sk-ant-oat\d{2}-[A-Za-z0-9_-]{20,}$/

/**
 * A subscription provider's sign-in. The vendor's own CLI runs in a system VM: the user opens the
 * page it names on their computer and finishes there, and the terminal shows the CLI. Studio stores
 * what the sign-in yields, so teammates' sessions only ever see a placeholder.
 */
export function ProviderLogin({
  env,
  provider,
}: {
  env: string
  provider: Provider
}) {
  const queryClient = useQueryClient()
  const claude = provider.kind === "claude_subscription"
  const vendor = claude ? "Claude" : "ChatGPT"
  const [token, setToken] = useState("")

  const status = $api.useQuery(
    "get",
    loginPath,
    { params: { path: { env, id: provider.id } } },
    {
      // The sign-in moves while it runs; the dialog checks it once a second then.
      refetchInterval: (q) => {
        const state = q.state.data?.state
        return state === "starting" || state === "waiting" || state === "saving"
          ? 1000
          : false
      },
    }
  )
  const login = status.data
  const state = login?.state ?? "idle"
  const running =
    state === "starting" || state === "waiting" || state === "saving"

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ["get", loginPath] })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/providers"],
    })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/secrets"],
    })
    queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
  }
  const failed = (what: string) => (err: unknown) =>
    toast.error(`Could not ${what}`, { description: errorMessage(err) })

  const start = $api.useMutation("post", loginPath, {
    onSettled: refresh,
    onError: failed("start the sign-in"),
  })
  const cancel = $api.useMutation("delete", loginPath, {
    onSettled: refresh,
    onError: failed("cancel the sign-in"),
  })
  const paste = $api.useMutation(
    "post",
    "/api/environments/{env}/providers/{id}/login/token",
    {
      onSuccess: () => setToken(""),
      onSettled: refresh,
      onError: failed("save the token"),
    }
  )

  const path = { params: { path: { env, id: provider.id } } }
  const begin = () => start.mutate(path)
  const pasteToken = () =>
    paste.mutate({ ...path, body: { token: token.trim() } })
  const tokenValid = claudeToken.test(token.trim())

  return (
    <div className="grid gap-3">
      {running ? null : provider.state === "ready" ? (
        <p className="text-sm">Signed in with {vendor}.</p>
      ) : provider.state === "expired" ? (
        <p className="text-sm">
          The {vendor} sign-in has expired. Sign in again to keep using it.
        </p>
      ) : (
        <p className="text-sm">
          Sign in with your {vendor} subscription to use it here.
        </p>
      )}

      {state === "starting" && (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner /> Starting the sign-in…
        </p>
      )}

      {state === "waiting" && (
        <div className="grid gap-2">
          {claude ? (
            <p className="text-sm">
              Open the page below on your computer and sign in. Then paste the
              code it shows into the terminal.
            </p>
          ) : (
            <p className="text-sm">
              Open the page below on your computer and enter the code.
            </p>
          )}
          {login?.url && (
            <a
              href={login.url}
              target="_blank"
              rel="noreferrer"
              className="text-sm break-all underline underline-offset-2"
            >
              {login.url}
            </a>
          )}
          {login?.code && (
            <p className="font-mono text-lg tracking-widest">{login.code}</p>
          )}
          <div className="h-64 overflow-hidden rounded-md ring-1 ring-foreground/10">
            <Terminal
              url={`/api/environments/${env}/providers/${provider.id}/login/terminal`}
              active
              onExit={refresh}
              className="h-full"
            />
          </div>
        </div>
      )}

      {state === "saving" && (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner /> Saving the sign-in…
        </p>
      )}

      {state === "done" && !running && (
        <p className="text-sm">The sign-in is saved.</p>
      )}

      {state === "failed" && login?.message && (
        <FieldError>{login.message}</FieldError>
      )}

      {claude && (login?.paste || state === "waiting") && (
        <Field>
          <FieldLabel htmlFor="provider-token">Or paste the token</FieldLabel>
          <div className="flex gap-2">
            <Input
              id="provider-token"
              type="password"
              className="font-mono"
              autoComplete="off"
              data-1p-ignore
              data-lpignore="true"
              spellCheck={false}
              placeholder="sk-ant-oat01-…"
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
            <Button
              type="button"
              variant="outline"
              disabled={!tokenValid || paste.isPending}
              onClick={pasteToken}
            >
              {paste.isPending && <Spinner />}
              Save token
            </Button>
          </div>
          <FieldDescription>
            The long-lived token that{" "}
            <span className="font-mono">claude setup-token</span> prints. It is
            sealed on arrival and never shown again.
          </FieldDescription>
        </Field>
      )}

      <div className="flex flex-wrap gap-2">
        {running ? (
          <Button
            type="button"
            variant="outline"
            disabled={cancel.isPending}
            onClick={() => cancel.mutate(path)}
          >
            Cancel sign-in
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            disabled={start.isPending}
            onClick={begin}
          >
            {start.isPending && <Spinner />}
            {state === "failed"
              ? "Try again"
              : provider.state === "ready" || provider.state === "expired"
                ? "Sign in again"
                : `Sign in with ${vendor}`}
          </Button>
        )}
      </div>
    </div>
  )
}
