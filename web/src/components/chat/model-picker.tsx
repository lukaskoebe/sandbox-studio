import { CaretDownIcon } from "@phosphor-icons/react"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { components } from "@/lib/api/schema.gen"

type Teammate = components["schemas"]["Teammate"]

/** The models a teammate offers in chats besides its default; the picker shows only with some. */
export function offersModels(t: Teammate | undefined): boolean {
  return (t?.models?.length ?? 0) > 0
}

/** How a thread on model ("" for the teammate's default) names it. */
export function modelLabel(t: Teammate, model: string): string {
  return model || t.model || "Default model"
}

/**
 * Picks a thread's model among the teammate's default and the ones it also offers. value is ""
 * for the default; a switch applies from the next message.
 */
export function ModelPicker({
  teammate,
  value,
  onChange,
  disabled,
}: {
  teammate: Teammate
  value: string
  onChange: (model: string) => void
  disabled?: boolean
}) {
  if (!offersModels(teammate)) return null
  const current = value && teammate.models?.includes(value) ? value : ""
  const label = modelLabel(teammate, current)
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        aria-label={`Model: ${label}`}
        disabled={disabled}
        className="inline-flex h-6 max-w-48 min-w-0 shrink items-center gap-1 rounded-md px-1.5 text-foreground hover:bg-muted disabled:opacity-50 data-popup-open:bg-muted"
      >
        <span className="truncate font-mono text-caption">{label}</span>
        <CaretDownIcon className="size-2.5 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Model for this thread</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={current}
            onValueChange={(v: string) => onChange(v)}
          >
            {["", ...(teammate.models ?? [])].map((m) => (
              <DropdownMenuRadioItem
                key={m || "default"}
                value={m}
                closeOnClick
              >
                <span className="min-w-0 flex-1 truncate font-mono">
                  {modelLabel(teammate, m)}
                </span>
                {m === "" && (
                  <span className="text-caption text-muted-foreground">
                    default
                  </span>
                )}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
