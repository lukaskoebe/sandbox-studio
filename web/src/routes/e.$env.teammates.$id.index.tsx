import { Link, createFileRoute } from "@tanstack/react-router"
import { StatusBadge } from "@/components/status-badge"
import { Readiness } from "@/components/teammate-readiness"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { $api } from "@/lib/api/client"
import { harnessLabels, kindLabels } from "@/lib/teammates"

export const Route = createFileRoute("/e/$env/teammates/$id/")({
  component: TeammateOverview,
})

function TeammateOverview() {
  const { env, id } = Route.useParams()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )
  const secrets = $api.useQuery("get", "/api/environments/{env}/secrets", {
    params: { path: { env } },
  })
  const t = teammates.data?.find((p) => p.id === id)
  if (!t) return null
  const provider = providers.data?.find((p) => p.id === t.providerId)
  const owned = (sandboxes.data ?? []).filter((s) => s.teammateId === id)
  const scoped = (secrets.data ?? []).filter((s) => s.teammateId === id)

  return (
    <div className="grid max-w-5xl gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Agent</CardTitle>
          <CardDescription>
            The harness it runs in, and the provider and model behind it.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 text-xs">
            <dt className="text-muted-foreground">Harness</dt>
            <dd>{harnessLabels[t.harness]}</dd>
            <dt className="text-muted-foreground">Provider</dt>
            <dd>
              {provider ? (
                <>
                  {provider.name}{" "}
                  <span className="text-muted-foreground">
                    {kindLabels[provider.kind]}
                  </span>
                </>
              ) : (
                "None"
              )}
            </dd>
            <dt className="text-muted-foreground">Model</dt>
            <dd className="font-mono">
              {t.model || (
                <span className="text-muted-foreground">default</span>
              )}
            </dd>
            <dt className="text-muted-foreground">Status</dt>
            <dd>
              <Readiness teammate={t} provider={provider} />
            </dd>
          </dl>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Identity</CardTitle>
          <CardDescription>
            The name it commits under, and its role.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 text-xs">
            <dt className="text-muted-foreground">Role</dt>
            <dd>
              {t.role || <span className="text-muted-foreground">none</span>}
            </dd>
            <dt className="text-muted-foreground">Git identity</dt>
            <dd className="break-all">
              {t.gitName} &lt;{t.gitEmail}&gt;
            </dd>
          </dl>
        </CardContent>
      </Card>
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle>Soul</CardTitle>
          <CardDescription>
            Personality and working rules, in markdown.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {t.soul ? (
            <pre className="font-mono text-xs/relaxed whitespace-pre-wrap">
              {t.soul}
            </pre>
          ) : (
            <p className="text-xs text-muted-foreground">
              No soul yet. Add one on the Settings tab.
            </p>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Sandboxes</CardTitle>
          <CardDescription>The sandboxes it owns.</CardDescription>
        </CardHeader>
        <CardContent>
          {owned.length ? (
            <ul className="space-y-1">
              {owned.map((sb) => (
                <li key={sb.id}>
                  <Link
                    to="/e/$env/sandboxes/$id"
                    params={{ env, id: sb.id }}
                    className="flex items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-muted"
                  >
                    <span className="truncate font-medium">{sb.name}</span>
                    <span className="ml-auto">
                      <StatusBadge sandbox={sb} />
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">
              No sandboxes. Pick this teammate as the owner when you create one.
            </p>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Secrets</CardTitle>
          <CardDescription>
            Secrets that only this teammate's sandboxes see.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {scoped.length ? (
            <ul className="space-y-1">
              {scoped.map((s) => (
                <li key={s.id} className="px-2 py-1 font-mono text-xs">
                  {s.name}
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">
              None.{" "}
              <Link to="/e/$env/secrets" params={{ env }} className="underline">
                Add a secret
              </Link>{" "}
              and scope it to this teammate.
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
