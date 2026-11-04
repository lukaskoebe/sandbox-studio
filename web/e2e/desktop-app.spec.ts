import type { Page } from "@playwright/test"
import { expect, test } from "./fixtures"

test("app: in the Studio app's webview, new windows go to the app and the page keeps what it said", async ({
  page,
  env,
}) => {
  // The app's message handler, as WebKit gives it to the page; messages are recorded.
  await page.addInitScript(() => {
    const posted: string[] = []
    Object.assign(window, {
      posted,
      webkit: {
        messageHandlers: {
          external: { postMessage: (m: string) => posted.push(m) },
        },
      },
    })
  })
  const posted = () =>
    page.evaluate(() => (window as unknown as { posted: string[] }).posted)

  await page.goto(`/e/${env}?studio-app=notify`)
  await expect(page.getByRole("heading", { name: "Home" })).toBeVisible()
  // The parameter is kept for the session and dropped from the address.
  await expect(page).toHaveURL((u) => !u.searchParams.has("studio-app"))
  expect(await page.evaluate(() => sessionStorage.getItem("studio.app"))).toBe(
    "notify"
  )

  // window.open and links with a target or to other sites open in the system browser.
  expect(
    await page.evaluate(() => window.open("https://example.com/a") === null)
  ).toBe(true)
  await page.evaluate(() => {
    const a = document.createElement("a")
    a.href = "https://example.com/b"
    a.textContent = "elsewhere"
    document.body.append(a)
    a.click()
  })
  expect(await posted()).toEqual([
    "studio:open https://example.com/a",
    "studio:open https://example.com/b",
  ])
  await expect(page.getByRole("heading", { name: "Home" })).toBeVisible()

  // The system shows notifications, so Settings doesn't ask for the browser's.
  await page.goto(`/e/${env}/settings/general`)
  const row = page.getByRole("group", { name: "Notifications" })
  await expect(row).toContainText("On")
  await expect(row.getByRole("button")).toHaveCount(0)
})

/** The app's message handler, as WebKit gives it to the page; messages are recorded. */
async function inTheApp(page: Page) {
  await page.addInitScript(() => {
    const posted: string[] = []
    Object.assign(window, {
      posted,
      webkit: {
        messageHandlers: {
          external: { postMessage: (m: string) => posted.push(m) },
        },
      },
    })
  })
  return () =>
    page.evaluate(() => (window as unknown as { posted: string[] }).posted)
}

test("app: a frameless window's bar moves, resizes and closes it, and the page fits under it", async ({
  page,
  env,
}) => {
  const posted = await inTheApp(page)
  await page.goto(`/e/${env}?studio-app=1&studio-window=frameless`)
  await expect(page.getByRole("heading", { name: "Home" })).toBeVisible()
  await expect(page).toHaveURL((u) => !u.searchParams.has("studio-window"))

  const bar = page.getByTestId("window-bar")
  await expect(bar).toBeVisible()
  await bar.getByRole("button", { name: "Minimise" }).click()
  await bar.getByRole("button", { name: "Maximise" }).click()
  await bar.getByRole("button", { name: "Close" }).click()
  expect(await posted()).toEqual([
    "studio:window minimise",
    "studio:window maximise",
    "studio:window close",
  ])

  // A press on the bar moves the window once the pointer moves; a click alone doesn't.
  const box = (await bar.boundingBox())!
  const reset = () =>
    page.evaluate(() => {
      ;(window as unknown as { posted: string[] }).posted.length = 0
    })
  await reset()
  await page.mouse.move(box.x + 40, box.y + 10)
  await page.mouse.down()
  await page.mouse.up()
  expect(await posted()).toEqual([])
  await page.waitForTimeout(600) // past a double click
  await page.mouse.down()
  await page.mouse.move(box.x + 60, box.y + 12)
  await page.mouse.move(box.x + 80, box.y + 14)
  await page.mouse.up()
  expect(await posted()).toEqual(["wails:drag"])

  await reset()
  await bar.dblclick({ position: { x: 40, y: 10 } })
  expect(await posted()).toEqual(["studio:window maximise"])

  await reset()
  await page.getByTestId("window-edge-se").dispatchEvent("mousedown", {
    button: 0,
  })
  expect(await posted()).toEqual(["wails:resize:se-resize"])

  // The page takes the height that is left, without a scroll of its own.
  const inset = page.locator("[data-slot=sidebar-inset]")
  expect(await inset.evaluate((el) => el.scrollHeight <= el.clientHeight)).toBe(
    true
  )
  expect(await inset.evaluate((el) => el.clientHeight)).toBe(
    page.viewportSize()!.height
  )

  // The session keeps it, as the page moves on without the parameter.
  await page.goto(`/e/${env}/settings/general`)
  await expect(page.getByTestId("window-bar")).toBeVisible()
})

test("app: the window's bar is only in a window that needs one", async ({
  page,
  env,
}) => {
  // In a browser, whatever the address says.
  await page.goto(`/e/${env}?studio-window=frameless`)
  await expect(page.getByRole("heading", { name: "Home" })).toBeVisible()
  await expect(page.getByTestId("window-bar")).toHaveCount(0)
  await expect(page.getByTestId("window-edge-n")).toHaveCount(0)
})

test("app: on macOS the bar has room for the system's buttons, and none of its own", async ({
  page,
  env,
}) => {
  await inTheApp(page)
  await page.goto(`/e/${env}?studio-app=1&studio-window=inset`)
  await expect(page.getByRole("heading", { name: "Home" })).toBeVisible()
  const bar = page.getByTestId("window-bar")
  await expect(bar).toBeVisible()
  await expect(bar.getByRole("button")).toHaveCount(0)
  await expect(page.getByTestId("window-edge-n")).toHaveCount(0)
  // The sidebar starts below them.
  const header = page.locator("[data-slot=sidebar-header]").first()
  expect((await header.boundingBox())!.y).toBeGreaterThanOrEqual(28)
})
