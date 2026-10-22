import { expect, sandboxName, test } from "./fixtures"

test("notifications: Home asks once, and Settings → General has the button", async ({
  page,
  context,
  env,
}) => {
  await page.goto(`/e/${env}`)
  const banner = page.getByRole("region", { name: "Notifications" })
  await expect(
    banner.getByRole("button", { name: "Enable notifications" })
  ).toBeVisible()
  await banner.getByRole("button", { name: "Dismiss" }).click()
  await expect(banner).toBeHidden()
  await page.reload()
  await expect(page.getByRole("heading", { name: "Home" })).toBeVisible()
  await expect(banner).toBeHidden()

  await page.goto(`/e/${env}/settings/general`)
  const row = page.getByRole("group", { name: "Notifications" })
  await expect(
    row.getByRole("button", { name: "Enable notifications" })
  ).toBeVisible()
  await context.grantPermissions(["notifications"])
  await page.reload()
  await expect(row).toContainText("On")
  await expect(row.getByRole("button")).toHaveCount(0)
})

test("notifications: an approval notifies while Studio's window is not in front, though the tab is visible", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  // A browser window behind another app: visible, but without focus. Notifications are recorded.
  await page.addInitScript(() => {
    const shown: string[] = []
    Object.assign(window, { shown })
    document.hasFocus = () => false
    class Recorded {
      static permission = "granted"
      static requestPermission = () => Promise.resolve("granted")
      onclick: (() => void) | null = null
      constructor(title: string, options?: { body?: string }) {
        shown.push(`${title}: ${options?.body ?? ""}`)
      }
      close() {}
    }
    Object.assign(window, { Notification: Recorded })
  })
  const id = await api.createSandbox(sandboxName("notify"))
  const host = `notify-${Math.random().toString(36).slice(2, 8)}.example.org`
  await page.goto(`/e/${env}/settings/secrets`)
  await expect(page.getByRole("heading", { name: "Secrets" })).toBeVisible()
  await api.connect(id, host)
  await expect
    .poll(() =>
      page.evaluate(() => (window as unknown as { shown: string[] }).shown)
    )
    .toEqual([expect.stringContaining(host)])
})
