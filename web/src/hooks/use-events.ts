import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"

/** The API queries each change topic makes stale. */
const stale: Record<string, string[]> = {
  approvals: ["/api/approvals", "/api/environments/{env}/approvals"],
  rules: ["/api/environments/{env}/rules"],
  secrets: ["/api/environments/{env}/secrets"],
  personas: [
    "/api/environments/{env}/personas",
    "/api/environments/{env}/providers",
  ],
  builds: [
    "/api/environments/{env}/builds",
    "/api/environments/{env}/builds/{id}",
    "/api/environments/{env}/builds/{id}/log",
  ],
}

/**
 * Keeps the API queries current from the server's change stream. Events say what changed,
 * so the affected queries refetch.
 */
export function useEvents() {
  const queryClient = useQueryClient()
  useEffect(() => {
    const source = new EventSource("/api/events")
    let opened = false
    source.onopen = () => {
      // The first open is the normal start. A later one follows an error, so events may have been missed.
      if (opened) queryClient.invalidateQueries()
      opened = true
    }
    source.onmessage = (msg: MessageEvent<string>) => {
      const change = JSON.parse(msg.data) as { topic: string }
      for (const path of stale[change.topic] ?? []) {
        queryClient.invalidateQueries({ queryKey: ["get", path] })
      }
    }
    return () => source.close()
  }, [queryClient])
}
