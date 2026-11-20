import {
  ArrowsOutIcon,
  BrowserIcon,
  DesktopIcon,
  DeviceMobileIcon,
  DeviceTabletIcon,
  LaptopIcon,
  RulerIcon,
  type Icon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  defaultPageSize,
  pageSizes,
  sizeName,
  type SizeName,
} from "@/lib/browser-fit"
import type { components } from "@/lib/api/schema.gen"
import { cn } from "@/lib/utils"

type Viewport = components["schemas"]["Viewport"]

/** What the page size menu offers: fitting the view, agent-browser's own size, or a kept one. */
export type SizeChoice = "fit" | "default" | SizeName

const named: { name: SizeName; label: string; icon: Icon }[] = [
  { name: "phone", label: "Phone", icon: DeviceMobileIcon },
  { name: "tablet", label: "Tablet", icon: DeviceTabletIcon },
  { name: "laptop", label: "Laptop", icon: LaptopIcon },
  { name: "desktop", label: "Desktop", icon: DesktopIcon },
]

const dims = (v: { width: number; height: number }) => `${v.width}×${v.height}`

/**
 * The page size of the browser, beside its address: fit to this view, as this browser
 * remembers, agent-browser's own 1280×720, or a phone's, a tablet's, a laptop's or a desktop's.
 * Those the session keeps, for everyone watching and for the agent's screenshots, and the view
 * scales them to fit; so does a size the agent chose with `studio browser viewport`.
 */
export function PageSizeMenu({
  name,
  fit,
  kept,
  disabled,
  onChoose,
}: {
  /** The teammate's name: its agents choose sizes as it. */
  name: string
  fit: boolean
  /** The size the session keeps, the user's or an agent's. */
  kept?: Viewport
  disabled?: boolean
  onChoose: (choice: SizeChoice) => void
}) {
  const keptName = kept ? sizeName(kept) : undefined
  const value = kept ? (keptName ?? "own") : fit ? "fit" : "default"
  const current = named.find((n) => n.name === keptName)
  const Shown =
    current?.icon ?? (kept ? RulerIcon : fit ? ArrowsOutIcon : BrowserIcon)
  const words = kept
    ? `${current?.label ?? "Page"} size, ${dims(kept)}${kept.by ? `, chosen by ${name}` : ""}`
    : fit
      ? "Fits this view"
      : `Standard size, ${dims(defaultPageSize)}`
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Page size: ${words}`}
            title={`Page size: ${words}`}
            disabled={disabled}
            className={cn(kept && "bg-muted")}
          />
        }
      >
        <Shown />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Page size</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={value}
            onValueChange={(v: string) => {
              if (v !== value && v !== "own") onChoose(v as SizeChoice)
            }}
          >
            <DropdownMenuRadioItem value="fit" closeOnClick>
              <ArrowsOutIcon />
              Fit to this view
            </DropdownMenuRadioItem>
            {named.map(({ name: size, label, icon: I }) => (
              <DropdownMenuRadioItem key={size} value={size} closeOnClick>
                <I />
                {label}
                <Dims>{dims(pageSizes[size])}</Dims>
              </DropdownMenuRadioItem>
            ))}
            <DropdownMenuRadioItem value="default" closeOnClick>
              <BrowserIcon />
              Standard
              <Dims>{dims(defaultPageSize)}</Dims>
            </DropdownMenuRadioItem>
            {value === "own" && kept && (
              <DropdownMenuRadioItem value="own" closeOnClick>
                <RulerIcon />
                {kept.by ? `${name}'s size` : "Its own size"}
                <Dims>{dims(kept)}</Dims>
              </DropdownMenuRadioItem>
            )}
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
        <p className="px-2 pt-1 pb-1.5 text-xs text-muted-foreground">
          {kept?.by
            ? `${name} chose this size. `
            : "A device's size stays for everyone watching, and for agents' screenshots, "}
          {kept?.by
            ? "It stays until the browser restarts or a size is chosen here."
            : "until the browser restarts."}
        </p>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Dims({ children }: { children: string }) {
  return (
    <span className="ml-auto text-muted-foreground tabular-nums">
      {children}
    </span>
  )
}
