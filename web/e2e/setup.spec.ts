import { expect, test } from "@playwright/test"
import { startStudio, studioPort } from "./studio"

test("the guided setup walks its steps, with skips, to a first sandbox", async ({
  browser,
}, testInfo) => {
  // A Studio of its own, empty, as on a first start.
  const studio = await startStudio(studioPort(testInfo.parallelIndex * 2 + 1))
  const context = await browser.newContext({ baseURL: studio.url })
  try {
    const page = await context.newPage()
    await page.goto("about:blank")
    await page.goto(await studio.loginURL())
    await expect(page).toHaveURL(/\/setup$/)

    // Each step is a row; the open one shows its body and the buttons to move on.
    const row = (title: string) => page.getByRole("button", { name: title })
    const open = (title: string) =>
      expect(row(title)).toHaveAttribute("aria-expanded", "true")
    const next = () => page.getByRole("button", { name: "Next", exact: true })

    // Host check: done where the checks pass, as on a machine with the runtime; where they fail,
    // the step waits for a skip.
    await expect(row("Host check")).toBeVisible()
    if ((await row("Host check").getAttribute("aria-expanded")) === "true")
      await page.getByRole("button", { name: "Skip", exact: true }).click()

    // Work mode: the choice is saved as it is made, which makes the step done.
    await open("Work mode")
    await page.getByRole("button", { name: /^Terminal/ }).click()
    await expect(
      page.getByRole("button", { name: /^Terminal/ })
    ).toHaveAttribute("aria-pressed", "true")
    await next().click()

    // AI provider: an API key. The fake key is never sent anywhere in the e2e build.
    await open("AI provider")
    await page.getByRole("button", { name: "Add provider" }).click()
    const provider = page.getByRole("dialog", { name: "Add provider" })
    await provider.getByLabel("Name", { exact: true }).fill("Anthropic")
    await provider
      .getByLabel("API key", { exact: true })
      .fill("sk-ant-e2e-fake")
    await provider.getByRole("button", { name: "Add provider" }).click()
    await expect(provider).toBeHidden()
    await expect(page.getByText("Anthropic", { exact: true })).toBeVisible()
    await next().click()

    // Teammate: the first provider brought Iris along; accept her as she is.
    await open("Teammate")
    await expect(page.getByText("Iris", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "Accept", exact: true }).click()

    // Git: no forge yet, so leave it. Going Home in the middle shows where the setup stands,
    // and Continue opens it again on this step.
    await open("Git")
    await expect(page.getByText(/SSH agent/).first()).toBeVisible()
    await page.getByRole("link", { name: "Finish later" }).click()
    await expect(
      page.getByRole("heading", { name: "Home", level: 1 })
    ).toBeVisible()
    const card = page.getByRole("region", { name: "Finish setup" })
    await expect(card).toContainText("/7")
    await card.getByRole("link", { name: "Continue" }).click()
    await open("Git")
    await page.getByRole("button", { name: "Skip", exact: true }).click()

    // Network: going through it is the step; the approval flow is explained on its page.
    await open("Network")
    await expect(page.getByRole("link", { name: "Learn more" })).toBeVisible()
    await next().click()

    // First sandbox: created by Iris, with its progress shown until the workspace opens.
    await open("First sandbox")
    await page.getByLabel("Name", { exact: true }).fill("first")
    await page.getByRole("button", { name: "Create sandbox" }).click()
    await expect(
      page.getByRole("link", { name: "Open workspace" })
    ).toBeVisible({ timeout: 30_000 })
    await page.getByRole("link", { name: "Open workspace" }).click()
    await expect(page).toHaveURL(/\/sandboxes\/[a-z0-9]+\/work$/)

    // Every step is done or skipped now, so the setup is complete and Home has no card.
    await page.goto("/")
    await expect(
      page.getByRole("heading", { name: "Home", level: 1 })
    ).toBeVisible()
    await expect(
      page.getByRole("region", { name: "Finish setup" })
    ).toBeHidden()
    // The sandbox table's row: its name, ID, owner, template, status and ports.
    await expect(
      page.getByRole("link", { name: /^first \S+ Iris .*Running/ })
    ).toBeVisible()
  } finally {
    await context.close()
    await studio.stop()
  }
})
