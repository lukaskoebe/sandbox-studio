import { useEffect, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CopyIcon, KeyIcon, LockKeyIcon, XIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Ident } from "@/components/ident"
import { DeleteSecret } from "@/components/secret-parts"
import { $api, errorMessage, fetchClient, type Secret } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import {
  loginName,
  loginPassword,
  loginsFor,
  setNeverOffered,
  type TypedPassword,
} from "@/lib/browser-logins"
import { copyText } from "@/lib/clipboard"

type SignIn = components["schemas"]["SignIn"]

const secretsPath = "/api/environments/{env}/secrets"

function useSecrets(env: string) {
  return $api.useQuery("get", secretsPath, { params: { path: { env } } })
}

function useInvalidateSecrets() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: ["get", secretsPath] })
}

/** Saves a new browser login, or a new password and username for one. */
async function saveLogin(
  env: string,
  teammate: string,
  host: string,
  login: Secret | undefined,
  fields: {
    name: string
    username: string
    password: string
    agentMayUse?: boolean
  }
) {
  if (login) {
    const { error } = await fetchClient.PUT(`${secretsPath}/{id}`, {
      params: { path: { env, id: login.id } },
      body: {
        hosts: login.hosts ?? [host],
        note: login.note,
        value: fields.password,
        username: fields.username,
        agentMayUse: fields.agentMayUse,
      },
    })
    if (error) throw error
    return
  }
  const { error } = await fetchClient.POST(secretsPath, {
    params: { path: { env } },
    body: {
      name: fields.name,
      value: fields.password,
      hosts: [host],
      teammateId: teammate,
      browserOnly: true,
      username: fields.username || undefined,
      agentMayUse: fields.agentMayUse,
      note: `Saved from ${host} in the browser`,
    },
  })
  if (error) throw error
}

/**
 * Offers to save a password the user typed into a page of the teammate's browser as one of its
 * browser logins, or to update the login it has for the site. A password a login already holds
 * is not offered.
 */
export function SaveLoginCard({
  env,
  teammate,
  name,
  typed,
  onDone,
}: {
  env: string
  teammate: string
  name: string
  typed: TypedPassword
  onDone: () => void
}) {
  const secrets = useSecrets(env)
  const invalidate = useInvalidateSecrets()
  const logins = loginsFor(secrets.data ?? [], teammate, typed.host)
  const login = logins.at(0)
  // Whether a login of the site already holds the password, which is then not offered.
  const [known, setKnown] = useState<boolean>()
  const loaded = !!secrets.data
  const ids = logins.map((l) => l.id).join(",")
  useEffect(() => {
    if (!loaded) return
    let stale = false
    void Promise.all(
      ids
        .split(",")
        .filter(Boolean)
        .map((id) => loginPassword(env, id).catch(() => undefined))
    ).then((values) => {
      if (!stale) setKnown(values.includes(typed.password))
    })
    return () => {
      stale = true
    }
  }, [env, ids, typed.password, loaded])
  useEffect(() => {
    if (known) onDone()
  }, [known, onDone])

  const [loginNameValue, setLoginName] = useState<string>()
  const [username, setUsername] = useState<string>()
  const [agentMayUse, setAgentMayUse] = useState(false)
  const [saving, setSaving] = useState(false)
  if (known !== false) return null
  const suggested = loginName(
    typed.host,
    (secrets.data ?? []).map((s) => s.name)
  )
  const save = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      await saveLogin(env, teammate, typed.host, login, {
        name: (loginNameValue ?? suggested).trim(),
        username: (username ?? login?.username ?? "").trim(),
        password: typed.password,
        agentMayUse: login ? undefined : agentMayUse,
      })
      toast.success(
        login ? `Updated ${login.name}` : `Saved for ${name}'s browser`
      )
      invalidate()
      onDone()
    } catch (err) {
      toast.error("Could not save the password", {
        description: errorMessage(err),
      })
    } finally {
      setSaving(false)
    }
  }
  return (
    <form
      aria-label="Save password"
      onSubmit={save}
      className="grid shrink-0 gap-2 border-b bg-card p-3 text-xs"
    >
      <div className="flex items-start gap-2">
        <KeyIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="font-medium">
            {login ? (
              <>
                Update <Ident>{login.name}</Ident> with the password you typed?
              </>
            ) : (
              <>
                Save this password for{" "}
                <span className="font-mono">{typed.host}</span> in {name}'s
                browser?
              </>
            )}
          </p>
          <p className="text-muted-foreground">
            Studio keeps it in the vault and fills it in only here, in {name}'s
            browser; no sandbox sees it. Copy it from the site info when you
            need it.
          </p>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label="Not now"
          onClick={onDone}
        >
          <XIcon />
        </Button>
      </div>
      <div className="flex flex-wrap items-end gap-2 pl-6">
        {!login && (
          <div className="grid gap-1">
            <Label htmlFor="login-name">Name</Label>
            <Input
              id="login-name"
              className="h-7 w-56 font-mono"
              value={loginNameValue ?? suggested}
              onChange={(e) => setLoginName(e.target.value.toUpperCase())}
            />
          </div>
        )}
        <div className="grid gap-1">
          <Label htmlFor="login-username">Username (optional)</Label>
          <Input
            id="login-username"
            className="h-7 w-56"
            autoComplete="off"
            value={username ?? login?.username ?? ""}
            onChange={(e) => setUsername(e.target.value)}
          />
        </div>
      </div>
      {!login && (
        <label className="flex items-center gap-2 pl-6">
          <Switch checked={agentMayUse} onCheckedChange={setAgentMayUse} />
          Let {name}'s agents use it without asking
        </label>
      )}
      <div className="flex flex-wrap gap-2 pl-6">
        <Button type="submit" size="xs" disabled={saving}>
          {saving && <Spinner />}
          {login ? "Update" : "Save"}
        </Button>
        <Button type="button" size="xs" variant="outline" onClick={onDone}>
          Not now
        </Button>
        <Button
          type="button"
          size="xs"
          variant="ghost"
          onClick={() => {
            setNeverOffered(typed.host)
            onDone()
          }}
        >
          Never for this site
        </Button>
      </div>
    </form>
  )
}

/**
 * A page asks for a username and password (HTTP Basic), which the browser can't ask for:
 * the strip saves a browser login, which the gateway then sends, or fixes the one it sent.
 */
export function SignInStrip({
  env,
  teammate,
  name,
  signIn,
  onSignedIn,
}: {
  env: string
  teammate: string
  name: string
  signIn: SignIn
  onSignedIn: () => void
}) {
  const secrets = useSecrets(env)
  const invalidate = useInvalidateSecrets()
  const login = (secrets.data ?? []).find(
    (s) => s.browserOnly && s.teammateId === teammate && s.name === signIn.login
  )
  const [form, setForm] = useState(false)
  const [username, setUsername] = useState<string>()
  const [password, setPassword] = useState("")
  const [agentMayUse, setAgentMayUse] = useState(false)
  const [saving, setSaving] = useState(false)
  const held = !!signIn.login && !signIn.refused
  const realm = signIn.realm ? ` for “${signIn.realm}”` : ""

  const letAgents = async () => {
    if (!login) return
    setSaving(true)
    try {
      const { error } = await fetchClient.PUT(`${secretsPath}/{id}`, {
        params: { path: { env, id: login.id } },
        body: { hosts: login.hosts ?? [], note: login.note, agentMayUse: true },
      })
      if (error) throw error
      invalidate()
      toast.success(`${name}'s agents may use ${login.name}`)
    } catch (err) {
      toast.error("Could not change the login", {
        description: errorMessage(err),
      })
    } finally {
      setSaving(false)
    }
  }
  const save = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      await saveLogin(env, teammate, signIn.host, login, {
        name: loginName(
          signIn.host,
          (secrets.data ?? []).map((s) => s.name)
        ),
        username: (username ?? login?.username ?? "").trim(),
        password,
        agentMayUse: login ? undefined : agentMayUse,
      })
      invalidate()
      setForm(false)
      setPassword("")
      onSignedIn()
    } catch (err) {
      toast.error("Could not save the login", {
        description: errorMessage(err),
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      role="status"
      aria-label="Sign in"
      className="grid shrink-0 gap-2 border-b bg-(--st-waiting)/10 px-3 py-1.5 text-xs"
    >
      <div className="flex items-center gap-2">
        <LockKeyIcon className="size-3.5 shrink-0 text-(--st-waiting-fg)" />
        <span className="min-w-0 flex-1">
          {signIn.refused ? (
            <>
              <b className="font-mono font-semibold">{signIn.host}</b> refused
              the login <Ident>{signIn.login ?? ""}</Ident>
              {realm}.
            </>
          ) : held ? (
            <>
              <b className="font-mono font-semibold">{signIn.host}</b> asks to
              sign in{realm}: {name}'s agents may not use{" "}
              <Ident>{signIn.login ?? ""}</Ident> without asking.
            </>
          ) : (
            <>
              <b className="font-mono font-semibold">{signIn.host}</b> asks for
              a username and password{realm}.
            </>
          )}
        </span>
        {held && login && (
          <Button
            variant="outline"
            size="xs"
            disabled={saving}
            onClick={() => void letAgents()}
          >
            Let agents use it
          </Button>
        )}
        {!held && !form && (
          <Button variant="outline" size="xs" onClick={() => setForm(true)}>
            {signIn.refused ? "Change the password…" : "Sign in…"}
          </Button>
        )}
      </div>
      {form && (
        <form
          onSubmit={save}
          className="flex flex-wrap items-end gap-2 pb-1 pl-5.5"
        >
          <div className="grid gap-1">
            <Label htmlFor="signin-username">Username</Label>
            <Input
              id="signin-username"
              className="h-7 w-48"
              autoComplete="off"
              required
              value={username ?? login?.username ?? ""}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div className="grid gap-1">
            <Label htmlFor="signin-password">Password</Label>
            <Input
              id="signin-password"
              type="password"
              className="h-7 w-48"
              autoComplete="off"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          {!login && (
            <label className="flex h-7 items-center gap-2">
              <Switch checked={agentMayUse} onCheckedChange={setAgentMayUse} />
              Agents may use it
            </label>
          )}
          <Button type="submit" size="xs" disabled={saving}>
            {saving && <Spinner />}
            {login ? "Save and retry" : "Save and sign in"}
          </Button>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            onClick={() => setForm(false)}
          >
            Cancel
          </Button>
        </form>
      )}
    </div>
  )
}

/** The teammate's saved logins for a site, in its site info: copy, let agents use, delete. */
export function SiteLogins({
  env,
  teammate,
  name,
  host,
}: {
  env: string
  teammate: string
  name: string
  host: string
}) {
  const secrets = useSecrets(env)
  const invalidate = useInvalidateSecrets()
  const logins = loginsFor(secrets.data ?? [], teammate, host)
  const update = $api.useMutation("put", `${secretsPath}/{id}`, {
    onSettled: invalidate,
    onError: (err) =>
      toast.error("Could not change the login", {
        description: errorMessage(err),
      }),
  })
  if (!logins.length)
    return (
      <span>
        None saved. Type a password into the page and Studio offers to save it.
      </span>
    )
  return (
    <ul className="grid gap-1.5">
      {logins.map((l) => (
        <li key={l.id} className="grid gap-1">
          <div className="flex min-w-0 items-center gap-1">
            <span className="min-w-0 flex-1 truncate">
              <Ident>{l.name}</Ident>
              {l.username && (
                <span className="text-muted-foreground"> · {l.username}</span>
              )}
            </span>
            <Button
              variant="ghost"
              size="icon-xs"
              title="Copy password"
              aria-label={`Copy the password of ${l.name}`}
              onClick={() => void copyPassword(env, l)}
            >
              <CopyIcon />
            </Button>
            <DeleteSecret env={env} secret={l} />
          </div>
          <label className="flex items-center gap-2 text-muted-foreground">
            <Switch
              checked={l.agentMayUse}
              disabled={update.isPending}
              onCheckedChange={(on) =>
                update.mutate({
                  params: { path: { env, id: l.id } },
                  body: { hosts: l.hosts ?? [], note: l.note, agentMayUse: on },
                })
              }
            />
            {name}'s agents use it without asking
          </label>
        </li>
      ))}
    </ul>
  )
}

export async function copyPassword(env: string, login: Secret) {
  try {
    await copyText(await loginPassword(env, login.id))
    toast.success(`Copied the password of ${login.name}`)
  } catch (err) {
    toast.error("Could not copy the password", {
      description: errorMessage(err),
    })
  }
}
