import createFetchClient from "openapi-fetch"
import createClient from "openapi-react-query"
import type { components, paths } from "./schema.gen"

export const fetchClient = createFetchClient<paths>({
  baseUrl: window.location.origin,
})

// Includes direct fetchClient calls (such as the secret dialog), not only query hooks.
fetchClient.use({
  onResponse({ response }) {
    if (response.status === 401)
      window.dispatchEvent(new Event("studio-auth-expired"))
    return response
  },
})

/** Typed TanStack Query hooks for every API operation. */
export const $api = createClient(fetchClient)

export type Environment = components["schemas"]["Environment"]
export type Sandbox = components["schemas"]["View"]
export type SandboxStatus = Sandbox["status"]
export type Terminal = components["schemas"]["Session"]
export type PreviewPort = components["schemas"]["PreviewPort"]
export type Approval = components["schemas"]["ApprovalView"]
export type GitDetail = components["schemas"]["GitDetail"]
export type Forge = components["schemas"]["Forge"]
export type Rule = components["schemas"]["Rule"]
export type Connection = components["schemas"]["Conn"]
export type Secret = components["schemas"]["Secret"]
export type BuildJobSummary = components["schemas"]["BuildJobSummary"]
export type BuildJob = components["schemas"]["BuildJobView"]
export type BuildJobLog = components["schemas"]["BuildJobLog"]
export type Template = components["schemas"]["TemplateView"]
export type BuildJobStatus = BuildJobSummary["status"]
export type Provider = components["schemas"]["ProviderView"]
export type ProviderKind = Provider["kind"]
export type Persona = components["schemas"]["Persona"]
export type Harness = Persona["harness"]

/** The message of an API problem response, or of any thrown error. */
export function errorMessage(err: unknown): string {
  if (err && typeof err === "object") {
    const e = err as { detail?: string; title?: string; message?: string }
    return e.detail ?? e.message ?? e.title ?? "Something went wrong"
  }
  return String(err)
}

/** The HTTP status of an API problem response, if the error is one. */
export function errorStatus(err: unknown): number | undefined {
  return err && typeof err === "object"
    ? (err as { status?: number }).status
    : undefined
}

/** Whether a sandbox's guest agent is connected, so terminals and previews work. */
export function isReady(sb: Sandbox) {
  return sb.status === "running" && sb.agent !== undefined
}
