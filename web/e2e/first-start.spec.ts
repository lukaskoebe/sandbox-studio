import { expect, test } from "@playwright/test"
import { startStudio, studioPort } from "./studio"

test("first start: login link, onboarding, provider, teammate and sandbox", async ({
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
    const onboarding = page.getByRole("dialog", {
      name: "How do you want to work with agents?",
    })
    await expect(onboarding).toBeVisible()
    expect(page.url()).not.toContain("studio-login")
    await onboarding.getByRole("button", { name: /^Chat/ }).click()
    await expect(onboarding).toBeHidden()

    await page.getByRole("link", { name: "Providers" }).click()
    await page.getByRole("button", { name: "Add provider" }).first().click()
    const provider = page.getByRole("dialog", { name: "Add provider" })
    await provider.getByLabel("Name", { exact: true }).fill("Anthropic")
    await provider
      .getByLabel("API key", { exact: true })
      .fill("sk-ant-e2e-fake")
    await provider.getByRole("button", { name: "Add provider" }).click()
    await expect(provider).toBeHidden()
    await expect(
      page.getByRole("cell", { name: "Anthropic", exact: true })
    ).toBeVisible()

    await page.getByRole("link", { name: "Teammates" }).click()
    await page.getByRole("button", { name: "Add a teammate" }).first().click()
    const teammate = page.getByRole("dialog", { name: "Add a teammate" })
    await teammate.getByLabel("Name", { exact: true }).fill("Ada")
    await teammate
      .getByLabel("Model", { exact: true })
      .fill("claude-sonnet-4-5")
    await teammate.getByRole("button", { name: "Add a teammate" }).click()
    await expect(teammate).toBeHidden()
    await expect(page.getByRole("cell", { name: "Ada" }).first()).toBeVisible()

    await page.getByRole("link", { name: "Overview" }).click()
    await page.getByRole("button", { name: "New sandbox" }).first().click()
    const create = page.getByRole("dialog", { name: "New sandbox" })
    await create.getByLabel("Name", { exact: true }).fill("first")
    await create.getByLabel("Teammate", { exact: true }).click()
    await page.getByRole("option", { name: "Ada" }).click()
    await create.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/sandboxes\/[a-z0-9]+$/)
    await expect(page.getByRole("heading", { name: "first" })).toBeVisible()
    await expect(page.getByTitle("microsandbox status: running")).toHaveText(
      "Running"
    )
  } finally {
    await context.close()
    await studio.stop()
  }
})
