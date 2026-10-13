import { expect, test } from "@playwright/test"
import { startStudio, studioPort } from "./studio"

test("first start: the login link opens the guided setup, once per session", async ({
  browser,
}, testInfo) => {
  // A Studio of its own, on a port the worker Studios don't use: the first start is about
  // empty state.
  const studio = await startStudio(studioPort(testInfo.parallelIndex * 2 + 1))
  const context = await browser.newContext({ baseURL: studio.url })
  try {
    const page = await context.newPage()

    await page.goto("/")
    await expect(
      page.getByRole("heading", { name: "Connect to Studio" })
    ).toBeVisible()

    // The link Studio prints logs in; its ticket leaves the address bar.
    await page.goto("about:blank")
    await page.goto(await studio.loginURL())
    await expect(page).toHaveURL(/\/setup$/)
    await expect(
      page.getByRole("heading", { name: "Set up Studio" })
    ).toBeVisible()
    expect(page.url()).not.toContain("studio-login")

    // Finish later goes Home, where the setup card says what is left.
    await page.getByRole("link", { name: "Finish later" }).click()
    await expect(
      page.getByRole("heading", { name: "Home", level: 1 })
    ).toBeVisible()
    const card = page.getByRole("region", { name: "Finish setup" })
    await expect(card).toBeVisible()

    // The setup opens once per session: a reload of Home stays on Home.
    await page.reload()
    await expect(
      page.getByRole("heading", { name: "Home", level: 1 })
    ).toBeVisible()
    await expect(card).toBeVisible()

    await card.getByRole("button", { name: "Dismiss" }).click()
    await expect(card).toBeHidden()

    // Dismissed, a new session opens Home and not the setup.
    const next = await context.newPage()
    await next.goto("/")
    await expect(
      next.getByRole("heading", { name: "Home", level: 1 })
    ).toBeVisible()
    await expect(
      next.getByRole("region", { name: "Finish setup" })
    ).toBeHidden()
    await expect(next).not.toHaveURL(/\/setup$/)
  } finally {
    await context.close()
    await studio.stop()
  }
})
