import { useState } from "react"
import { XIcon } from "@phosphor-icons/react"

/** The most models a teammate offers besides its default, as the API takes them. */
export const maxOfferedModels = 20

/**
 * A list of model ids as tokens: Enter, a comma or a space adds what was typed, × or Backspace
 * in the empty field takes one off. list names a datalist of suggestions.
 */
export function ModelsInput({
  id,
  value,
  onChange,
  list,
  exclude,
  placeholder = "Add a model…",
}: {
  id: string
  value: string[]
  onChange: (models: string[]) => void
  list?: string
  /** A model left out, such as the default, which is offered anyway. */
  exclude?: string
  placeholder?: string
}) {
  const [draft, setDraft] = useState("")
  const add = (text: string) => {
    const next = [...value]
    for (const m of text.split(/[\s,]+/)) {
      if (m && m !== exclude && !next.includes(m)) next.push(m)
    }
    onChange(next.slice(0, maxOfferedModels))
    setDraft("")
  }
  return (
    <div className="flex min-h-8 w-full min-w-0 flex-wrap items-center gap-1 rounded-md border bg-transparent px-1.5 py-1 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30">
      {value.map((m) => (
        <span
          key={m}
          className="inline-flex max-w-full min-w-0 items-center gap-1 rounded-sm bg-muted py-0.5 pr-0.5 pl-1.5 font-mono text-caption"
        >
          <span className="truncate">{m}</span>
          <button
            type="button"
            aria-label={`Remove ${m}`}
            className="rounded-sm p-0.5 text-muted-foreground hover:text-foreground"
            onClick={() => onChange(value.filter((v) => v !== m))}
          >
            <XIcon className="size-3" />
          </button>
        </span>
      ))}
      {value.length < maxOfferedModels && (
        <input
          id={id}
          list={list}
          autoComplete="off"
          spellCheck={false}
          maxLength={128}
          className="min-w-32 flex-1 bg-transparent px-1 font-mono text-xs outline-none placeholder:font-sans placeholder:text-muted-foreground"
          placeholder={value.length ? "" : placeholder}
          value={draft}
          onChange={(e) => {
            const v = e.target.value
            // A suggestion picked from the list arrives whole.
            if (/[\s,]/.test(v)) add(v)
            else setDraft(v)
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault()
              if (draft.trim()) add(draft)
            } else if (e.key === "Backspace" && draft === "" && value.length) {
              onChange(value.slice(0, -1))
            }
          }}
          onBlur={() => draft.trim() && add(draft)}
        />
      )}
    </div>
  )
}
