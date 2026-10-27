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
