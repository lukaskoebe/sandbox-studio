import { useState, type FormEvent } from "react"
import { ChatCircleDotsIcon } from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Questionnaire,
  QuestionnaireActions,
  QuestionnaireChoice,
  QuestionnaireChoices,
  QuestionnaireDescription,
  QuestionnaireInput,
  QuestionnaireItem,
  QuestionnaireNext,
  QuestionnairePrevious,
  QuestionnaireProgress,
  QuestionnaireSkip,
  QuestionnaireSubmit,
  QuestionnaireTitle,
} from "@/components/ui/questionnaire"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage, fetchClient } from "@/lib/api/client"
import type { ChatItem } from "@/lib/chats"

/** One property of the question's form, in the JSON Schema subset Studio accepts. */
type Prop = {
  type?: string | string[]
  title?: string
  description?: string
  enum?: unknown[]
  items?: { enum?: unknown[] }
}

type Field = {
  name: string
  title: string
  description?: string
  required: boolean
  /** How the field is asked: pick one, pick several, a yes/no, a number or text. */
  kind: "one" | "many" | "bool" | "number" | "text"
  choices: string[]
}

function typeOf(p: Prop) {
  const t = Array.isArray(p.type) ? p.type.find((x) => x !== "null") : p.type
  return t ?? "string"
}

/** The form's fields; a schema that isn't an object of properties asks nothing. */
export function fields(schema: unknown): Field[] {
  if (!schema || typeof schema !== "object") return []
  const s = schema as { properties?: Record<string, Prop>; required?: unknown }
  const required = Array.isArray(s.required) ? s.required : []
  return Object.entries(s.properties ?? {}).map(([name, p]) => {
    const t = typeOf(p)
    const kind: Field["kind"] = p.enum
      ? "one"
      : t === "array"
        ? "many"
        : t === "boolean"
          ? "bool"
          : t === "number" || t === "integer"
            ? "number"
            : "text"
    const choices =
      kind === "one"
        ? (p.enum ?? []).map(String)
        : kind === "many"
          ? (p.items?.enum ?? []).map(String)
          : kind === "bool"
            ? ["Yes", "No"]
            : []
    return {
      name,
      title: p.title || name,
      description: p.description,
      required: required.includes(name),
      kind,
      choices,
    }
  })
}

/** The form's values as the schema wants them; empty optional answers are left out. */
function answer(form: FormData, list: Field[], schema: unknown) {
  const props =
    (schema as { properties?: Record<string, Prop> } | null)?.properties ?? {}
  const out: Record<string, unknown> = {}
  for (const f of list) {
    if (f.kind === "many") {
      out[f.name] = form.getAll(f.name).map(String)
      continue
    }
    const v = form.get(f.name)
    if (v === null || v === "") continue
    const raw = String(v)
    if (f.kind === "bool") out[f.name] = raw === "Yes"
    else if (f.kind === "number") out[f.name] = Number(raw)
    else if (f.kind === "one") {
      // Enum values keep their JSON type: a number stays a number.
      const match = props[f.name].enum?.find((e) => String(e) === raw)
      out[f.name] = match ?? raw
    } else out[f.name] = raw
  }
  return out
}

const settled: Record<string, string> = {
  answered: "Answered",
  declined: "Skipped",
  cancelled: "No longer needed",
}

/**
 * A question the agent asks (an ACP form): one step per field in shadcn's Questionnaire,
 * sent back as the form's values. Skipping the question declines it.
 */
export function QuestionCard({
  env,
  chatId,
  item,
  teammateName,
}: {
  env: string
  chatId: string
  item: ChatItem
  teammateName: string
}) {
  const q = item.question
  const [sending, setSending] = useState<"accept" | "decline">()
  const [error, setError] = useState<string>()
  if (!q) return null
  const list = fields(q.schema)
  const pending = item.status === "pending"

  const send = async (action: "accept" | "decline", content?: unknown) => {
    setSending(action)
    setError(undefined)
    const res = await fetchClient.POST(
      "/api/environments/{env}/chats/{chat}/items/{item}/answer",
      {
        params: { path: { env, chat: chatId, item: item.id } },
        body: { action, content },
      }
    )
    setSending(undefined)
    if (res.error) setError(errorMessage(res.error))
  }
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    void send("accept", answer(new FormData(e.currentTarget), list, q.schema))
  }

  return (
    <div className="grid min-w-0 gap-3 rounded-lg border border-primary/30 bg-card p-3 text-xs">
      <div className="flex items-start gap-2">
        <ChatCircleDotsIcon className="mt-0.5 size-3.5 shrink-0 text-primary" />
        <div className="grid min-w-0 flex-1 gap-0.5">
          <p className="font-medium">{teammateName} has a question</p>
          <p className="wrap-break-word whitespace-pre-wrap">{q.message}</p>
        </div>
        {!pending && (
          <Badge variant="outline" className="shrink-0">
            {settled[item.status ?? ""] ?? item.status}
          </Badge>
        )}
      </div>
      {!pending && q.action === "accept" && q.answer != null && (
        <Answers value={q.answer} list={list} />
      )}
      {pending && list.length > 0 && (
        <Questionnaire
          onSubmit={submit}
          items={list.map((f) => ({
            name: f.name,
            required: f.required,
            choices: f.choices.map((value) => ({ value })),
          }))}
        >
          {list.length > 1 && <QuestionnaireProgress />}
          {list.map((f) => (
            <QuestionnaireItem
              key={f.name}
              name={f.name}
              required={f.required}
              multiple={f.kind === "many"}
            >
              <QuestionnaireTitle>{f.title}</QuestionnaireTitle>
              {f.description && (
                <QuestionnaireDescription>
                  {f.description}
                </QuestionnaireDescription>
              )}
              {f.choices.length > 0 ? (
                <QuestionnaireChoices>
                  {f.choices.map((c) => (
                    <QuestionnaireChoice key={c} value={c}>
                      {c}
                    </QuestionnaireChoice>
                  ))}
                </QuestionnaireChoices>
              ) : (
                <QuestionnaireInput
                  type={f.kind === "number" ? "number" : "text"}
                  placeholder="Your answer"
                />
              )}
            </QuestionnaireItem>
          ))}
          <QuestionnaireActions>
            <QuestionnairePrevious size="sm" />
            <QuestionnaireSkip size="sm" />
            <QuestionnaireNext size="sm" />
            <QuestionnaireSubmit size="sm" disabled={!!sending}>
              {sending === "accept" && <Spinner />}
              Send answer
            </QuestionnaireSubmit>
          </QuestionnaireActions>
        </Questionnaire>
      )}
      {pending && (
        <div className="flex items-center gap-1.5">
          <Button
            variant="ghost"
            size="xs"
            disabled={!!sending}
            onClick={() => void send("decline")}
          >
            {sending === "decline" && <Spinner className="size-3" />}
            Skip this question
          </Button>
          {list.length === 0 && (
            <Button
              size="sm"
              className="ml-auto"
              disabled={!!sending}
              onClick={() => void send("accept", {})}
            >
              {sending === "accept" && <Spinner />}
              OK
            </Button>
          )}
        </div>
      )}
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}

function Answers({ value, list }: { value: unknown; list: Field[] }) {
  if (!value || typeof value !== "object") return null
  const v = value as Record<string, unknown>
  const show = (x: unknown) =>
    Array.isArray(x)
      ? x.map(String).join(", ")
      : typeof x === "boolean"
        ? x
          ? "Yes"
          : "No"
        : String(x)
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
      {Object.entries(v).map(([k, x]) => (
        <div key={k} className="contents">
          <dt className="text-muted-foreground">
            {list.find((f) => f.name === k)?.title ?? k}
          </dt>
          <dd className="min-w-0 wrap-break-word">{show(x)}</dd>
        </div>
      ))}
    </dl>
  )
}
