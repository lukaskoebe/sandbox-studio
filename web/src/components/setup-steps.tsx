import { useState, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PlusIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { ForgeDialog } from "@/components/forge-dialog"
import {
  SecurityLevelPicker,
  useEnvironmentLevel,
} from "@/components/security-level"
import { HostChecks } from "@/components/runtime-card"
import { DeleteSandboxButton } from "@/components/sandbox-options-menu"
import { SandboxProgress } from "@/components/sandbox-progress"
import { HostLoginOffers, type HostOffer } from "@/components/host-logins"
import { ProviderDialog } from "@/components/provider-dialog"
import { StatusBadge } from "@/components/status-badge"
import { TeammateForm } from "@/components/teammate-form"
import { Ident } from "@/components/ident"
import {
  CloneRepositories,
  RepositoryChecklist,
  type RepoChoice,
} from "@/components/clone-repositories"
import {
  $api,
  errorMessage,
  type Provider,
  type Teammate,
} from "@/lib/api/client"
import { isSubscription, kindLabels, harnessLabels } from "@/lib/teammates"
import type { SetupKey } from "@/lib/setup"

/** The body of one step of the guided setup. Each one uses the forms the settings pages use. */
export function SetupStepBody({ step, env }: { step: SetupKey; env: string }) {
  switch (step) {
    case "doctor":
      return (
        <Column>
          <Hint>
            Studio checks the host it runs on. A failed check names its fix;
            skip it to come back later.
          </Hint>
          <HostChecks />
        </Column>
      )
    case "provider":
      return <ProviderStep env={env} />
    case "teammate":
      return <TeammateStep env={env} />
    case "git":
      return <GitStep env={env} />
    case "network":
      return <NetworkLevelStep env={env} />
    case "sandbox":
      return <SandboxStep env={env} />
  }
}

function Column({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-1 gap-3">{children}</div>
}

function Hint({ children }: { children: ReactNode }) {
  return <p className="text-xs/relaxed text-muted-foreground">{children}</p>
}

/**
 * The network level step: the environment's level, which new environments start at guarded. It
 * can be changed later on the Network page, so going through the step is the step.
 */
function NetworkLevelStep({ env }: { env: string }) {
  const { level, saving, setLevel } = useEnvironmentLevel(env)
  return (
    <Column>
      <Hint>
        Sandboxes reach the internet through Studio. This sets how many hosts
        may connect without asking. Change it later in Network.
      </Hint>
      <SecurityLevelPicker
        env={env}
        value={level}
        onChange={(v) => {
          if (v) setLevel(v)
        }}
        disabled={saving}
      />
      <a
        href="https://lukaskoebe.github.io/sandbox-studio/network/"
        target="_blank"
        rel="noreferrer"
        className="w-fit text-xs underline underline-offset-4"
      >
        Learn more
      </a>
    </Column>
  )
}

function ProviderStep({ env }: { env: string }) {
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  // The add dialog: open, and with the kind and name of a login on this computer.
  const [adding, setAdding] = useState<{ offer?: HostOffer } | null>(null)
  const [signingIn, setSigningIn] = useState<Provider>()
  const list = providers.data ?? []
  return (
    <Column>
      <Hint>
        Teammates reach a model through a provider: an API key, or a
        subscription login. Keys are sealed on arrival, and sandboxes only ever
        see a placeholder.
      </Hint>
      {list.length ? (
        <ul className="divide-y rounded-md ring-1 ring-foreground/10">
          {list.map((p) => (
            <li key={p.id} className="flex items-center gap-2 p-2.5 text-xs">
              <span className="font-medium">{p.name}</span>
              <span className="text-muted-foreground">
                {kindLabels[p.kind]}
              </span>
              {isSubscription(p.kind) && p.state !== "ready" && (
                <Button
                  size="xs"
                  variant="outline"
                  className="ml-auto"
                  onClick={() => setSigningIn(p)}
                >
                  Sign in
                </Button>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted-foreground">No provider yet.</p>
      )}
      <HostLoginOffers
        env={env}
        providers={list}
        onUse={(offer) => setAdding({ offer })}
      />
      <div>
        <Button size="sm" variant="outline" onClick={() => setAdding({})}>
          <PlusIcon />
          Add provider
        </Button>
      </div>
      <ProviderDialog
        env={env}
        offer={adding?.offer}
        open={!!adding}
        onOpenChange={(open) => !open && setAdding(null)}
      />
      <ProviderDialog
        env={env}
        provider={signingIn}
        open={!!signingIn}
        onOpenChange={(open) => !open && setSigningIn(undefined)}
      />
    </Column>
  )
}

/** The default teammate's name and role, which a new teammate starts with. */
const defaultTeammate = { name: "Iris", role: "Generalist engineer" }

function TeammateStep({ env }: { env: string }) {
  const queryClient = useQueryClient()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const first = teammates.data?.[0]
  if (first) return <TeammateRow env={env} teammate={first} />
  if (!providers.data?.length)
    return (
      <Hint>Add a provider first; Studio then creates a teammate with it.</Hint>
    )
  return (
    <Column>
      <Hint>
        A teammate is an agent identity: the harness it runs in, its model and
        the name it commits under. Studio suggests one, which you can change.
      </Hint>
      <TeammateForm
        env={env}
        providers={providers.data}
        prefill={defaultTeammate}
        onSaved={() =>
          queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
        }
      />
    </Column>
  )
}

function TeammateRow({ env, teammate }: { env: string; teammate: Teammate }) {
  return (
    <Column>
      <Hint>
        Studio created {teammate.name} with your first provider. Accept it, or
        change its harness, model or instructions.
      </Hint>
      <div className="flex items-center gap-3 rounded-md p-2.5 text-xs ring-1 ring-foreground/10">
        <span className="font-medium">{teammate.name}</span>
        <span className="text-muted-foreground">
          {harnessLabels[teammate.harness]}
        </span>
        {teammate.model && <Ident>{teammate.model}</Ident>}
        <Link
          to="/e/$env/teammates/$id"
          params={{ env, id: teammate.id }}
          search={{ profile: "settings", from: "setup" }}
          className="ml-auto underline underline-offset-4"
        >
          Edit
        </Link>
      </div>
    </Column>
  )
}

function GitStep({ env }: { env: string }) {
  const agent = $api.useQuery("get", "/api/system/ssh-agent")
  const forges = $api.useQuery("get", "/api/environments/{env}/forges", {
    params: { path: { env } },
  })
  const [adding, setAdding] = useState(false)
  const status = agent.data
  const keys = status?.keys?.length ?? 0
  return (
    <Column>
      <Hint>
        Sandboxes fetch and push through Studio, which holds the credentials and
        keeps each push for your review. Without a forge, git stays local to the
        sandbox.
      </Hint>
      {status && (
        <p className="text-xs">
          {status.available ? (
            <>
              The SSH agent is available with {keys}{" "}
              {keys === 1 ? "key" : "keys"}.
            </>
          ) : (
            <>
              <span className="text-muted-foreground">
                {status.message ?? "The SSH agent is not available."}
              </span>{" "}
              {status.fix}
            </>
          )}
        </p>
      )}
      {forges.data?.length ? (
        <ul className="divide-y rounded-md ring-1 ring-foreground/10">
          {forges.data.map((f) => (
            <li key={f.id} className="flex items-center gap-2 p-2.5 text-xs">
              <span className="font-medium">{f.name}</span>
              <Ident>{f.baseUrl}</Ident>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted-foreground">No forge yet.</p>
      )}
      <div>
        <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
          <PlusIcon />
          Add forge
        </Button>
      </div>
      <ForgeDialog env={env} open={adding} onOpenChange={setAdding} />
    </Column>
  )
}

/** A sandbox's name: lowercase letters, digits and dashes, as the create dialog takes it. */
const sandboxName = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/

function SandboxStep({ env }: { env: string }) {
  const queryClient = useQueryClient()
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const [name, setName] = useState("")
  // Repositories picked with the sandbox, cloned once it runs.
  const [repos, setRepos] = useState<RepoChoice[]>([])
  const create = $api.useMutation("post", "/api/environments/{env}/sandboxes", {
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes"],
      })
      queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
    },
    onError: (err) =>
      toast.error("Could not create the sandbox", {
        description: errorMessage(err),
      }),
  })
  const invalid = name !== "" && !sandboxName.test(name)
  const latest = [...(sandboxes.data ?? [])]
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    .at(0)

  if (latest) {
    const building = latest.status === "creating" || latest.status === "failed"
    return (
      <Column>
        {building ? (
          <SandboxProgress
            sandbox={latest}
            action={
              latest.status === "failed" ? (
                <DeleteSandboxButton env={env} sandbox={latest} />
              ) : undefined
            }
          />
        ) : (
          <div className="flex items-center gap-3 text-xs">
            <Ident>{latest.name}</Ident>
            <StatusBadge sandbox={latest} />
          </div>
        )}
        {!building && (
          <div>
            <Button
              size="sm"
              variant="outline"
              render={
                <Link
                  to="/e/$env/sandboxes/$id"
                  params={{ env, id: latest.id }}
                  search={{ from: "setup" }}
                />
              }
            >
              Open sandbox
            </Button>
          </div>
        )}
        {!building && (
          <CloneRepositories env={env} sandbox={latest} queued={repos} />
        )}
      </Column>
    )
  }

  const owner = teammates.data?.[0]
  return (
    <form
      className="grid gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (!name || invalid) return
        create.mutate({
          params: { path: { env } },
          body: { name, teammateId: owner?.id },
        })
      }}
    >
      <Hint>
        A Linux VM with Docker and a persistent workspace. Its progress shows
        here, and the workspace opens once it runs.
        {owner && <> It is {owner.name}&apos;s.</>}
      </Hint>
      <Field data-invalid={invalid || undefined}>
        <FieldLabel htmlFor="setup-sandbox-name">Name</FieldLabel>
        <Input
          id="setup-sandbox-name"
          autoComplete="off"
          placeholder="my-project"
          value={name}
          aria-invalid={invalid}
          onChange={(e) => setName(e.target.value.toLowerCase())}
        />
        {invalid ? (
          <FieldError>
            Lowercase letters, digits and dashes; up to 40 characters.
          </FieldError>
        ) : (
          <FieldDescription>Also the sandbox&apos;s hostname.</FieldDescription>
        )}
      </Field>
      <RepositoryChecklist
        env={env}
        value={repos}
        onChange={setRepos}
        disabled={create.isPending}
      />
      <div>
        <Button
          size="sm"
          type="submit"
          disabled={!name || invalid || create.isPending}
        >
          {create.isPending && <Spinner />}
          Create sandbox
        </Button>
      </div>
    </form>
  )
}
