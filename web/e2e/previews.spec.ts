import { expect, sandboxName, test } from "./fixtures"

// An app's service worker that answers navigations from its cache must not keep a preview
// from logging in, and Open fresh removes it.
test("previews: a service worker can't swallow Studio's login, and Open fresh removes it", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("pwa"))
  const open = async (fresh = false) =>
    (await api.json<{ url: string }>(
      "POST",
      `/api/environments/${env}/sandboxes/${id}/previews/3000/open${fresh ? "?fresh=true" : ""}`
    ))!.url
  const login = await open()
  const origin = new URL(login).origin
  await page.goto(login)
  await expect(page.getByText(/Preview of sandbox/)).toBeVisible()

  await page.goto(`${origin}/pwa`)
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
  })
  await page.goto(`${origin}/elsewhere`)
  await expect(page.getByText("cached app")).toBeVisible()

  // Studio's own paths pass the worker: a bad ticket gets Studio's answer, not the cache.
  await page.goto(`${origin}/__studio/preview-login?token=bogus`)
  await expect(
    page.getByText("Open this preview from Sandbox Studio.")
  ).toBeVisible()

  await page.goto(await open(true))
  await expect(
    page.getByText(/Preview of sandbox .* path \/\s*$/)
  ).toBeVisible()
  expect(
    await page.evaluate(
      async () => (await navigator.serviceWorker.getRegistrations()).length
    )
  ).toBe(0)
})

// The chat's preview panel frames the preview cross-site, where Chromium sends a service
// worker no partitioned cookie: the app's worker must still install and serve, and Reload
// fresh remove it. The frame here is the panel's, without a chat to open it from.
test("previews: an app's service worker works in a preview framed by Studio", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("pwa"))
  const open = async (query: string) =>
    (await api.json<{ url: string }>(
      "POST",
      `/api/environments/${env}/sandboxes/${id}/previews/3000/open?${query}`
    ))!.url
  const login = await open("embed=true")
  const origin = new URL(login).origin
  await page.goto(`/e/${env}`)
  const show = (src: string) =>
    page.evaluate((src) => {
      let f = document.getElementById("pv") as HTMLIFrameElement | null
      if (!f) {
        f = document.createElement("iframe")
        f.id = "pv"
        f.style.cssText =
          "position:fixed;inset:0;width:100vw;height:100vh;z-index:99999;background:white"
        document.body.appendChild(f)
      }
      f.src = src
    }, src)
  const preview = () => page.frames().find((f) => f.url().startsWith(origin))!
  const frame = page.frameLocator("#pv")

  await show(login)
  await expect(frame.getByText(/Preview of sandbox/)).toBeVisible()
  await show(`${origin}/pwa`)
  await expect(frame.getByText("PWA")).toBeVisible()
  expect(
    await preview().evaluate(async () => {
      await navigator.serviceWorker.ready
      return true
    })
  ).toBe(true)
  await show(`${origin}/elsewhere`)
  await expect(frame.getByText("cached app")).toBeVisible()

  await show(await open("embed=true&fresh=true"))
  await expect(
    frame.getByText(/Preview of sandbox .* path \/\s*$/)
  ).toBeVisible()
  expect(
    await preview().evaluate(
      async () => (await navigator.serviceWorker.getRegistrations()).length
    )
  ).toBe(0)
})

// Previews are off until the user turns them on in Settings → General; the sandbox's
// workspace offers them only then.
test("previews: the workspace offers them once Settings turns them on", async ({
  page,
  api,
  env,
}) => {
  await api.json("PUT", "/api/system/previews", { enabled: false })
  try {
    const id = await api.createSandbox(sandboxName("previews-off"))
    await api.guestReady(id)
    await page.goto(`/e/${env}/sandboxes/${id}/work`)
    const toolbar = page.getByRole("group", {
      name: "Workspace tabs and tools",
    })
    await expect(
      toolbar.getByRole("button", { name: "New terminal" })
    ).toBeVisible()
    await expect(toolbar.getByRole("button", { name: "Previews" })).toHaveCount(
      0
    )

    await page.goto(`/e/${env}/settings/general`)
    const toggle = page.getByRole("switch", {
      name: "Offer previews of sandbox ports",
    })
    await expect(toggle).not.toBeChecked()
    await toggle.click()
    await expect(toggle).toBeChecked()
    await page.goto(`/e/${env}/sandboxes/${id}/work`)
    await expect(
      toolbar.getByRole("button", { name: "Previews" })
    ).toBeVisible()
  } finally {
    await api.json("PUT", "/api/system/previews", { enabled: false })
  }
})
