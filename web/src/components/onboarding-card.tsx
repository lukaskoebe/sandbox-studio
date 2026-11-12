import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate } from "@tanstack/react-router"
import {
  BrowserIcon,
  CheckIcon,
  GitBranchIcon,
  KeyIcon,
  MonitorIcon,
  PlayIcon,
  PlusIcon,
  ShieldCheckIcon,
  SparkleIcon,
  type Icon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { SectionHeader } from "@/components/section-header"
import { useGitStatus } from "@/components/computer/git-changes"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, isReady, type Sandbox } from "@/lib/api/client"
import { saveDraft } from "@/lib/drafts"
import {
  featureDone,
  onboardingFeatures,
  useOnboarding,
  type FeatureKey,
} from "@/lib/onboarding"
import { setupOver, useSetup } from "@/lib/setup"
import { cn } from "@/lib/utils"

const icons: Record<FeatureKey, Icon> = {
  network: ShieldCheckIcon,
  push: GitBranchIcon,
  browser: BrowserIcon,
  preview: MonitorIcon,
  secret: KeyIcon,
}

/** How long the card stays once every feature is done, in ms. */
const doneFor = 24 * 60 * 60 * 1000

/**
 * Home's Get to know Studio card (docs/frontend.md#onboarding): once setup is over, a first task
 * for a teammate, and the five features that each explain themselves the first time they happen.
 * It stays until all five are done or the user says Not now.
 */
export function OnboardingCard({ env }: { env: string }) {
  const { setup } = useSetup(env)
  const { onboarding, dismiss, saving } = useOnboarding(env)
  // A request or a push that comes in may tick a feature off.
  const queryClient = useQueryClient()
  const pending = usePendingApprovals().data?.length ?? 0
  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: ["get", "/api/onboarding"] })
  }, [pending, queryClient])
  if (!setup || !setupOver(setup) || !onboarding || onboarding.dismissed)
    return null
  const doneAt = onboarding.doneAt ? Date.parse(onboarding.doneAt) : undefined
  if (doneAt && Date.now() - doneAt > doneFor) return null
  const done = onboardingFeatures.filter((f) => featureDone(onboarding, f.key))
  return (
    <section
      aria-label="Get to know Studio"
      className="grid gap-2.5 rounded-lg border border-dashed border-primary/40 bg-primary/[0.035] p-3"
    >
      <SectionHeader
        title="Get to know Studio"
        icon={SparkleIcon}
        count={`${done.length}/${onboardingFeatures.length}`}
        actions={
          <Button variant="ghost" size="xs" disabled={saving} onClick={dismiss}>
            {doneAt ? "Dismiss" : "Not now"}
          </Button>
        }
      />
      <div className="flex gap-0.5">
        {onboardingFeatures.map((f) => (
          <span
            key={f.key}
            className={cn(
              "h-1 flex-1 rounded-[1px]",
              featureDone(onboarding, f.key) ? "bg-primary" : "bg-foreground/10"
            )}
          />
        ))}
      </div>
      {doneAt ? (
        <p className="text-xs text-muted-foreground">
          That's all five. Settings → General shows the tips again.
        </p>
      ) : (
        <FirstTask env={env} />
      )}
      <ul className="grid divide-y rounded-md border bg-card">
        {onboardingFeatures.map((f) => {
          const I = icons[f.key]
          const ok = featureDone(onboarding, f.key)
          return (
            <li
              key={f.key}
              className="flex min-w-0 items-center gap-2.5 px-3 py-1.5 text-xs"
            >
              <I
                className={cn(
                  "size-3.5 shrink-0",
                  ok ? "text-primary" : "text-muted-foreground"
                )}
              />
              <span className="shrink-0 font-medium">{f.title}</span>
              <span className="min-w-0 truncate text-muted-foreground">
                {f.short}
              </span>
              <span className="ml-auto shrink-0">
                {ok ? (
                  <CheckIcon
                    aria-label="seen"
                    className="size-3.5 text-primary"
                  />
                ) : f.key === "secret" ? (
                  <Button
                    size="xs"
                    variant="outline"
                    render={
                      <Link to="/e/$env/settings/secrets" params={{ env }} />
                    }
                  >
                    Try it
                  </Button>
                ) : (
                  <span className="text-muted-foreground">not seen yet</span>
                )}
              </span>
            </li>
          )
        })}
      </ul>
    </section>
  )
}

/** The repository's folder name, from git's path relative to /workspace. */
function repoName(path?: string) {
  return path ? path.split("/").filter(Boolean).at(-1) : undefined
}

/** The first tasks the card offers, for a checkout named repo, and how many features each shows. */
function starters(repo?: string) {
  const where = repo ?? "the workspace"
  return [
    {
      text: `In ${where}, add a small health check endpoint, or a status page if it has no server. Run the app, show it to me in your browser, then push a branch and open a pull request.`,
      shows: "shows 4 of 5",
    },
    {
      text: `Run the tests in ${where}. Fix one that fails, or add one that's missing, then push the fix on a branch.`,
      shows: "shows 2 of 5",
    },
  ]
}

/** A first task for a teammate in its sandbox, which opens a new chat with the task to edit. */
function FirstTask({ env }: { env: string }) {
  const navigate = useNavigate()
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const owned = (sandboxes.data ?? []).filter((s) => s.teammateId)
  const sb: Sandbox | undefined = owned.find(isReady) ?? owned.at(0)
  const git = useGitStatus(env, sb?.id ?? "", {
    enabled: !!sb && isReady(sb),
    shown: false,
  })
  const [picked, setPicked] = useState(0)
  if (!sb) return null
  const owner = teammates.data?.find((t) => t.id === sb.teammateId)
  const who = owner?.name ?? "your teammate"
  const tasks = starters(repoName(git.data?.repos?.at(0)?.path))
  const start = () => {
    const text = tasks[picked]?.text ?? ""
    saveDraft(`new-chat:${sb.id}`, { text, files: [], mentions: [] })
    void navigate({
      to: "/e/$env/chats/new",
      params: { env },
      search: { sandbox: sb.id },
    })
  }
  return (
    <div className="grid gap-2">
      <p className="text-xs text-muted-foreground">
        Give {who} a first task in{" "}
        <span className="font-mono text-foreground">{sb.name}</span>. Each
        feature below explains itself the first time it happens.
      </p>
      <div role="radiogroup" aria-label="First task" className="grid gap-1">
        {[...tasks, { text: "", shows: "" }].map((t, i) => (
          <button
            key={i}
            type="button"
            role="radio"
            aria-checked={picked === i}
            onClick={() => setPicked(i)}
            className={cn(
              "flex min-w-0 items-start gap-2 rounded-md border bg-card px-2.5 py-1.5 text-left text-xs outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring/30",
              picked === i && "border-primary bg-primary/5"
            )}
          >
            {t.text ? (
              <PlayIcon className="mt-0.5 size-3 shrink-0 text-primary" />
            ) : (
              <PlusIcon className="mt-0.5 size-3 shrink-0 text-muted-foreground" />
            )}
            <span
              className={cn(
                "min-w-0 flex-1",
                !t.text && "text-muted-foreground"
              )}
            >
              {t.text || "Your own task…"}
            </span>
            {t.shows && (
              <span className="shrink-0 font-mono text-meta text-muted-foreground">
                {t.shows}
              </span>
            )}
          </button>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={start}>
          <PlayIcon />
          Start with {who}
        </Button>
        <span className="text-xs text-muted-foreground">
          Opens a chat with the task; you can edit it before you send.
        </span>
      </div>
    </div>
  )
}
