import { useEffect, useState } from "react"
import { errorMessage, fetchClient } from "@/lib/api/client"

/**
 * The models an OpenAI-compatible endpoint lists, for suggesting them in a model field. Studio
 * asks the endpoint, so the key never reaches it from the browser. It asks once the base URL
 * and a key (typed, or the saved provider's) are set and have stayed the same for a moment.
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
  const ready =
    /^https:\/\/[^/]+\.[^/]+/.test(url) && (!!apiKey || !!providerId)
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
