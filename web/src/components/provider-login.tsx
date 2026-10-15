import { useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  CaretRightIcon,
  ClipboardTextIcon,
  CopyIcon,
} from "@phosphor-icons/react"
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
import { $api, errorMessage, type Login, type Provider } from "@/lib/api/client"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"

const loginPath = "/api/environments/{env}/providers/{id}/login"

// The shape of a Claude Code token, as `claude setup-token` prints it. verify (S8).
const claudeToken = /^sk-ant-oat\d{2}-[A-Za-z0-9_-]{20,}$/

/**
 * A subscription provider's sign-in. The vendor's own CLI runs in a small system VM, and the
 * user finishes in their own browser. For Claude, the page then shows a code: the user pastes it
 * here, and Studio types it into the CLI, so nobody has to paste into a terminal. The terminal
 * stays one click away, to see what the CLI says. Studio stores what the sign-in yields, so
 * teammates' sessions only ever see a placeholder.
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
  const plans = claude ? "Pro or Max" : "Plus or Pro"
  // Unset until the user toggles it: then the terminal opens by itself when a code is rejected.
  const [showTerminal, setShowTerminal] = useState<boolean>()

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
  const terminalOpen = showTerminal ?? !!login?.codeRejected

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

  const path = { params: { path: { env, id: provider.id } } }

  return (
    <div className="grid gap-3">
      {running ? null : provider.state === "ready" ? (
        <p className="text-xs/relaxed">
          Signed in with {vendor}. Sign in again to use another account.
        </p>
      ) : provider.state === "expired" ? (
        <p className="text-xs/relaxed">
          The {vendor} sign-in has expired. Sign in again to keep using it.
        </p>
      ) : (
        <p className="text-xs/relaxed text-muted-foreground">
          Sign in with the {vendor} account of your {plans} plan, in your
          browser. There is no API key to paste: Studio runs {vendor}'s own
          sign-in in a small VM and keeps what it yields.
        </p>
      )}

      {state === "starting" && (
        <p className="flex items-center gap-2 text-xs text-muted-foreground">
          <Spinner /> Starting the sign-in. This takes a few seconds.
        </p>
      )}

      {state === "waiting" &&
        (claude ? (
          <ClaudeSteps env={env} provider={provider} login={login} />
        ) : (
          <ChatGPTSteps login={login} />
        ))}

      {state === "waiting" && (
        <div className="grid gap-2">
          <button
            type="button"
            className="flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            aria-expanded={terminalOpen}
            onClick={() => setShowTerminal(!terminalOpen)}
          >
            <CaretRightIcon
              className={cn(
                "size-3 transition-transform",
                terminalOpen && "rotate-90"
              )}
            />
            {terminalOpen
              ? "Hide the terminal"
              : `Show the terminal ${claude ? "Claude Code" : "Codex"} runs in`}
          </button>
          {terminalOpen && (
            <>
              <p className="text-xs text-muted-foreground">
                {claude
                  ? "Claude Code runs here. You don't need to type in it: the code you paste above is typed in for you."
                  : "Codex runs here. You don't need to type in it."}
              </p>
              <div className="h-56 overflow-hidden rounded-md ring-1 ring-foreground/10">
                <Terminal
                  url={`/api/environments/${env}/providers/${provider.id}/login/terminal`}
                  active
                  onExit={refresh}
                  className="h-full"
                />
              </div>
            </>
          )}
        </div>
      )}

      {state === "saving" && (
        <p className="flex items-center gap-2 text-xs text-muted-foreground">
          <Spinner /> Saving the sign-in…
        </p>
      )}

      {state === "done" && !running && (
        <p className="text-xs">The sign-in is saved.</p>
      )}

      {state === "failed" && login?.message && (
        <FieldError>{login.message}</FieldError>
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
            variant={provider.state === "ready" ? "outline" : "default"}
            disabled={start.isPending}
            onClick={() => start.mutate(path)}
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

      {claude && !running && (
        <TokenFallback
          env={env}
          provider={provider}
          open={!!login?.paste}
          onSaved={refresh}
        />
      )}
    </div>
  )
}

/** One numbered step of a sign-in. */
function Step({
  n,
  title,
  children,
}: {
  n: number
  title: string
  children?: ReactNode
}) {
  return (
    <li className="grid grid-cols-[1.25rem_1fr] gap-x-2 gap-y-1">
      <span
        className="mt-px inline-flex size-5 items-center justify-center rounded-full bg-muted font-mono text-[0.6875rem] text-muted-foreground"
        aria-hidden
      >
        {n}
      </span>
      <span className="self-center text-xs font-medium">{title}</span>
      {children && (
        <div className="col-start-2 grid gap-2 text-xs/relaxed text-muted-foreground">
          {children}
        </div>
      )}
    </li>
  )
}

/** The page the sign-in waits on: a button that opens it, and one that copies its link. */
function OpenPage({ url }: { url?: string }) {
  if (!url)
    return (
      <p className="flex items-center gap-2">
        <Spinner /> Waiting for the link…
      </p>
    )
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Button
        size="sm"
        nativeButton={false}
        render={<a href={url} target="_blank" rel="noreferrer" />}
      >
        Open the sign-in page
        <ArrowSquareOutIcon data-icon="inline-end" />
      </Button>
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() => copy(url, "Link copied")}
      >
        <CopyIcon />
        Copy link
      </Button>
    </div>
  )
}

async function copy(text: string, done: string) {
  try {
    await copyText(text)
    toast.success(done)
  } catch {
    toast.error("Could not copy")
  }
}

/**
 * Claude's sign-in: open the page, sign in, copy the code it shows, and paste it here. Studio
 * types the code into Claude Code, which then prints the token Studio stores.
 */
function ClaudeSteps({
  env,
  provider,
  login,
}: {
  env: string
  provider: Provider
  login?: Login
}) {
  const queryClient = useQueryClient()
  const [code, setCode] = useState("")
  const [error, setError] = useState<string>()
  const send = $api.useMutation(
    "post",
    "/api/environments/{env}/providers/{id}/login/code",
    {
      onSuccess: () => {
        setCode("")
        setError(undefined)
      },
      onError: (err) => setError(errorMessage(err)),
      onSettled: () =>
        queryClient.invalidateQueries({ queryKey: ["get", loginPath] }),
    }
  )
  const submit = (text: string) => {
    const value = text.trim()
    if (!value || send.isPending) return
    setError(undefined)
    send.mutate({
      params: { path: { env, id: provider.id } },
      body: { code: value },
    })
  }
  const paste = async () => {
    try {
      const text = await navigator.clipboard.readText()
      if (!text.trim()) {
        setError("The clipboard is empty. Copy the code on the sign-in page.")
        return
      }
      setCode(text.trim())
      submit(text)
    } catch {
      setError(
        "Studio may not read the clipboard here. Click the field and press Ctrl+V (⌘V on a Mac)."
      )
    }
  }
  const sent = login?.codeSent && !login.codeRejected

  return (
    <ol className="grid gap-3" aria-label="Sign-in steps">
      <Step n={1} title="Open the sign-in page">
        <OpenPage url={login?.url} />
      </Step>
      <Step n={2} title="Sign in and authorize">
        <p>
          Sign in with the Claude account that has your Pro or Max plan, then
          choose Authorize.
        </p>
      </Step>
      <Step n={3} title="Copy the code">
        <p>The page then shows a code. Copy it with the page's Copy button.</p>
      </Step>
      <Step n={4} title="Paste the code here">
        {/* Not a form: the sign-in sits inside the provider dialog's form. */}
        <div className="grid gap-1.5">
          <FieldLabel htmlFor="login-code" className="sr-only">
            Code from the sign-in page
          </FieldLabel>
          <div className="flex gap-1.5">
            <Input
              id="login-code"
              className="font-mono"
              autoComplete="off"
              data-1p-ignore
              data-lpignore="true"
              spellCheck={false}
              placeholder="Paste the code"
              value={code}
              aria-invalid={!!error || !!login?.codeRejected}
              onChange={(e) => setCode(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== "Enter") return
                e.preventDefault()
                submit(code)
              }}
            />
            <Button
              type="button"
              variant="outline"
              title="Paste the code from the clipboard and send it"
              disabled={send.isPending}
              onClick={paste}
            >
              <ClipboardTextIcon />
              Paste
            </Button>
            <Button
              type="button"
              disabled={!code.trim() || send.isPending}
              onClick={() => submit(code)}
            >
              {send.isPending && <Spinner />}
              Submit
            </Button>
          </div>
          {error ? (
            <FieldError>{error}</FieldError>
          ) : login?.codeRejected ? (
            <FieldError>
              Claude Code didn't accept the code; the terminal below says why.
              Check that you copied the whole code and paste it again, or cancel
              and sign in again for a new one.
            </FieldError>
          ) : sent ? (
            <p className="flex items-center gap-2">
              <Spinner /> Code sent. Waiting for Claude Code to finish…
            </p>
          ) : (
            <p>
              Studio types it into Claude Code for you and saves the sign-in by
              itself.
            </p>
          )}
        </div>
      </Step>
    </ol>
  )
}

/** ChatGPT's device sign-in: open the page and enter the code Studio shows there. */
function ChatGPTSteps({ login }: { login?: Login }) {
  return (
    <ol className="grid gap-3" aria-label="Sign-in steps">
      <Step n={1} title="Open the sign-in page">
        <OpenPage url={login?.url} />
      </Step>
      <Step n={2} title="Enter this code on the page">
        {login?.code ? (
          <div className="flex items-center gap-2">
            <span className="font-mono text-lg tracking-widest text-foreground">
              {login.code}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              title="Copy code"
              aria-label="Copy code"
              onClick={() => copy(login.code!, "Code copied")}
            >
              <CopyIcon />
            </Button>
          </div>
        ) : (
          <p className="flex items-center gap-2">
            <Spinner /> Waiting for the code…
          </p>
        )}
      </Step>
      <Step n={3} title="Sign in with your ChatGPT account">
        <p>Studio notices when you're done. There is nothing to paste back.</p>
      </Step>
    </ol>
  )
}

/**
 * The other way in for Claude: a long-lived token from `claude setup-token` run on the user's
 * own computer. It opens by itself when Studio couldn't read the token in the sign-in VM.
 */
function TokenFallback({
  env,
  provider,
  open: suggested,
  onSaved,
}: {
  env: string
  provider: Provider
  open: boolean
  onSaved: () => void
}) {
  const [open, setOpen] = useState(false)
  const [token, setToken] = useState("")
  const paste = $api.useMutation(
    "post",
    "/api/environments/{env}/providers/{id}/login/token",
    {
      onSuccess: () => {
        setToken("")
        toast.success("Token saved")
      },
      onSettled: onSaved,
      onError: (err) =>
        toast.error("Could not save the token", {
          description: errorMessage(err),
        }),
    }
  )
  const valid = claudeToken.test(token.trim())
  const shown = open || suggested

  return (
    <div className="grid gap-2 border-t pt-3">
      <button
        type="button"
        className="flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
        aria-expanded={shown}
        onClick={() => setOpen(!shown)}
      >
        <CaretRightIcon
          className={cn("size-3 transition-transform", shown && "rotate-90")}
        />
        Use a token from claude setup-token instead
      </button>
      {shown && (
        <Field>
          <FieldLabel htmlFor="provider-token">Claude Code token</FieldLabel>
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
              disabled={!valid || paste.isPending}
              onClick={() =>
                paste.mutate({
                  params: { path: { env, id: provider.id } },
                  body: { token: token.trim() },
                })
              }
            >
              {paste.isPending && <Spinner />}
              Save token
            </Button>
          </div>
          <FieldDescription>
            Run <span className="font-mono">claude setup-token</span> on your
            computer and paste the token it prints at the end. It starts with{" "}
            <span className="font-mono">sk-ant-oat</span>, and is sealed on
            arrival. This is not the code from the sign-in page.
          </FieldDescription>
        </Field>
      )}
    </div>
  )
}
