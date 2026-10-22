import { toast } from "sonner"
import { errorMessage, fetchClient } from "@/lib/api/client"

/** Preview URLs live on *.localhost, which only resolves on the machine running Studio. */
export const onStudioMachine = ["localhost", "127.0.0.1", "[::1]"].includes(
  window.location.hostname
)

/**
 * How a preview opens. embed makes the preview cookie work inside an iframe. fresh clears the
 * preview's caches and storage first, service workers among them: a worker can keep serving
 * an app that no longer runs on the port. path is the page to land on, such as /login.
 */
export type PreviewOpen = { embed?: boolean; fresh?: boolean; path?: string }

/** The one-time login URL of a sandbox port's preview; every load needs a new one. */
export async function previewLoginURL(
  env: string,
  id: string,
  port: number,
  { embed = false, fresh = false, path }: PreviewOpen = {}
): Promise<string> {
  const { data, error, response } = await fetchClient.POST(
    "/api/environments/{env}/sandboxes/{id}/previews/{port}/open",
    {
      params: {
        path: { env, id, port },
        query:
          embed || fresh || path
            ? {
                ...(embed && { embed }),
                ...(fresh && { fresh }),
                ...(path && { path }),
              }
            : undefined,
      },
    }
  )
  if (response.status === 401) {
    throw new Error("Studio login expired. Reconnect, then try again.")
  }
  if (error) throw new Error(errorMessage(error))
  if (!data.url) throw new Error("Studio returned an invalid preview address.")
  const target = new URL(data.url, window.location.origin)
  if (target.protocol !== "http:" && target.protocol !== "https:") {
    throw new Error("Studio returned an invalid preview address.")
  }
  return target.href
}

/**
 * Opens a preview in a new tab. The tab opens first, while the click still counts as a
 * user gesture, and goes to the login URL once Studio returns it.
 */
export async function openPreviewTab(
  env: string,
  id: string,
  port: number,
  fresh = false,
  path?: string
) {
  let popup: Window | null = null
  try {
    popup = window.open("about:blank", "_blank")
  } catch {
    popup = null
  }
  if (!popup) {
    toast.error("Could not open preview", {
      description: "Allow pop-ups for Studio, then select this preview again.",
    })
    return
  }
  try {
    popup.opener = null
    popup.location.replace(
      await previewLoginURL(env, id, port, { fresh, path })
    )
  } catch (error) {
    try {
      popup.close()
    } catch {
      // The browser may already have closed the blank tab.
    }
    toast.error("Could not open preview", {
      description:
        error instanceof TypeError
          ? "Could not reach Studio. Check the connection and try again."
          : errorMessage(error),
    })
  }
}

/** Says what opening a preview fresh does, for its menu items and buttons. */
export const freshPreviewHint =
  "Clears the preview's caches, storage and service workers, then opens it"

/**
 * A link to a server in the sandbox as an agent writes it, such as localhost:5173 or
 * http://127.0.0.1:3000/login: the user's own localhost is not the sandbox, so Studio opens
 * these as previews.
 */
export const sandboxLocalLink =
  /^(?:https?:\/\/)?(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):(\d{1,5})(\/\S*)?$/i

/** The port and path of a link sandboxLocalLink matches, or undefined. */
export function parseSandboxLocalLink(
  text: string
): { port: number; path?: string } | undefined {
  const m = sandboxLocalLink.exec(text.trim())
  const port = Number(m?.[1])
  if (!m || port < 1 || port > 65535) return undefined
  return { port, path: m[2] && m[2] !== "/" ? m[2] : undefined }
}
