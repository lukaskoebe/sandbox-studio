export function normalizeBrowserAddress(raw: string): {
  value?: string
  error?: string
} {
  if (/\p{Cc}/u.test(raw))
    return { error: "Remove control characters from the address." }

  const input = raw.trim()
  if (!input) return { error: "Enter a website address." }

  const webScheme = /^https?:\/\//i.test(input)
  const hostWithPort =
    /^(?:localhost|(?:[a-z\d](?:[a-z\d-]*[a-z\d])?\.)+[a-z\d](?:[a-z\d-]*[a-z\d])?|\d{1,3}(?:\.\d{1,3}){3}|\[[\da-f:.]+\]):\d+(?:[/?#]|$)/i.test(
      input
    )
  if (/^https?:/i.test(input) && !webScheme)
    return { error: "Enter a complete http:// or https:// address." }
  if (/^[a-z][a-z\d+.-]*:/i.test(input) && !webScheme && !hostWithPort)
    return { error: "Only http and https websites are supported." }

  // Without a scheme an address opens as https, except a local one, which a dev server
  // answers without TLS, as Chrome's own address bar assumes.
  const candidate = input.startsWith("//")
    ? `https:${input}`
    : webScheme
      ? input
      : `${localAddress(input) ? "http" : "https"}://${input}`
  let parsed: URL
  try {
    parsed = new URL(candidate)
  } catch {
    return { error: "Enter a valid website address." }
  }

  if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
    return { error: "Only http and https websites are supported." }
  const authority = candidate.match(/^https?:\/\/([^/?#]*)/i)?.[1] ?? ""
  if (parsed.username || parsed.password || authority.includes("@"))
    return { error: "Addresses with usernames or passwords are not supported." }
  if (parsed.href.length > 4000)
    return { error: "Address must be 4,000 characters or fewer." }

  return { value: parsed.href }
}

/** Whether an address typed without a scheme names this machine: localhost, a name under
 * .localhost, a 127.x.x.x address or [::1]. */
function localAddress(input: string) {
  const host = input
    .replace(/^\/\//, "")
    .split(/[/?#]/, 1)[0]
    .replace(/:\d*$/, "")
    .toLowerCase()
  return (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    /^127(?:\.\d{1,3}){3}$/.test(host) ||
    host === "[::1]"
  )
}
