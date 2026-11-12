import { useRef, useState } from "react"
import { Autocomplete } from "@base-ui/react/autocomplete"
import { XIcon } from "@phosphor-icons/react"
import { ModelSuggestionList, useSuggestions } from "@/components/model-input"

/** The most models a teammate offers besides its default, as the API takes them. */
export const maxOfferedModels = 20

/**
 * A list of model ids as tokens: Enter, a comma or a space adds what was typed, × or Backspace
 * in the empty field takes one off. It suggests the models of suggestions not added yet.
 */
export function ModelsInput({
  id,
  value,
  onChange,
  suggestions = [],
  exclude,
  placeholder = "Add a model…",
}: {
  id: string
  value: string[]
  onChange: (models: string[]) => void
  suggestions?: string[]
  /** A model left out, such as the default, which is offered anyway. */
  exclude?: string
  placeholder?: string
}) {
  const [draft, setDraft] = useState("")
  const suggested = useSuggestions(
    suggestions.filter((m) => m !== exclude && !value.includes(m)),
    draft
  )
  // The input that adds a model would open the list again; it stays closed until the next.
  const added = useRef(false)
  const popup = {
    ...suggested,
    onOpenChange: (open: boolean) => {
      if (open && added.current) return
      suggested.onOpenChange(open)
    },
  }
  const add = (text: string) => {
    const next = [...value]
    for (const m of text.split(/[\s,]+/)) {
      if (m && m !== exclude && !next.includes(m)) next.push(m)
    }
    onChange(next.slice(0, maxOfferedModels))
    setDraft("")
    // Closed, the list doesn't hide the tokens from a screen reader.
    suggested.onOpenChange(false)
    added.current = true
    setTimeout(() => (added.current = false))
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
        <Autocomplete.Root
          {...popup}
          value={draft}
          onValueChange={(v, details) => {
            // A suggestion picked from the list arrives whole.
            if (details.reason === "item-press" || /[\s,]/.test(v)) add(v)
            else setDraft(v)
          }}
        >
          <Autocomplete.Input
            id={id}
            autoComplete="off"
            spellCheck={false}
            maxLength={128}
            className="min-w-32 flex-1 bg-transparent px-1 font-mono text-xs outline-none placeholder:font-sans placeholder:text-muted-foreground"
            placeholder={value.length ? "" : placeholder}
            onKeyDown={(e) => {
              if (e.key === "Enter" && draft.trim()) {
                e.preventDefault()
                add(draft)
              } else if (
                e.key === "Backspace" &&
                draft === "" &&
                value.length
              ) {
                onChange(value.slice(0, -1))
              }
            }}
            onBlur={() => draft.trim() && add(draft)}
          />
          {suggestions.length > 0 && <ModelSuggestionList />}
        </Autocomplete.Root>
      )}
    </div>
  )
}
