import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import { clock, dollars, refTargets, type LeadAsk } from "@/lib/lead"
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
 * at the threads, pushes and requests it speaks of. Lead only reads.
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

/**
 * The box under a day to ask Lead about it. One question at a time: it waits while Lead
 * answers.
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
  const [text, setText] = useState("")
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
  const busy = ask.isPending || answering
  const send = () => {
    const question = text.trim()
    if (!question || busy) return
    ask.mutate({ params: { path: { env, day } }, body: { question } })
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
      <div className="mx-auto grid w-full max-w-5xl gap-1.5">
        <div className="flex items-end gap-2">
          <Textarea
            aria-label={`Your question for ${name}`}
            placeholder={`Ask ${name} about ${about}…`}
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
            Ask
          </Button>
        </div>
        <p className="text-caption text-muted-foreground">
          {name} only reads: it answers from Studio's records of the day.
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
