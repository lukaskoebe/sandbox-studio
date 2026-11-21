import { expect, test } from "@playwright/test"
import { startStudio, studioPort } from "./studio"

// After a reboot with only SSH logins the OS keychain that holds the vault key stays locked.
// Studio used to exit, and under systemd it was restarted every 5 s; now it waits and says so.
test("Studio waits for a locked keychain, says so, and starts once it unlocks", async ({
  browser,
}, testInfo) => {
  const studio = await startStudio(studioPort(testInfo.parallelIndex * 2 + 1), {
    keychainLocked: true,
  })
  const context = await browser.newContext({ baseURL: studio.url })
  try {
    const page = await context.newPage()

    // Launch links still work while it waits, so the page has its session once Studio starts.
    await page.goto(await studio.loginURL())
    const heading = page.getByRole("heading", { name: "Unlock your keychain" })
    await expect(heading).toBeVisible()
    await expect(
      page.getByText(
        /Unlock it, such as by logging in on this machine's desktop/
      )
    ).toBeVisible()
    await expect(page.getByText(/not reachable \(e2e\)/)).toBeVisible()
    expect(page.url()).not.toContain("studio-login")

    // Retry while it is still locked keeps the page as it is.
    const retry = page.getByRole("button", { name: "Retry" })
    await retry.click()
    await expect(heading).toBeVisible()
    await expect(
      page.getByRole("status").filter({ hasText: "Retry asks" })
    ).toContainText("may show its password prompt on the desktop")

    // Unlocked, Retry starts Studio, and the page goes on with its session.
    await studio.unlockKeychain()
    await retry.click()
    await expect(page).toHaveURL(/\/setup$/, { timeout: 30_000 })
    await expect(
      page.getByRole("heading", { name: "Set up Studio" })
    ).toBeVisible()
  } finally {
    await context.close()
    await studio.stop()
  }
})
