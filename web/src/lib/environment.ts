const key = "studio.environment"

/** The environment last opened in this browser; a per-viewer convenience only. */
export function lastEnvironment(): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

export function rememberEnvironment(id: string) {
  try {
    localStorage.setItem(key, id)
  } catch {
    // Storage can be unavailable (private windows); the URL still carries the environment.
  }
}
