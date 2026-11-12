import { useState } from "react"
import { Autocomplete } from "@base-ui/react/autocomplete"
import { cn } from "@/lib/utils"

/**
 * The models a provider of kind is known to take on harness, for suggesting them. An
 * OpenAI-compatible endpoint lists its own (useProviderModels), so it has none here. The field
 * still takes any model the provider has.
 */
export function knownModels(kind?: string, harness?: string): string[] {
  switch (kind) {
    case "claude_subscription":
    case "anthropic_api": {
      const ids = [
        "claude-sonnet-5-5",
        "claude-opus-5-5",
        "claude-haiku-5-5",
        "claude-fable-5-2",
      ]
      // Claude Code also takes the families' names, which follow their newest model.
      return harness === "claude" ? [...ids, "sonnet", "opus", "haiku"] : ids
    }
    case "openai_api":
    case "chatgpt_subscription":
      return ["gpt-6.1-sol", "gpt-6-luna"]
  }
  return []
}

const inputClass =
  "h-7 w-full min-w-0 rounded-md border border-input bg-input/20 px-2 py-0.5 font-mono text-sm transition-colors outline-none placeholder:font-sans placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:pointer-events-none disabled:opacity-50 md:text-xs/relaxed dark:bg-input/30"

/**
 * The suggestions that match what is typed, other than itself, and the popup's open state: it
 * opens only when there is something to pick, so an empty one never covers the form. A field
 * that holds a suggestion offers all the others.
 */
export function useSuggestions(suggestions: string[], typed: string) {
  const [open, setOpen] = useState(false)
  const q = suggestions.includes(typed) ? "" : typed.trim().toLowerCase()
  const items = suggestions.filter(
    (m) => m !== typed && m.toLowerCase().includes(q)
  )
  return {
    items,
    filteredItems: items,
    open: open && items.length > 0,
    onOpenChange: setOpen,
    openOnInputClick: true,
  }
}

/**
 * The popup of suggestions under a model field, styled as the app's menus, which a native
 * datalist isn't: it follows the theme.
 */
export function ModelSuggestionList() {
  return (
    <Autocomplete.Portal>
      <Autocomplete.Positioner
        className="isolate z-50 outline-none"
        sideOffset={4}
        align="start"
      >
        <Autocomplete.Popup className="max-h-[min(var(--available-height),16rem)] w-(--anchor-width) min-w-48 overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none data-empty:hidden">
          <Autocomplete.List>
            {(m: string) => (
              <Autocomplete.Item
                key={m}
                value={m}
                className="flex min-h-7 cursor-default items-center rounded-md px-2 py-1 font-mono text-xs outline-hidden select-none data-highlighted:bg-accent data-highlighted:text-accent-foreground"
              >
                {m}
              </Autocomplete.Item>
            )}
          </Autocomplete.List>
        </Autocomplete.Popup>
      </Autocomplete.Positioner>
    </Autocomplete.Portal>
  )
}

/**
 * A model id with suggestions: it takes any text, and offers the suggestions that match what
 * is typed, or all of them on a click into an empty field.
 */
export function ModelInput({
  id,
  value,
  onChange,
  suggestions,
  placeholder,
  className,
  required,
  disabled,
  "aria-invalid": invalid,
}: {
  id: string
  value: string
  onChange: (model: string) => void
  suggestions: string[]
  placeholder?: string
  className?: string
  required?: boolean
  disabled?: boolean
  "aria-invalid"?: boolean
}) {
  const popup = useSuggestions(suggestions, value)
  return (
    <Autocomplete.Root
      {...popup}
      value={value}
      onValueChange={(v) => onChange(v)}
    >
      <Autocomplete.Input
        id={id}
        autoComplete="off"
        spellCheck={false}
        maxLength={128}
        placeholder={placeholder}
        required={required}
        disabled={disabled}
        aria-invalid={invalid}
        className={cn(inputClass, className)}
      />
      {suggestions.length > 0 && <ModelSuggestionList />}
    </Autocomplete.Root>
  )
}
