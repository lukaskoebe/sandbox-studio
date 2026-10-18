import {
  ArrowDownIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  ArrowUpIcon,
  KeyboardIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"

const keyClassName = "h-9 min-w-9 touch-manipulation px-2"
const iconKeyClassName = "size-9 touch-manipulation p-0"

export function TerminalControls({
  disabled,
  ctrlArmed,
  onToggleCtrl,
  onInput,
  onFocus,
}: {
  disabled: boolean
  ctrlArmed: boolean
  onToggleCtrl: () => void
  onInput: (data: string) => void
  onFocus: () => void
}) {
  return (
    <div
      role="group"
      aria-label="Terminal keys"
      className="flex shrink-0 items-center justify-between gap-1 overflow-x-auto overscroll-x-contain border-t border-[#2a3035] px-1 pt-1 pb-[env(safe-area-inset-bottom)] md:hidden [@media(pointer:coarse)]:!flex"
    >
      <KeyButton
        aria-label="Control key. Applies to the next key."
        aria-pressed={ctrlArmed}
        className={keyClassName}
        disabled={disabled}
        onClick={onToggleCtrl}
        title="Control key (next key)"
        variant={ctrlArmed ? "outline" : "secondary"}
      >
        Ctrl
      </KeyButton>
      <KeyButton
        aria-label="Escape"
        className={keyClassName}
        disabled={disabled}
        onClick={() => onInput("\x1b")}
        title="Escape"
      >
        Esc
      </KeyButton>
      <KeyButton
        aria-label="Tab"
        className={keyClassName}
        disabled={disabled}
        onClick={() => onInput("\t")}
        title="Tab"
      >
        Tab
      </KeyButton>
      <div className="grid shrink-0 grid-cols-3 grid-rows-2 gap-0.5">
        <span aria-hidden="true" />
        <KeyButton
          aria-label="Arrow up"
          className={iconKeyClassName}
          disabled={disabled}
          onClick={() => onInput("\x1b[A")}
          title="Arrow up"
        >
          <ArrowUpIcon />
        </KeyButton>
        <span aria-hidden="true" />
        <KeyButton
          aria-label="Arrow left"
          className={iconKeyClassName}
          disabled={disabled}
          onClick={() => onInput("\x1b[D")}
          title="Arrow left"
        >
          <ArrowLeftIcon />
        </KeyButton>
        <KeyButton
          aria-label="Arrow down"
          className={iconKeyClassName}
          disabled={disabled}
          onClick={() => onInput("\x1b[B")}
          title="Arrow down"
        >
          <ArrowDownIcon />
        </KeyButton>
        <KeyButton
          aria-label="Arrow right"
          className={iconKeyClassName}
          disabled={disabled}
          onClick={() => onInput("\x1b[C")}
          title="Arrow right"
        >
          <ArrowRightIcon />
        </KeyButton>
      </div>
      <KeyButton
        aria-label="Focus terminal keyboard"
        className={iconKeyClassName}
        disabled={disabled}
        onClick={onFocus}
        title="Show keyboard"
      >
        <KeyboardIcon />
      </KeyButton>
    </div>
  )
}

function KeyButton({
  className,
  children,
  ...props
}: React.ComponentProps<typeof Button>) {
  return (
    <Button
      type="button"
      variant="secondary"
      size="sm"
      className={className}
      {...props}
      onPointerDown={(event) => {
        event.preventDefault()
        props.onPointerDown?.(event)
      }}
      onMouseDown={(event) => {
        event.preventDefault()
        props.onMouseDown?.(event)
      }}
    >
      {children}
    </Button>
  )
}
