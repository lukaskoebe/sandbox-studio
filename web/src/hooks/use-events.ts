import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"

/** The API queries each change topic makes stale. */
const stale: Record<string, string[]> = {
  approvals: [
    "/api/approvals",
    "/api/environments/{env}/approvals",
    "/api/environments/{env}/approvals/{id}",
  ],
  rules: ["/api/environments/{env}/rules"],
  secrets: ["/api/environments/{env}/secrets"],
  teammates: [
    "/api/environments/{env}/teammates",
    "/api/environments/{env}/providers",
  ],
  tasks: [
    "/api/environments/{env}/teammates/{id}/tasks",
    "/api/environments/{env}/teammates/{id}/heartbeats",
    "/api/environments/{env}/teammates/{id}/task-runs",
    "/api/environments/{env}/teammates/{id}/tasks/{task}/runs",
    "/api/environments/{env}/teammates/{id}/timeline",
  ],
  chats: [
    "/api/environments/{env}/chats",
    "/api/environments/{env}/sandboxes/{id}/chats",
    "/api/environments/{env}/teammates/{id}/timeline",
  ],
  sandboxes: [
    "/api/environments/{env}/sandboxes",
    "/api/environments/{env}/sandboxes/{id}",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    "/api/environments/{env}/sandboxes/{id}/checkpoint-schedule",
    "/api/environments/{env}/sandboxes/{id}/lineage",
    "/api/environments/{env}/sandboxes/{id}/merges",
    "/api/environments/{env}/merges/{merge}",
    "/api/environments/{env}/lineage",
  ],
  // A new step in the action log can come from a run that just started: the status too.
  browser: [
    "/api/environments/{env}/teammates/{teammate}/browser",
    "/api/environments/{env}/teammates/{teammate}/browser/actions",
  ],
  builds: [
    "/api/environments/{env}/builds",
    "/api/environments/{env}/builds/{id}",
    "/api/environments/{env}/builds/{id}/log",
  ],
}

/** The longest wait between two tries to reopen the stream. */
const maxBackoff = 30_000

/**
 * Keeps the API queries current from the server's change stream. Events say what changed,
 * so the affected queries refetch.
 *
 * EventSource retries a dropped connection by itself, but gives up for good after an answer
 * that isn't the stream, such as a 502 while Studio restarts. Then the stream is opened again
 * with a growing wait, and at once when the network or the tab comes back.
 */
export function useEvents() {
  const queryClient = useQueryClient()
  useEffect(() => {
    let source: EventSource | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let backoff = 1000
    let opened = false
    let closed = false

    const connect = () => {
      clearTimeout(retry)
      retry = undefined
      source?.close()
      source = new EventSource("/api/events")
      source.onopen = () => {
        backoff = 1000
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
      source.onerror = () => {
        if (closed || source?.readyState !== EventSource.CLOSED) return
        retry = setTimeout(connect, backoff)
        backoff = Math.min(backoff * 2, maxBackoff)
      }
    }
    // Back online or in view again: a stream that gave up opens now, not after its wait.
    const wake = () => {
      if (document.visibilityState !== "visible") return
      if (source?.readyState === EventSource.CLOSED) {
        backoff = 1000
        connect()
      }
    }
    connect()
    window.addEventListener("online", wake)
    document.addEventListener("visibilitychange", wake)
    return () => {
      closed = true
      clearTimeout(retry)
      source?.close()
      window.removeEventListener("online", wake)
      document.removeEventListener("visibilitychange", wake)
    }
  }, [queryClient])
}
