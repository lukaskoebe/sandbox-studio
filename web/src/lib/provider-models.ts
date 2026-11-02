import { useEffect, useState } from "react"
import { errorMessage, fetchClient } from "@/lib/api/client"

/**
 * The models an OpenAI-compatible endpoint lists, for suggesting them in a model field. Studio
 * asks the endpoint, so the key never reaches it from the browser. It asks once the base URL
 * looks whole and has stayed the same for a moment, with the key typed or the saved provider's
 * when there is one: many local servers take none.
 * The request is sent directly: a query cache would keep a typed key.
 */
export function useProviderModels({
  env,
  baseUrl,
  apiKey,
  providerId,
}: {
  env: string
  baseUrl: string
  apiKey?: string
  /** A saved provider whose stored key lists, when no key is typed. */
  providerId?: string
}) {
  const [state, setState] = useState<{
    for: string
    models: string[]
    error?: string
  }>()
  const url = baseUrl.trim()
  const ready = endpointPlace(url) !== undefined
  const ask = ready ? JSON.stringify([url, apiKey ?? "", providerId ?? ""]) : ""

  useEffect(() => {
    if (!ask) return
    const abort = new AbortController()
    const timer = setTimeout(async () => {
      try {
        const res = await fetchClient.POST(
          "/api/environments/{env}/providers/models",
          {
            params: { path: { env } },
            body: {
              baseUrl: url,
              apiKey: apiKey || undefined,
              providerId: apiKey ? undefined : providerId,
            },
            signal: abort.signal,
          }
        )
        if (abort.signal.aborted) return
        setState(
          res.data
            ? { for: ask, models: res.data.models ?? [] }
            : { for: ask, models: [], error: errorMessage(res.error) }
        )
      } catch (err) {
        if (!abort.signal.aborted)
          setState({ for: ask, models: [], error: errorMessage(err) })
      }
    }, 600)
    return () => {
      clearTimeout(timer)
      abort.abort()
    }
  }, [ask, env, url, apiKey, providerId])

  const current = ask && state?.for === ask ? state : undefined
  return {
    models: current?.models ?? [],
    error: current?.error,
    loading: !!ask && !current,
  }
}

/** Where an OpenAI-compatible base URL's server is, as Studio reaches it. */
export type EndpointPlace = "public" | "computer" | "network"

/**
 * Where the server of url is, or undefined while the URL isn't whole yet. Studio decides for
 * real; this only picks the hints: https:// is a public host, http:// on localhost this
 * computer, and any other http:// the local network.
 */
export function endpointPlace(url: string): EndpointPlace | undefined {
  let u: URL
  try {
    u = new URL(url.trim())
  } catch {
    return undefined
  }
  const host = u.hostname
  const whole =
    host.includes(".") || host === "localhost" || host.startsWith("[")
  if (!whole) return undefined
  if (u.protocol === "https:") return "public"
  if (u.protocol !== "http:") return undefined
  if (host === "localhost" || host === "[::1]" || host.startsWith("127."))
    return "computer"
  return "network"
}
