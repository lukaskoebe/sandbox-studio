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

// Chromium fetches a module worker and its imports without cookies. The preview's pages get a
// script that hands the worker a capability in its URL, which the app never sees.
test("previews: a module service worker installs with its imports", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("mpwa"))
  const login = (await api.json<{ url: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/previews/3000/open`
  ))!.url
  const origin = new URL(login).origin
  await page.goto(login)
  await expect(page.getByText(/Preview of sandbox/)).toBeVisible()

  await page.goto(`${origin}/module-pwa`)
  await expect(page.getByText("Module PWA")).toBeVisible()
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
  })
  await expect(page.locator("body")).toHaveAttribute(
    "data-script-url",
    `${origin}/module-sw.js?dev-sw`
  )
  await page.goto(`${origin}/elsewhere`)
  await expect(page.getByText("cached module app")).toBeVisible()

  // The capability counts only on a worker's own fetch: a page's fetch without cookies that
  // carries it is still refused.
  expect(
    await page.evaluate(async () => {
      const patch = await (await fetch("/__studio/preview-sw.js")).text()
      const token = /t="([^"]+)"/.exec(patch)![1]
      const res = await fetch(`/module-sw.js?dev-sw&__studio_sw=${token}`, {
        credentials: "omit",
      })
      return res.status
    })
  ).toBe(403)
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

// The desktop app's webview keeps no cookie for a framed preview, so in the app the screen
// frames a host that carries the preview's key, which needs none.
test("previews: in the app, a framed preview's host carries its key instead of a cookie", async ({
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
    const id = await api.createSandbox(sandboxName("keyed"))
    await api.guestReady(id)
    await page.addInitScript(() => {
      Object.assign(window, {
        webkit: { messageHandlers: { external: { postMessage: () => {} } } },
      })
    })
    await page.goto(`/e/${env}/sandboxes/${id}?tab=preview`)
    const element = page.locator('iframe[title="Preview of port 3000"]')
    await expect(
      element.contentFrame().getByText(/Preview of sandbox/)
    ).toBeVisible()
    const frame = (await (await element.elementHandle()).contentFrame())!
    const host = new URL(frame.url()).host
    expect(host).toMatch(new RegExp(`^3000-${id}-[a-z2-7]{26}\\.localhost:`))
    // Without any cookie of the preview's, it still answers, and keeps the same host.
    await page.context().clearCookies({ domain: new URL(frame.url()).hostname })
    expect(
      await frame.evaluate(async () => (await fetch("/api/items")).status)
    ).toBe(200)
    expect(
      (await api.json<{ url: string }>(
        "POST",
        `/api/environments/${env}/sandboxes/${id}/previews/3000/open?embed=true&keyed=true`
      ))!.url
    ).toContain(`//${host}/`)
    // Another key gets nothing.
    const other = await page.context().newPage()
    const wrong = await other.goto(
      `http://3000-${id}-${"a".repeat(26)}.localhost:${new URL(frame.url()).port}/`
    )
    expect(wrong!.status()).toBe(403)
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

// An app behind HTTP basic auth asks in Studio's own dialog over the preview, not the browser's
// over all of Studio, and opens the page it was on once signed in.
test("previews: a basic auth sign-in is asked over the preview", async ({
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
    const id = await api.createSandbox(sandboxName("basic"))
    await api.guestReady(id)
    let native = 0
    page.on("dialog", () => native++)
    await page.goto(`/e/${env}/sandboxes/${id}?tab=preview`)
    const element = page.locator('iframe[title="Preview of port 3000"]')
    await expect(
      element.contentFrame().getByText(/Preview of sandbox/)
    ).toBeVisible()
    const frame = (await (await element.elementHandle()).contentFrame())!
    await frame.goto(`${new URL(frame.url()).origin}/private/area?x=1`)

    const dialog = page.getByRole("dialog", { name: "Sign in to port 3000" })
    await expect(dialog).toContainText("“Staging”")
    await dialog.getByLabel("Username").fill("e2e")
    await dialog.getByLabel("Password").fill("wrong")
    await dialog.getByRole("button", { name: "Sign in" }).click()
    await expect(dialog).toContainText("It didn’t take that sign-in")
    await expect(dialog.getByLabel("Username")).toHaveValue("e2e")
    await dialog.getByLabel("Password").fill("e2e")
    await dialog.getByRole("button", { name: "Sign in" }).click()
    await expect(dialog).toBeHidden()
    await expect(
      element.contentFrame().getByText("Signed in to /private/area")
    ).toBeVisible()
    expect(native).toBe(0)
  } finally {
    await api.json("PUT", "/api/system/previews", {
      enabled: previews!.enabled,
    })
  }
})

// The preview's address bar follows the page through Studio's bridge: links, pushState and
// Back. A typed path opens on the preview's own origin; a link to another site opens in a new
// tab instead of the frame, and one to another port of the sandbox switches the port.
test("previews: the address bar follows the page and keeps its links in the preview", async ({
  page,
  context,
  api,
  env,
}) => {
  const previews = await api.json<{ enabled: boolean }>(
    "GET",
    "/api/system/previews"
  )
  await api.json("PUT", "/api/system/previews", { enabled: true })
  await context.route("https://example.com/**", (route) =>
    route.fulfill({ contentType: "text/plain", body: "the docs" })
  )
  try {
    const id = await api.createSandbox(sandboxName("navbar"))
    await api.guestReady(id)
    await api.json("PUT", `/api/e2e/sandboxes/${id}/ports`, {
      ports: [3000, 5173],
    })
    await page.goto(`/e/${env}/sandboxes/${id}?tab=preview&port=3000`)
    const port = page.getByRole("combobox", { name: "Port" })
    await expect(port).toContainText("localhost:3000")
    const frame = page
      .locator('iframe[title="Preview of port 3000"]')
      .contentFrame()
    await expect(frame.getByText(/Preview of sandbox/)).toBeVisible()
    const path = page.getByLabel("Preview path")
    await expect(path).toHaveValue("/")

    // The root is plain text, without the bridge: a typed path loads the frame on it.
    await path.fill("nav/one")
    await path.press("Enter")
    await expect(
      frame.getByRole("heading", { name: "Nav page /nav/one" })
    ).toBeVisible()
    await expect(path).toHaveValue("/nav/one")

    await frame.getByRole("link", { name: "Two" }).click()
    await expect(path).toHaveValue("/nav/two?x=1")
    await frame.getByRole("button", { name: "Push" }).click()
    await expect(path).toHaveValue("/nav/pushed")
    await page.getByRole("button", { name: "Back", exact: true }).click()
    await expect(path).toHaveValue("/nav/two?x=1")
    await page.getByRole("button", { name: "Forward", exact: true }).click()
    await expect(path).toHaveValue("/nav/pushed")

    // With the bridge a typed path, or a whole address of the preview, goes there in the page.
    await path.fill("/nav/three")
    await path.press("Enter")
    await expect(
      frame.getByRole("heading", { name: "Nav page /nav/three" })
    ).toBeVisible()
    await expect(path).toHaveValue("/nav/three")

    await page
      .getByRole("button", { name: "Pages visited in this preview" })
      .click()
    const visited = page.getByRole("menuitem", { name: /^\/nav\/one/ })
    await expect(visited).toBeVisible()
    await expect(
      page.getByRole("menuitem", { name: /^Port 5173/ })
    ).toBeVisible()
    await visited.click()
    await expect(
      frame.getByRole("heading", { name: "Nav page /nav/one" })
    ).toBeVisible()

    const [docs] = await Promise.all([
      context.waitForEvent("page"),
      frame.getByRole("link", { name: "Docs" }).click(),
    ])
    await expect.poll(() => docs.url()).toBe("https://example.com/docs")
    await expect(
      page.getByText(
        "example.com isn't part of this preview. It opened in a new tab."
      )
    ).toBeVisible()
    await expect(
      frame.getByRole("heading", { name: "Nav page /nav/one" })
    ).toBeVisible()
    await docs.close()

    await frame.getByRole("link", { name: "Other port" }).click()
    await expect(port).toContainText("localhost:5173")
    await expect(
      page
        .locator('iframe[title="Preview of port 5173"]')
        .contentFrame()
        .getByText(/on port 5173, path \/from-link/)
    ).toBeVisible()
    await expect(path).toHaveValue("/from-link")
  } finally {
    await api.json("PUT", "/api/system/previews", {
      enabled: previews!.enabled,
    })
  }
})
