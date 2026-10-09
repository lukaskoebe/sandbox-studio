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
  readAuthStatus,
  tokenFromLoginInput,
  type AuthBootstrapResult,
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
  const [authenticated, setAuthenticated] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [value, setValue] = useState("")
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true
    bootstrap.then((result) => {
      if (!mounted || expired.current) return
      setAuthenticated(result.authenticated)
      setError(result.authenticated ? null : (result.error ?? null))
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

  return (
    <main className="flex min-h-svh items-center justify-center bg-background p-6 text-foreground">
      <section className="w-full max-w-sm space-y-5 rounded-lg border bg-card p-6 shadow-sm">
        <div className="space-y-2">
          <h1 className="text-lg font-semibold">Connect to Studio</h1>
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
