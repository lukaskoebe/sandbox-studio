import { useEffect, useRef, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { ChatCircleIcon, ListChecksIcon } from "@phosphor-icons/react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import { clock, dollars, refTargets, type LeadAsk } from "@/lib/lead"
import { cn } from "@/lib/utils"
import { LeadGlyph } from "./glyph"
import { Prose } from "./prose"

/** The questions asked about a day, oldest first, refreshed by the lead event. */
function useAsks(env: string, day: string) {
  return $api.useQuery("get", "/api/environments/{env}/lead/days/{day}/asks", {
    params: { path: { env, day } },
  })
}

/**
 * Ask Lead (docs/lead.md): the questions asked about a day and Lead's answers, each pointing
 * at the threads, pushes and requests it speaks of. An answer sends nothing; plans do.
 */
export function LeadAsks({
  env,
  day,
  name,
}: {
  env: string
  day: string
  name: string
}) {
  const asks = useAsks(env, day)
  const list = asks.data ?? []
  const end = useRef<HTMLDivElement>(null)
  const was = useRef<string | null>(null)
  // A new question, or its answer, comes into view; the ones there on opening the day don't.
  const mark = asks.data ? `${list.length}:${list.at(-1)?.state ?? ""}` : null
  useEffect(() => {
    if (mark === null) return
    if (was.current !== null && was.current !== mark)
      end.current?.scrollIntoView({ block: "end", behavior: "smooth" })
    was.current = mark
  }, [mark])
  if (list.length === 0) return null
  return (
    <section aria-label={`Asked ${name}`} className="grid gap-4 border-t pt-5">
      <h2 className="section-label text-muted-foreground">Asked {name}</h2>
      {list.map((a) => (
        <Ask key={a.id} env={env} a={a} name={name} />
      ))}
      <div ref={end} />
    </section>
  )
}

function Ask({ env, a, name }: { env: string; a: LeadAsk; name: string }) {
  const refs = refTargets(a.refs)
  return (
    <article aria-label={a.question} className="grid gap-2">
      <p className="ml-auto max-w-[85%] rounded-(--radius) bg-muted px-3 py-2 text-sm/relaxed break-words whitespace-pre-wrap">
        {a.question}
      </p>
      <div className="flex max-w-3xl items-start gap-2">
        <LeadGlyph className="mt-0.5 size-4 shrink-0" />
        <div className="grid min-w-0 gap-2 text-sm/relaxed">
          {a.state === "answering" ? (
            <p className="flex items-center gap-2 text-muted-foreground">
              <Spinner /> {name} is reading the day…
            </p>
          ) : a.state === "failed" ? (
            <p className="text-muted-foreground">{a.note}</p>
          ) : (
            a.answer.split("\n\n").map((p, i) => (
              <p key={i}>
                <Prose env={env} text={p} refs={refs} />
              </p>
            ))
          )}
          {a.state !== "answering" && (
            <p className="text-caption text-muted-foreground">
              {clock(a.answeredAt ?? a.askedAt)}
              {a.model && ` · ${a.model}`}
              {a.priced && a.cost > 0 && ` · ${dollars(a.cost)}`}
            </p>
          )}
        </div>
      </div>
    </article>
  )
}

/** What the one box under a day does: ask about it, or ask for a plan to send messages. */
type ComposerMode = "ask" | "plan"

const composerModes = {
  ask: { label: "Ask", icon: ChatCircleIcon },
  plan: { label: "Plan", icon: ListChecksIcon },
} as const

/**
 * The box under a day. Ask answers from the day's records, one question at a time; Plan asks
 * Lead to draft messages to teammates, which wait for the user's approval on the plan's page.
 */
export function AskComposer({
  env,
  day,
  name,
  about,
}: {
  env: string
  day: string
  name: string
  /** The day as the placeholder names it: "today", or its date. */
  about: string
}) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [text, setText] = useState("")
  const [mode, setMode] = useState<ComposerMode>("ask")
  const asks = useAsks(env, day)
  const answering = (asks.data ?? []).some((a) => a.state === "answering")
  const ask = $api.useMutation(
    "post",
    "/api/environments/{env}/lead/days/{day}/asks",
    {
      onSuccess: () => {
        setText("")
        void queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/lead/days/{day}/asks"],
        })
      },
      onError: (err) =>
        toast.error(`Could not ask ${name}`, {
          description: errorMessage(err),
        }),
    }
  )
  const plan = $api.useMutation("post", "/api/environments/{env}/lead/plans", {
    onSuccess: (p) => {
      setText("")
      void queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/lead/plans"],
      })
      void navigate({
        to: "/e/$env/lead/plans/$plan",
        params: { env, plan: p.id },
      })
    },
    onError: (err) =>
      toast.error(`Could not ask ${name} for a plan`, {
        description: errorMessage(err),
      }),
  })
  const busy = mode === "plan" ? plan.isPending : ask.isPending || answering
  const send = () => {
    const words = text.trim()
    if (!words || busy) return
    if (mode === "plan")
      plan.mutate({ params: { path: { env } }, body: { text: words } })
    else
      ask.mutate({ params: { path: { env, day } }, body: { question: words } })
  }
  return (
    <form
      aria-label={`Ask ${name}`}
      className="shrink-0 border-t bg-background px-4 py-3 md:px-6"
      onSubmit={(e) => {
        e.preventDefault()
        send()
      }}
    >
      <div className="mx-auto grid w-full max-w-3xl gap-1.5">
        <div className="flex items-end gap-2">
          <Textarea
            aria-label={
              mode === "plan"
                ? `Ask ${name} for a plan`
                : `Your question for ${name}`
            }
            placeholder={
              mode === "plan"
                ? "For example: have Ada rerun the coupon tests on main and tell me how they do"
                : `Ask ${name} about ${about}…`
            }
            rows={1}
            maxLength={2000}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing
              ) {
                e.preventDefault()
                send()
              }
            }}
            className="max-h-40 min-h-9 resize-none"
          />
          <Button type="submit" size="sm" disabled={!text.trim() || busy}>
            {busy && <Spinner />}
            {mode === "plan" ? "Make a plan" : "Ask"}
          </Button>
        </div>
        <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted-foreground">
          <span
            role="group"
            aria-label="Mode"
            className="inline-flex h-6 shrink-0 items-center rounded-md border p-px"
          >
            {(Object.keys(composerModes) as ComposerMode[]).map((m) => {
              const I = composerModes[m].icon
              return (
                <button
                  key={m}
                  type="button"
                  aria-pressed={mode === m}
                  onClick={() => setMode(m)}
                  className={cn(
                    "inline-flex h-full items-center gap-1 rounded-[calc(var(--radius-md)-2px)] px-1.5 hover:text-foreground",
                    mode === m && "bg-muted font-medium text-foreground"
                  )}
                >
                  <I className="size-3.5" />
                  {composerModes[m].label}
                </button>
              )
            })}
          </span>
          <span className="min-w-0">
            {mode === "plan"
              ? `${name} drafts messages to teammates; nothing is sent until you approve the plan.`
              : `${name} answers from Studio's records of the day and sends nothing.`}
          </span>
        </p>
      </div>
    </form>
  )
}

/** Records that the user looked at today now, for Home's "since you looked". */
export function postLeadSeen(env: string) {
  return fetchClient
    .POST("/api/environments/{env}/lead/seen", { params: { path: { env } } })
    .catch(() => undefined)
}

/**
 * Records that the user looked at today when the place showing it opens and when they leave
 * it, so Home's "since you looked" starts from there.
 */
export function useLeadSeen(env: string, on: boolean) {
  useEffect(() => {
    if (!on) return
    void postLeadSeen(env)
    return () => void postLeadSeen(env)
  }, [env, on])
}
