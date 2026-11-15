import {
  BrainIcon,
  GlobeIcon,
  KeyIcon,
  ListBulletsIcon,
  type Icon,
} from "@phosphor-icons/react"
import type { environmentPages } from "@/components/settings/link"
import { cn } from "@/lib/utils"

const icons: Record<
  (typeof environmentPages)[number]["id"],
  { icon: Icon; color?: string }
> = {
  network: { icon: GlobeIcon, color: "text-(--acc-network)" },
  secrets: { icon: KeyIcon },
  memory: { icon: BrainIcon, color: "text-(--acc-memory)" },
  activity: { icon: ListBulletsIcon },
}

/** An environment page's icon, in its area's hue where it has one. */
export function EnvironmentIcon({
  page,
  className,
}: {
  page: (typeof environmentPages)[number]["id"]
  className?: string
}) {
  const { icon: PageIcon, color } = icons[page]
  return <PageIcon className={cn(color, className)} />
}
