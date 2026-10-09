export type AuthBootstrapResult = {
  authenticated: boolean
  error?: string
}

/** Read the launch token and remove its fragment before any route can load. */
function consumeLaunchToken(): string | null {
  const fragment = window.location.hash.slice(1)
  const params = new URLSearchParams(fragment)
  if (!params.has("studio-login")) return null

  const token = params.get("studio-login")
  window.history.replaceState(
    window.history.state,
    "",
    `${window.location.pathname}${window.location.search}`
  )
  return token
}

export function bootstrapAuth(): Promise<AuthBootstrapResult> {
  let token = consumeLaunchToken()

  return (async () => {
    let exchangeError: string | undefined
    try {
      if (token) await exchangeToken(token)
    } catch (error) {
      exchangeError = authErrorMessage(error)
    } finally {
      token = null
    }

    try {
      return { authenticated: await readAuthStatus(), error: exchangeError }
    } catch (error) {
      return { authenticated: false, error: authErrorMessage(error) }
    }
  })()
}

export async function exchangeToken(token: string): Promise<void> {
  let response: Response
  try {
    response = await fetch("/api/auth/exchange", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify({ token }),
    })
  } catch {
    throw new Error(
      "Could not reach Studio. Check the connection and try again."
    )
  }

  if (response.status === 401) {
    throw new Error(
      "That launch link or token is invalid or expired. Run `studio login-url` on the host to get a fresh link."
    )
  }
  if (!response.ok) {
    throw new Error(
      `Studio could not accept the login link (HTTP ${response.status}). Check the connection and try again.`
    )
  }
}

export async function readAuthStatus(): Promise<boolean> {
  let response: Response
  try {
    response = await fetch("/api/auth/status", {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    })
  } catch {
    throw new Error(
      "Could not reach Studio. Check the connection and try again."
    )
  }

  if (!response.ok) {
    throw new Error(
      `Studio could not check the login state (HTTP ${response.status}). Retry when the connection is available.`
    )
  }

  let result: unknown
  try {
    result = await response.json()
  } catch {
    throw new Error(
      "Studio returned an unreadable login status. Retry the connection."
    )
  }

  if (
    !result ||
    typeof result !== "object" ||
    typeof (result as { authenticated?: unknown }).authenticated !== "boolean"
  ) {
    throw new Error(
      "Studio returned an unreadable login status. Retry the connection."
    )
  }

  return (result as { authenticated: boolean }).authenticated
}

export function tokenFromLoginInput(value: string): string {
  const input = value.trim()
  if (!input) throw new Error("Paste a Studio launch link or token to connect.")

  if (/^(https?:\/\/|\/)/i.test(input)) {
    let url: URL
    try {
      url = new URL(input, window.location.origin)
    } catch {
      throw new Error("That does not look like a valid Studio launch link.")
    }
    const token = new URLSearchParams(url.hash.slice(1)).get("studio-login")
    if (!token) {
      throw new Error("The launch link is missing its studio-login token.")
    }
    return token
  }

  const fragment = input.startsWith("#") ? input.slice(1) : input
  if (fragment.startsWith("studio-login=")) {
    const token = new URLSearchParams(fragment).get("studio-login")
    if (token) return token
  }
  return input
}

function authErrorMessage(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "Studio could not complete authentication. Check the connection and try again."
}
