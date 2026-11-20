import { useEffect, useRef, useState, type ReactNode } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ChatTextIcon,
  CheckCircleIcon,
  CircleIcon,
  HourglassIcon,
  InfoIcon,
  MinusCircleIcon,
  PlusCircleIcon,
  ScrollIcon,
  XCircleIcon,
  type Icon,
} from "@phosphor-icons/react"
import { useDecideApproval } from "@/components/approval-decision"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { $api, errorMessage } from "@/lib/api/client"
import { useClaimApprovals } from "@/lib/approval-claims"
import {
  clock,
  dollars,
  planOpen,
  planStateLabel,
  type LeadPlan,
  type LeadPlanItem,
  type PlanStep,
} from "@/lib/lead"
import { cn } from "@/lib/utils"
import { LeadGlyph } from "./glyph"

/** Lead's name, from its settings; "Lead" until they load. */
function useLeadName(env: string) {
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  return {
    name: lead.data?.settings.name ?? "Lead",
    showCosts: lead.data?.settings.showCosts ?? true,
  }
}

function useRefreshPlans() {
  const queryClient = useQueryClient()
  return () => {
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/lead/plans"],
    })
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/lead/plans/{plan}"],
    })
  }
}

/**
 * A plan of Lead's (docs/lead.md#plans): what the user asked, Lead's replies and the plan it
 * proposes, its steps as Studio carries them out, and Lead's report of what the teammates
 * said. Nothing is sent before the user approves the plan as it stands.
 */
export function LeadPlanPage({ env, plan }: { env: string; plan: string }) {
  const { name, showCosts } = useLeadName(env)
  const q = $api.useQuery("get", "/api/environments/{env}/lead/plans/{plan}", {
    params: { path: { env, plan } },
  })
  const p = q.data
  const end = useRef<HTMLDivElement>(null)
  // New items, and a draft that comes in, come into view; those there on opening don't.
  const mark = p ? `${p.items?.length ?? 0}:${p.state}` : null
  const was = useRef<string | null>(null)
  useEffect(() => {
    if (mark === null) return
    if (was.current !== null && was.current !== mark)
      end.current?.scrollIntoView({ block: "end", behavior: "smooth" })
    was.current = mark
  }, [mark])

  return (
    <>
      <header data-slot="page-header" className="shrink-0 px-4 md:px-6">
        <div className="mx-auto flex min-h-14 w-full max-w-3xl flex-wrap items-center gap-x-2 gap-y-1 py-2">
          <LeadGlyph className="size-5" />
          <h1 className="min-w-0 flex-1 basis-40 text-sm font-medium text-balance">
            {p?.title || "Plan"}
            {p && (
              <span className="font-normal text-muted-foreground">
                {" "}
                · {planStateLabel(p.state)}
              </span>
            )}
          </h1>
          {p && planOpen(p) && <CancelButton env={env} plan={p} />}
        </div>
      </header>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="px-4 pb-12 md:px-6"
      >
        <div className="mx-auto grid w-full max-w-3xl grid-cols-1 content-start gap-5">
          {q.isPending ? (
            <p className="flex items-center gap-2 text-muted-foreground">
              <Spinner /> Reading the plan…
            </p>
          ) : q.error ? (
            <p role="alert" className="text-destructive">
              {q.error.status === 404
                ? "There is no such plan."
                : errorMessage(q.error)}
            </p>
          ) : (
            p && (
              <PlanThread env={env} p={p} name={name} showCosts={showCosts} />
            )
          )}
          <div ref={end} />
        </div>
      </ScrollArea>
      {p && planOpen(p) && <PlanComposer env={env} p={p} name={name} />}
      {p && !planOpen(p) && (
        <div className="shrink-0 border-t px-4 py-3 md:px-6">
          <p className="mx-auto w-full max-w-3xl text-caption text-muted-foreground">
            This plan is {planStateLabel(p.state).toLowerCase()}.{" "}
            <Link
              to="/e/$env/lead/plans/new"
              params={{ env }}
              className="text-foreground underline underline-offset-2"
            >
              Make a new plan
            </Link>
          </p>
        </div>
      )}
    </>
  )
}

function PlanThread({
  env,
  p,
  name,
  showCosts,
}: {
  env: string
  p: LeadPlan
  name: string
  showCosts: boolean
}) {
  const items = p.items ?? []
  // The last proposal is the plan as it stands; the steps show with their states there.
  let lastProposal = -1
  items.forEach((it, i) => {
    if (it.kind === "lead" && (it.steps?.length ?? 0) > 0) lastProposal = i
  })
  const showSteps =
    lastProposal >= 0 &&
    (p.state === "proposed" ||
      p.state === "running" ||
      p.state === "done" ||
      p.state === "failed" ||
      (p.state === "cancelled" && !!p.approvedAt))
  return (
    <section aria-label={`The plan with ${name}`} className="grid gap-4 pt-2">
      {items.map((it, i) => (
        <PlanItem
          key={it.id}
          env={env}
          planId={p.id}
          it={it}
          name={name}
          showCosts={showCosts}
          latest={i === items.length - 1}
          answer={answerAfter(items, i)}
          planState={p.state}
          steps={
            i === lastProposal && showSteps ? (
              <PlanSteps env={env} p={p} name={name} />
            ) : it.steps && it.steps.length > 0 ? (
              <p className="text-caption text-muted-foreground">
                Proposed {it.steps.length}{" "}
                {it.steps.length === 1 ? "message" : "messages"}
                {i !== lastProposal ? ", replaced by a later plan" : ""}.
              </p>
            ) : null
          }
        />
      ))}
      {p.state === "drafting" && (
        <div className="flex items-start gap-2 text-sm">
          <LeadGlyph className="mt-0.5 size-4 shrink-0" />
          <p className="flex items-center gap-2 text-muted-foreground">
            <Spinner /> {name} is making a plan…
          </p>
        </div>
      )}
    </section>
  )
}

/** The user's next message after item i, when they spoke before Lead did. */
function answerAfter(items: LeadPlanItem[], i: number) {
  const next = items
    .slice(i + 1)
    .find((n) => n.kind === "you" || n.kind === "lead")
  return next?.kind === "you" ? next.text : undefined
}

function PlanItem({
  env,
  planId,
  it,
  name,
  showCosts,
  latest,
  answer,
  planState,
  steps,
}: {
  env: string
  planId: string
  it: LeadPlanItem
  name: string
  showCosts: boolean
  latest: boolean
  /** What the user said next, which for a question is the answer they gave. */
  answer?: string
  planState: LeadPlan["state"]
  steps: ReactNode
}) {
  if (it.kind === "you")
    return (
      <p className="ml-auto max-w-[85%] rounded-(--radius) bg-muted px-3 py-2 text-sm/relaxed break-words whitespace-pre-wrap">
        {it.text}
      </p>
    )
  if (it.kind === "note")
    return (
      <p className="flex items-start gap-1.5 text-caption text-muted-foreground">
        <InfoIcon className="mt-px size-3.5 shrink-0" aria-hidden />
        <span>
          {it.text} <span className="font-mono">{clock(it.at)}</span>
        </span>
      </p>
    )
  return (
    <article
      aria-label={it.kind === "report" ? `${name}'s report` : `${name} says`}
      className="flex items-start gap-2"
    >
      <LeadGlyph className="mt-0.5 size-4 shrink-0" />
      <div className="grid min-w-0 flex-1 gap-2 text-sm/relaxed">
        {it.kind === "report" && (
          <p className="section-label text-muted-foreground">What came of it</p>
        )}
        {it.text.split("\n\n").map((para, i) => (
          <p key={i} className="break-words whitespace-pre-wrap">
            {para}
          </p>
        ))}
        {it.question && (
          <QuestionOptions
            env={env}
            planId={planId}
            options={it.question.options ?? []}
            active={latest && planState === "asking"}
            picked={answer}
          />
        )}
        {it.threads && it.threads.length > 0 && (
          <ul className="flex flex-wrap gap-1.5" aria-label="Threads">
            {it.threads.map((t) => (
              <li key={t.chatId}>
                <ThreadLink
                  env={env}
                  teammateId={t.teammateId}
                  chatId={t.chatId}
                  className="inline-flex items-center gap-1 rounded-(--radius) border px-2 py-0.5 text-xs hover:bg-muted"
                >
                  {t.teammate} · {t.thread}
                  {!t.answered && (
                    <span className="text-muted-foreground"> · no answer</span>
                  )}
                </ThreadLink>
              </li>
            ))}
          </ul>
        )}
        {steps}
        <p className="text-caption text-muted-foreground">
          {clock(it.at)}
          {it.model && ` · ${it.model}`}
          {showCosts && it.priced && it.cost > 0 && ` · ${dollars(it.cost)}`}
        </p>
      </div>
    </article>
  )
}

/** A thread of a teammate's, on its page. */
function ThreadLink({
  env,
  teammateId,
  chatId,
  className,
  children,
}: {
  env: string
  teammateId?: string
  chatId: string
  className?: string
  children: ReactNode
}) {
  return teammateId ? (
    <Link
      to="/e/$env/teammates/$id"
      params={{ env, id: teammateId }}
      search={{ thread: chatId }}
      className={className}
    >
      {children}
    </Link>
  ) : (
    <Link
      to="/e/$env/chats/$chat"
      params={{ env, chat: chatId }}
      className={className}
    >
      {children}
    </Link>
  )
}

/** The answers Lead offers to its question; one sends it as the user's reply. */
function QuestionOptions({
  env,
  planId,
  options,
  active,
  picked,
}: {
  env: string
  planId: string
  options: string[]
  active: boolean
  picked?: string
}) {
  const send = usePlanMessage(env, planId)
  if (options.length === 0) return null
  return (
    <div className="flex flex-wrap gap-1.5" role="group" aria-label="Answers">
      {options.map((o) => (
        <Button
          key={o}
          size="sm"
          variant={o === picked ? "secondary" : "outline"}
          aria-pressed={o === picked}
          disabled={!active || send.isPending}
          onClick={() => send.send(o)}
          className={o === picked ? "disabled:opacity-100" : undefined}
        >
          {o}
        </Button>
      ))}
    </div>
  )
}

const stepIcons: Record<PlanStep["kind"], Icon> = {
  message: ChatTextIcon,
  start: PlusCircleIcon,
  wait: HourglassIcon,
  tell: ScrollIcon,
}

/**
 * The plan's steps: what is sent to whom, and once approved how far Studio got. While it waits
 * for approval, it is approved or refused here or on its approval card.
 */
function PlanSteps({
  env,
  p,
  name,
}: {
  env: string
  p: LeadPlan
  name: string
}) {
  // The decision shows here, so the floating stack leaves the plan's approval out.
  useClaimApprovals([p.state === "proposed" ? p.approvalId : undefined])
  // Studio adds the wait and the report once the plan is approved; a proposal shows them too.
  const steps: PlanStep[] =
    p.state === "proposed"
      ? [
          ...(p.steps ?? []),
          { kind: "wait", state: "proposed" },
          { kind: "tell", state: "proposed" },
        ]
      : (p.steps ?? [])
  return (
    <div className="grid gap-3 rounded-(--radius) border p-3">
      <ol className="grid gap-3" aria-label="Steps">
        {steps.map((st, i) => (
          <StepRow key={i} env={env} st={st} name={name} />
        ))}
      </ol>
      {p.state === "proposed" && p.approvalId && (
        <PlanDecision env={env} approvalId={p.approvalId} name={name} />
      )}
      {p.approvedAt && (
        <p className="text-caption text-muted-foreground">
          Approved by you at {clock(p.approvedAt)}. Teammates still ask you for
          pushes, pull requests and network requests.
        </p>
      )}
    </div>
  )
}

function StepRow({
  env,
  st,
  name,
}: {
  env: string
  st: PlanStep
  name: string
}) {
  const KindIcon = stepIcons[st.kind]
  const where =
    st.kind === "message" ? (
      <>
        To <span className="font-medium">{st.teammate}</span> in{" "}
        {st.chatId ? (
          <ThreadLink
            env={env}
            teammateId={st.teammateId}
            chatId={st.chatId}
            className="underline-offset-2 hover:underline"
          >
            “{st.thread}”
          </ThreadLink>
        ) : (
          <>“{st.thread}”</>
        )}
      </>
    ) : st.kind === "start" ? (
      <>
        A new thread for <span className="font-medium">{st.teammate}</span> on{" "}
        {st.computer}:{" "}
        {st.chatId ? (
          <ThreadLink
            env={env}
            teammateId={st.teammateId}
            chatId={st.chatId}
            className="underline-offset-2 hover:underline"
          >
            “{st.thread}”
          </ThreadLink>
        ) : (
          <>“{st.thread}”</>
        )}
      </>
    ) : st.kind === "wait" ? (
      <>Wait for the teammates to answer</>
    ) : (
      <>{name} tells you what they said</>
    )
  return (
    <li className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-sm">
      <KindIcon className="mt-0.5 size-4 text-muted-foreground" aria-hidden />
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
        <span className="min-w-0">{where}</span>
        <StepState st={st} />
      </div>
      {st.text && (
        <p className="col-start-2 max-h-40 overflow-auto rounded-(--radius) bg-muted/60 px-2.5 py-1.5 text-xs/relaxed break-words whitespace-pre-wrap">
          {st.text}
        </p>
      )}
      {st.note && (
        <p
          className={cn(
            "col-start-2 text-caption",
            st.state === "failed" ? "text-destructive" : "text-muted-foreground"
          )}
        >
          {st.note}
        </p>
      )}
    </li>
  )
}

function StepState({ st }: { st: PlanStep }) {
  const base = "inline-flex items-center gap-1 text-caption"
  switch (st.state) {
    case "proposed":
      return null
    case "pending":
      return (
        <span className={cn(base, "text-muted-foreground")}>
          <CircleIcon className="size-3" aria-hidden /> next
        </span>
      )
    case "running":
      return (
        <span className={cn(base, "text-muted-foreground")}>
          <Spinner className="size-3" />
          {st.kind === "wait"
            ? "waiting"
            : st.kind === "tell"
              ? "writing"
              : "sending"}
        </span>
      )
    case "done":
      return (
        <span className={cn(base, "text-(--st-running)")}>
          <CheckCircleIcon className="size-3" aria-hidden />
          {st.kind === "message" || st.kind === "start" ? "sent" : "done"}
          {st.at && ` ${clock(st.at)}`}
        </span>
      )
    case "failed":
      return (
        <span className={cn(base, "text-destructive")}>
          <XCircleIcon className="size-3" aria-hidden /> not sent
        </span>
      )
    case "skipped":
      return (
        <span className={cn(base, "text-muted-foreground")}>
          <MinusCircleIcon className="size-3" aria-hidden /> skipped
        </span>
      )
  }
}

/** Approve sends the plan as it stands; Don't send cancels it. */
export function PlanDecision({
  env,
  approvalId,
  name,
  compact,
}: {
  env: string
  approvalId: string
  name: string
  compact?: boolean
}) {
  const decide = useDecideApproval()
  const refresh = useRefreshPlans()
  const send = (action: "allow" | "deny") =>
    decide.mutate(
      {
        params: { path: { env, id: approvalId } },
        body: { action, scope: "environment" },
      },
      { onSettled: refresh }
    )
  return (
    <div className="grid gap-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => send("allow")}
        >
          {decide.isPending && decide.variables.body.action === "allow" && (
            <Spinner />
          )}
          Approve and send
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={decide.isPending}
          onClick={() => send("deny")}
        >
          Don't send
        </Button>
      </div>
      {!compact && (
        <p className="text-caption text-muted-foreground">
          {name} sends these messages as they are, and nothing else. To change
          them, tell {name} below.
        </p>
      )}
    </div>
  )
}

function CancelButton({ env, plan }: { env: string; plan: LeadPlan }) {
  const refresh = useRefreshPlans()
  const cancel = $api.useMutation(
    "post",
    "/api/environments/{env}/lead/plans/{plan}/cancel",
    {
      onSettled: refresh,
      onError: (err) =>
        toast.error("Could not cancel the plan", {
          description: errorMessage(err),
        }),
    }
  )
  return (
    <Button
      size="sm"
      variant="ghost"
      disabled={cancel.isPending}
      onClick={() =>
        cancel.mutate({ params: { path: { env, plan: plan.id } } })
      }
    >
      {cancel.isPending && <Spinner />}
      Cancel plan
    </Button>
  )
}

/** Sends the user's words to a plan. */
function usePlanMessage(env: string, plan: string) {
  const refresh = useRefreshPlans()
  const m = $api.useMutation(
    "post",
    "/api/environments/{env}/lead/plans/{plan}/messages",
    {
      onSettled: refresh,
      onError: (err) =>
        toast.error("Could not send your message", {
          description: errorMessage(err),
        }),
    }
  )
  return {
    isPending: m.isPending,
    send: (text: string, onDone?: () => void) =>
      m.mutate(
        { params: { path: { env, plan } }, body: { text } },
        { onSuccess: onDone }
      ),
  }
}

function PlanComposer({
  env,
  p,
  name,
}: {
  env: string
  p: LeadPlan
  name: string
}) {
  const [text, setText] = useState("")
  const msg = usePlanMessage(env, p.id)
  const busy = p.state === "drafting" || p.state === "running"
  const send = () => {
    const t = text.trim()
    if (!t || busy || msg.isPending) return
    msg.send(t, () => setText(""))
  }
  const placeholder =
    p.state === "asking"
      ? `Answer ${name}…`
      : p.state === "proposed"
        ? `Ask ${name} for a change…`
        : p.state === "running"
          ? "The plan is running"
          : `${name} is making the plan…`
  return (
    <ComposerFrame
      label={`Write to ${name}`}
      text={text}
      setText={setText}
      onSend={send}
      placeholder={placeholder}
      disabled={busy}
      pending={msg.isPending}
      button="Send"
      caption={
        p.state === "proposed"
          ? `A change withdraws the plan's approval; ${name} writes it again.`
          : p.state === "running"
            ? "Cancel the plan to send nothing more. Messages already sent stay in their threads."
            : `Nothing is sent to a teammate until you approve the plan.`
      }
    />
  )
}

function ComposerFrame({
  label,
  text,
  setText,
  onSend,
  placeholder,
  disabled,
  pending,
  button,
  caption,
  autoFocus,
}: {
  label: string
  text: string
  setText: (t: string) => void
  onSend: () => void
  placeholder: string
  disabled?: boolean
  pending?: boolean
  button: string
  caption: string
  autoFocus?: boolean
}) {
  return (
    <form
      aria-label={label}
      className="shrink-0 border-t bg-background px-4 py-3 md:px-6"
      onSubmit={(e) => {
        e.preventDefault()
        onSend()
      }}
    >
      <div className="mx-auto grid w-full max-w-3xl gap-1.5">
        <div className="flex items-end gap-2">
          <Textarea
            aria-label={label}
            placeholder={placeholder}
            rows={1}
            maxLength={2000}
            value={text}
            disabled={disabled}
            autoFocus={autoFocus}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing
              ) {
                e.preventDefault()
                onSend()
              }
            }}
            className="max-h-40 min-h-9 resize-none"
          />
          <Button
            type="submit"
            size="sm"
            disabled={!text.trim() || disabled || pending}
          >
            {pending && <Spinner />}
            {button}
          </Button>
        </div>
        <p className="text-caption text-muted-foreground">{caption}</p>
      </div>
    </form>
  )
}

/**
 * A new plan: the user says what they need from the team, and Lead proposes the messages.
 * text starts the box, as Make a plan on a day's suggestion fills it.
 */
export function NewLeadPlanPage({
  env,
  text: start,
}: {
  env: string
  text?: string
}) {
  const { name } = useLeadName(env)
  const navigate = useNavigate()
  const refresh = useRefreshPlans()
  const [text, setText] = useState(start ?? "")
  const create = $api.useMutation(
    "post",
    "/api/environments/{env}/lead/plans",
    {
      onSuccess: (p) => {
        refresh()
        void navigate({
          to: "/e/$env/lead/plans/$plan",
          params: { env, plan: p.id },
        })
      },
      onError: (err) =>
        toast.error(`Could not ask ${name} for a plan`, {
          description: errorMessage(err),
        }),
    }
  )
  const send = () => {
    const t = text.trim()
    if (!t || create.isPending) return
    create.mutate({ params: { path: { env } }, body: { text: t } })
  }
  return (
    <>
      <header data-slot="page-header" className="shrink-0 px-4 md:px-6">
        <div className="mx-auto flex min-h-14 w-full max-w-3xl items-center gap-2 py-2">
          <LeadGlyph className="size-5" />
          <h1 className="text-sm font-medium">New plan</h1>
        </div>
      </header>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="px-4 md:px-6">
        <div className="mx-auto grid w-full max-w-3xl gap-3 pt-2 text-sm/relaxed">
          <p>
            Tell {name} what you need from the team. {name} proposes messages to
            teammates' threads, or new threads on their computers, and asks when
            something is unclear.
          </p>
          <ul className="grid gap-1 text-muted-foreground">
            <li>Nothing is sent until you approve the plan as it stands.</li>
            <li>
              {name} only sends messages. Teammates still ask you for pushes,
              pull requests and network requests.
            </li>
            <li>
              Once they answer, {name} tells you what they said, with links to
              the threads.
            </li>
          </ul>
        </div>
      </ScrollArea>
      <ComposerFrame
        label={`Ask ${name} for a plan`}
        text={text}
        setText={setText}
        onSend={send}
        placeholder="For example: have Ada rerun the coupon tests on main and tell me how they do"
        pending={create.isPending}
        button="Make a plan"
        caption={`${name}'s calls count toward its daily limit.`}
        autoFocus
      />
    </>
  )
}
