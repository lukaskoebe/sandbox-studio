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

// A preview the screen shows logs in again while it stays open, so the app keeps working past
// its cookies' eight hours without a reload. Clearing them stands in for their expiry.
test("previews: a shown preview renews its login", async ({
  page,
  api,
  env,
}) => {
  const previews = await api.json<{ enabled: boolean }>(
    "GET",
    "/api/system/previews"
  )
  await api.json("PUT", "/api/system/previews", { enabled: true })
  try {
    const id = await api.createSandbox(sandboxName("renew"))
    await api.guestReady(id)
    await page.clock.install()
    await page.goto(`/e/${env}/sandboxes/${id}?tab=preview`)
    const element = page.locator('iframe[title="Preview of port 3000"]')
    await expect(
      element.contentFrame().getByText(/Preview of sandbox/)
    ).toBeVisible()
    const frame = (await (await element.elementHandle()).contentFrame())!
    const status = () =>
      frame.evaluate(async () => (await fetch("/api/items")).status)
    expect(await status()).toBe(200)

    await page.context().clearCookies({ domain: new URL(frame.url()).hostname })
    expect(await status()).toBe(403)
    await page.clock.fastForward("01:01:00")
    await expect.poll(status).toBe(200)
    await expect(
      page.locator('iframe[title="Renewing the preview of port 3000"]')
    ).toHaveCount(0)
  } finally {
    await api.json("PUT", "/api/system/previews", {
      enabled: previews!.enabled,
    })
  }
})

// Previews are off until the user turns them on in Settings → General; a computer's screen
// has a Preview tab only then.
test("previews: the screen offers them once Settings turns them on", async ({
  page,
  api,
  env,
}) => {
  await api.json("PUT", "/api/system/previews", { enabled: false })
  try {
    const name = sandboxName("previews-off")
    const id = await api.createSandbox(name)
    await api.guestReady(id)
    await page.goto(`/e/${env}/sandboxes/${id}`)
    const tabs = page.getByRole("tablist", { name: `${name}'s screen` })
    await expect(tabs.getByRole("tab", { name: "Terminal" })).toBeVisible()
    await expect(tabs.getByRole("tab", { name: "Preview" })).toHaveCount(0)

    await page.goto(`/e/${env}/settings/general`)
    const toggle = page.getByRole("switch", {
      name: "Offer previews of sandbox ports",
    })
    await expect(toggle).not.toBeChecked()
    await toggle.click()
    await expect(toggle).toBeChecked()
    await page.goto(`/e/${env}/sandboxes/${id}`)
    await expect(tabs.getByRole("tab", { name: "Preview" })).toBeVisible()
  } finally {
    await api.json("PUT", "/api/system/previews", { enabled: false })
  }
})
