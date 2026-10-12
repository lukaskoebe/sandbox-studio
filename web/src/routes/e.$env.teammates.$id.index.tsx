import type { ReactNode } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import {
  CubeIcon,
  GitBranchIcon,
  IdentificationCardIcon,
  KeyIcon,
  RobotIcon,
  SparkleIcon,
  type Icon,
} from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import { StatusBadge } from "@/components/status-badge"
import { Readiness } from "@/components/teammate-readiness"
import type { Area } from "@/lib/accents"
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
    <div className="grid max-w-5xl gap-x-8 gap-y-6 lg:grid-cols-2">
      <OverviewSection
        title="Agent"
        icon={RobotIcon}
        area="chat"
        description="The harness it runs in, and the provider and model behind it."
      >
        <dl className="divide-y text-xs">
          <Row label="Harness">{harnessLabels[t.harness]}</Row>
          <Row label="Provider">
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
          </Row>
          <Row label="Model">
            {t.model ? (
              <Ident>{t.model}</Ident>
            ) : (
              <span className="text-muted-foreground">default</span>
            )}
          </Row>
          <Row label="Status">
            <Readiness teammate={t} provider={provider} />
          </Row>
        </dl>
      </OverviewSection>
      <OverviewSection
        title="Identity"
        icon={IdentificationCardIcon}
        area="chat"
        description="The role it plays."
      >
        <dl className="divide-y text-xs">
          <Row label="Role">
            {t.role || <span className="text-muted-foreground">none</span>}
          </Row>
        </dl>
      </OverviewSection>
      <OverviewSection
        title="Git identity"
        icon={GitBranchIcon}
        area="git"
        description="The name and email it commits under."
      >
        <dl className="divide-y text-xs">
          <Row label="Name">{t.gitName}</Row>
          <Row label="Email">
            <Ident copy>{t.gitEmail}</Ident>
          </Row>
        </dl>
      </OverviewSection>
      <OverviewSection
        title="Soul"
        icon={SparkleIcon}
        area="chat"
        description="Personality and working rules, in markdown."
      >
        {t.soul ? (
          <pre className="font-mono text-xs/relaxed whitespace-pre-wrap">
            {t.soul}
          </pre>
        ) : (
          <p className="text-xs text-muted-foreground">
            No soul yet. Add one on the Settings tab.
          </p>
        )}
      </OverviewSection>
      <OverviewSection
        title="Sandboxes"
        icon={CubeIcon}
        description="The sandboxes it owns."
      >
        {owned.length ? (
          <ul className="-mx-2 divide-y text-xs">
            {owned.map((sb) => (
              <li key={sb.id}>
                <Link
                  to="/e/$env/sandboxes/$id"
                  params={{ env, id: sb.id }}
                  className="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-muted"
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
      </OverviewSection>
      <OverviewSection
        title="Secrets"
        icon={KeyIcon}
        description="Secrets that only this teammate's sandboxes see."
      >
        {scoped.length ? (
          <ul className="-mx-2 divide-y text-xs">
            {scoped.map((s) => (
              <li key={s.id} className="px-2 py-1.5">
                <Ident>{s.name}</Ident>
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
      </OverviewSection>
    </div>
  )
}

/** A section of the overview: its title row, a line on what it holds, then its rows. */
function OverviewSection({
  title,
  icon,
  area,
  description,
  children,
}: {
  title: string
  icon: Icon
  area?: Area
  description: string
  children: ReactNode
}) {
  return (
    <section className="grid min-w-0 content-start gap-2">
      <SectionHeader title={title} icon={icon} area={area} />
      <p className="text-xs text-muted-foreground">{description}</p>
      {children}
    </section>
  )
}

/** A label and its value on one ruled row. */
function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[7rem_1fr] gap-x-3 py-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  )
}
