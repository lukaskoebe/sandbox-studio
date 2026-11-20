import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import {
  exchangeToken,
  KeychainLockedError,
  readAuthStatus,
  retryKeychain,
  tokenFromLoginInput,
  UnreachableError,
  type AuthBootstrapResult,
  type KeychainWait,
} from "@/lib/auth"

export function AuthGate({
  bootstrap,
  children,
}: {
  bootstrap: Promise<AuthBootstrapResult>
  children: ReactNode
}) {
  const queryClient = useQueryClient()
  const expired = useRef(false)
  // Whether the last check found Studio down, so the next answer clears that error.
  const unreachable = useRef(false)
  const [authenticated, setAuthenticated] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [value, setValue] = useState("")
  const [error, setError] = useState<string | null>(null)
  // Studio runs but waits for the OS keychain, which holds its secrets' key, to unlock.
  const [keychain, setKeychain] = useState<KeychainWait | null>(null)

  useEffect(() => {
    let mounted = true
    bootstrap.then((result) => {
      if (!mounted || expired.current) return
      unreachable.current = !!result.unreachable
      setAuthenticated(result.authenticated)
      setKeychain(result.keychain ?? null)
      setError(
        result.authenticated || result.keychain ? null : (result.error ?? null)
      )
      setLoading(false)
    })
    return () => {
      mounted = false
    }
  }, [bootstrap])

  useEffect(() => {
    const onExpired = () => {
      expired.current = true
      queryClient.clear()
      setAuthenticated(false)
      setLoading(false)
      setError(
        "Your Studio login expired. Connect again with a fresh launch link or token."
      )
    }
    window.addEventListener("studio-auth-expired", onExpired)
    return () => window.removeEventListener("studio-auth-expired", onExpired)
  }, [queryClient])

  // Studio restarted while this page was open and waits for the keychain: the page shows that
  // until Studio starts, then comes back with the session it has.
  useEffect(() => {
    const onLocked = () => {
      queryClient.clear()
      setAuthenticated(false)
      setLoading(false)
      readAuthStatus().then(
        (ok) => setAuthenticated(ok),
        (cause: unknown) => {
          if (cause instanceof KeychainLockedError) setKeychain(cause.wait)
        }
      )
    }
    window.addEventListener("studio-keychain-locked", onLocked)
    return () => window.removeEventListener("studio-keychain-locked", onLocked)
  }, [queryClient])

  // While the gate shows, Studio may be restarting: a page opened while it was down connects
  // by itself once it answers, with the session it already has.
  useEffect(() => {
    if (authenticated || loading) return
    const id = window.setInterval(() => {
      if (document.hidden) return
      readAuthStatus().then(
        (ok) => {
          setKeychain(null)
          if (ok) setAuthenticated(true)
          else if (unreachable.current) setError(null)
          unreachable.current = false
        },
        (cause: unknown) => {
          if (cause instanceof KeychainLockedError) setKeychain(cause.wait)
          else if (cause instanceof UnreachableError) unreachable.current = true
        }
      )
    }, 3000)
    return () => window.clearInterval(id)
  }, [authenticated, loading])

  async function connect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const token = tokenFromLoginInput(value)
      await exchangeToken(token)
      if (!(await readAuthStatus())) {
        throw new Error(
          "Studio did not confirm the login. Try a fresh launch link."
        )
      }
      setValue("")
      setAuthenticated(true)
    } catch (cause) {
      setError(
        cause instanceof Error
          ? cause.message
          : "Studio could not complete authentication. Check the connection and try again."
      )
    } finally {
      setBusy(false)
    }
  }

  async function retryUnlock() {
    setError(null)
    setBusy(true)
    try {
      const wait = await retryKeychain()
      if (wait) setKeychain(wait)
      else await retryStatus()
    } catch (cause) {
      setError(
        cause instanceof Error
          ? cause.message
          : "Could not reach Studio. Check the connection and try again."
      )
    } finally {
      setBusy(false)
    }
  }

  async function retryStatus() {
    setError(null)
    setBusy(true)
    try {
      if (await readAuthStatus()) {
        setValue("")
        setAuthenticated(true)
      } else {
        setError(
          "Studio is not connected yet. Paste a launch link or token to connect."
        )
      }
    } catch (cause) {
      unreachable.current = cause instanceof UnreachableError
      if (cause instanceof KeychainLockedError) {
        setKeychain(cause.wait)
        return
      }
      setError(
        cause instanceof Error
          ? cause.message
          : "Could not reach Studio. Check the connection and try again."
      )
    } finally {
      setBusy(false)
    }
  }

  if (authenticated) return children

  if (keychain)
    return (
      <main className="flex min-h-svh items-center justify-center bg-background p-6 text-foreground">
        <section
          className="w-full max-w-sm space-y-5 rounded-lg border bg-card p-6 shadow-sm"
          aria-labelledby="keychain-title"
        >
          <div className="space-y-2">
            <h1
              id="keychain-title"
              className="font-heading text-lg font-semibold"
            >
              Unlock your keychain
            </h1>
            <p className="text-sm text-muted-foreground">{keychain.message}</p>
            {keychain.error && (
              <p className="text-caption break-words text-muted-foreground">
                {keychain.error}
              </p>
            )}
          </div>
          <div className="space-y-2">
            <Button type="button" disabled={busy} onClick={retryUnlock}>
              {busy && <Spinner />}
              Retry
            </Button>
            <p className="text-caption text-muted-foreground" role="status">
              {keychain.retryIn > 0
                ? `Retry asks the keychain again in ${keychain.retryIn} s; it may show its password prompt on the desktop.`
                : "Retry asks the keychain again; it may show its password prompt on the desktop."}
            </p>
            {error && <p className="text-sm text-destructive">{error}</p>}
          </div>
        </section>
      </main>
    )

  return (
    <main className="flex min-h-svh items-center justify-center bg-background p-6 text-foreground">
      <section className="w-full max-w-sm space-y-5 rounded-lg border bg-card p-6 shadow-sm">
        <div className="space-y-2">
          <h1 className="font-heading text-lg font-semibold">
            Connect to Studio
          </h1>
          <p className="text-sm text-muted-foreground">
            Open the launch link printed by Studio, or run{" "}
            <code>studio login-url</code> on the host to get a fresh link. For
            the LAN UI, paste the full launch link here.
          </p>
        </div>

        {loading ? (
          <div
            className="flex items-center gap-2 text-sm text-muted-foreground"
            role="status"
          >
            <Spinner />
            Checking Studio login…
          </div>
        ) : (
          <form className="space-y-4" onSubmit={connect}>
            <Field>
              <FieldLabel htmlFor="studio-login">
                Launch link or token
              </FieldLabel>
              <Input
                id="studio-login"
                type="password"
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                value={value}
                onChange={(event) => setValue(event.target.value)}
                aria-invalid={Boolean(error)}
                disabled={busy}
              />
              <FieldDescription>
                Launch links are one-use and expire after 10 minutes. Paste the
                full link or just its token.
              </FieldDescription>
              {error && <FieldError>{error}</FieldError>}
            </Field>
            <div className="flex items-center justify-between gap-2">
              <Button type="submit" disabled={!value.trim() || busy}>
                {busy && <Spinner />}
                Connect
              </Button>
              <Button
                type="button"
                variant="ghost"
                disabled={busy}
                onClick={retryStatus}
              >
                Retry status
              </Button>
            </div>
          </form>
        )}
      </section>
    </main>
  )
}
