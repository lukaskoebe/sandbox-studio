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

  const candidate = input.startsWith("//")
    ? `https:${input}`
    : webScheme
      ? input
      : `https://${input}`
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
