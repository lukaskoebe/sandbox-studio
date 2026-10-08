import createFetchClient from "openapi-fetch"
import createClient from "openapi-react-query"
import type { components, paths } from "./schema.gen"

export const fetchClient = createFetchClient<paths>({
  baseUrl: window.location.origin,
})

/** Typed TanStack Query hooks for every API operation. */
export const $api = createClient(fetchClient)

export type Environment = components["schemas"]["Environment"]
export type Sandbox = components["schemas"]["View"]
export type SandboxStatus = Sandbox["status"]
export type Terminal = components["schemas"]["Session"]
export type PreviewPort = components["schemas"]["PreviewPort"]

/** The message of an API problem response, or of any thrown error. */
export function errorMessage(err: unknown): string {
  if (err && typeof err === "object") {
    const e = err as { detail?: string; title?: string; message?: string }
    return e.detail ?? e.message ?? e.title ?? "Something went wrong"
  }
  return String(err)
}

/** Whether a sandbox's guest agent is connected, so terminals and previews work. */
export function isReady(sb: Sandbox) {
  return sb.status === "running" && sb.agent !== undefined
}
