import { fetchClient, type Secret } from "@/lib/api/client"

/**
 * A teammate's browser logins are secrets of its own with browserOnly set: no sandbox sees
 * them. Studio fills them into pages of their hosts (fill_credential) and the gateway sends
 * them as HTTP Basic credentials to the hosts that ask, in that teammate's browser alone.
 */

/** Whether a host pattern of a secret covers host, as the gateway matches them. */
export function covers(pattern: string, host: string) {
  const h = host.toLowerCase().replace(/\.$/, "")
  if (pattern === "*") return true
  if (pattern.startsWith("*.")) {
    const suffix = pattern.slice(2)
    return h === suffix || h.endsWith("." + suffix)
  }
  return pattern === h
}

/** The teammate's browser logins for host, by name. */
export function loginsFor(list: Secret[], teammate: string, host: string) {
  return list
    .filter(
      (s) =>
        s.browserOnly &&
        s.teammateId === teammate &&
        (s.hosts ?? []).some((p) => covers(p, host))
    )
    .sort((a, b) => a.name.localeCompare(b.name))
}

/** A new login's name: the host in upper case, such as STAGING_EXAMPLE_COM, unique in taken. */
export function loginName(host: string, taken: Iterable<string>) {
  const names = new Set(taken)
  let base = host
    .replace(/^www\./, "")
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
  if (!base || /^[0-9]/.test(base)) base = "LOGIN_" + base
  base = base.slice(0, 60).replace(/_+$/, "")
  if (!names.has(base)) return base
  for (let n = 2; ; n++) {
    if (!names.has(`${base}_${n}`)) return `${base}_${n}`
  }
}

/** A login's password, read back from the vault for the user. */
export async function loginPassword(env: string, id: string) {
  const { data, error } = await fetchClient.GET(
    "/api/environments/{env}/secrets/{id}/value",
    { params: { path: { env, id } } }
  )
  if (error) throw error
  return data.value
}

// The hosts this computer was told never to offer saving a password for.
const neverKey = "studio.browser.never-save"

export function neverOffered(host: string) {
  try {
    const list: unknown = JSON.parse(localStorage.getItem(neverKey) ?? "[]")
    return Array.isArray(list) && list.includes(host)
  } catch {
    return false
  }
}

export function setNeverOffered(host: string) {
  try {
    const list: unknown = JSON.parse(localStorage.getItem(neverKey) ?? "[]")
    const hosts = Array.isArray(list) ? list.filter((h) => h !== host) : []
    localStorage.setItem(neverKey, JSON.stringify([...hosts, host].slice(-200)))
  } catch {
    // Without storage, the card shows again next time.
  }
}

/** A password the user typed or pasted into a page, here on this computer only. */
export type TypedPassword = { host: string; password: string }

/**
 * Follows what the user types into a password field of the live view, which stays on this
 * computer, so the window can offer to save it as a browser login once the field is left.
 * A press on the page starts a field, which counts once the page says it is a password field;
 * an edit it can't follow, such as moving the caret, drops it.
 */
export class PasswordCapture {
  private field?: {
    host: string
    text: string
    password: boolean
    lost: boolean
    id: number
  }
  private next = 1

  /** A press on the page at host: the field it lands in, if any, is told by confirm. */
  press(host: string | undefined) {
    const done = this.finish()
    const id = this.next++
    this.field = host
      ? { host, text: "", password: false, lost: false, id }
      : undefined
    return { id, done }
  }

  /** The page's answer for press id: whether it landed in a password field. */
  confirm(id: number, password: boolean) {
    if (this.field?.id !== id) return
    if (password) this.field.password = true
    else this.field = undefined
  }

  type(text: string) {
    if (this.field) this.field.text += text
  }

  backspace() {
    if (this.field) this.field.text = [...this.field.text].slice(0, -1).join("")
  }

  /** An edit that can't be followed: the field is dropped. */
  lose() {
    if (this.field) this.field.lost = true
  }

  /** The field is left: its password, if it was one and was followed. */
  finish(): TypedPassword | undefined {
    const f = this.field
    this.field = undefined
    if (!f || !f.password || f.lost || !f.text) return undefined
    return { host: f.host, password: f.text }
  }
}
