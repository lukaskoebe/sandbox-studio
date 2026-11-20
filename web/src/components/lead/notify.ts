import { useEffect, useRef } from "react"
import { useNavigate } from "@tanstack/react-router"
import { $api } from "@/lib/api/client"
import { appNotifies, notifyApp } from "@/lib/desktop"
import { plural } from "@/lib/lead"
import { shouldNotify } from "@/lib/notifications"

/**
 * Tells you when Lead has written today's day while you look elsewhere (docs/lead.md), as its
 * settings say: only when something waits for you, every time, or never. A day already
 * written when the page loads isn't news.
 */
export function useLeadNotify(env: string) {
  const navigate = useNavigate()
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const seen = useRef<string | null | undefined>(undefined)
  const v = lead.data
  const written = v?.today.writtenAt ?? null
  useEffect(() => {
    if (!v) return
    const before = seen.current
    seen.current = written
    if (before === undefined || !written || written === before) return
    const { notify, name } = v.settings
    const waiting = v.today.waiting
    if (notify === "never" || (notify === "waiting" && waiting === 0)) return
    if (!shouldNotify()) return
    const title =
      waiting > 0
        ? `Your day is written · ${plural(waiting, "thing waits", "things wait")} for you`
        : "Your day is written"
    const body = v.today.summary || `${name} wrote up today.`
    const id = `lead-${env}-${v.today.day}`
    try {
      if (appNotifies) {
        notifyApp({ id, title, body })
        return
      }
      const n = new Notification(title, { body, tag: `${id}-${written}` })
      n.onclick = () => {
        window.focus()
        void navigate({ to: "/e/$env/lead", params: { env } })
        n.close()
      }
    } catch {
      // Some browsers only show notifications from a service worker; Home shows the day.
    }
  }, [env, navigate, v, written])
}
