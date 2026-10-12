import {
  DesktopIcon,
  MoonIcon,
  SunIcon,
  type Icon,
} from "@phosphor-icons/react"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { setThemeMode, useThemeMode, type ThemeMode } from "@/lib/theme"

const modes: { value: ThemeMode; label: string; icon: Icon }[] = [
  { value: "system", label: "System", icon: DesktopIcon },
  { value: "light", label: "Light", icon: SunIcon },
  { value: "dark", label: "Dark", icon: MoonIcon },
]

/**
 * Picks system, light or dark for this browser; lib/theme.ts stores the choice. Meant for the
 * sidebar's footer (docs/design/ui-style.md); nothing mounts it yet.
 */
export function ThemeToggle({ className }: { className?: string }) {
  const mode = useThemeMode()
  return (
    <ToggleGroup
      aria-label="Theme"
      variant="outline"
      size="sm"
      value={[mode]}
      onValueChange={(v) => {
        const next = modes.find((m) => m.value === v[0])
        if (next) setThemeMode(next.value)
      }}
      className={className}
    >
      {modes.map((m) => (
        <ToggleGroupItem
          key={m.value}
          value={m.value}
          aria-label={m.label}
          title={m.label}
        >
          <m.icon />
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}
