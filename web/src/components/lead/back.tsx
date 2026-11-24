import { useEffect } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import { ArrowLeftIcon } from "@phosphor-icons/react"
import { Kbd } from "@/components/ui/kbd"
import { $api } from "@/lib/api/client"

/**
 * "← Lead" above a page of Lead's, as "← Ada" is above a teammate's settings; Esc goes there
 * too, unless a field, dialog or menu has it.
 */
export function LeadBack({ env }: { env: string }) {
  const navigate = useNavigate()
  const name =
    $api.useQuery("get", "/api/environments/{env}/lead", {
      params: { path: { env } },
    }).data?.settings.name ?? "Lead"
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return
      const t = e.target as HTMLElement | null
      if (
        t?.closest(
          "input, textarea, select, [contenteditable], [role=dialog], [role=menu], [role=listbox]"
        ) ||
        document.querySelector(
          "[data-open][role=dialog], [role=menu]:not([data-closed])"
        )
      )
        return
      void navigate({ to: "/e/$env/lead", params: { env } })
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [env, navigate])
  return (
    <Link
      to="/e/$env/lead"
      params={{ env }}
      aria-label={`Back to ${name}`}
      className="-ml-1.5 inline-flex h-6 max-w-full items-center gap-1.5 rounded-md px-1.5 text-xs font-medium text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50"
    >
      <ArrowLeftIcon className="size-3.5 shrink-0" />
      <span className="truncate">{name}</span>
      <Kbd aria-hidden className="ml-0.5">
        Esc
      </Kbd>
    </Link>
  )
}
