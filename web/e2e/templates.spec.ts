import { expect, test } from "./fixtures"

test("the spec editor inserts the Studio dev example", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/templates`)
  await page.getByRole("button", { name: "New build" }).first().click()
  const editor = page.locator(".cm-content")
  // New builds start from the Web app example.
  await expect(editor).toContainText("postgresql-client")

  await page.getByRole("button", { name: "Insert example" }).click()
  await page.getByRole("menuitem", { name: "Studio dev" }).click()
  await expect(editor).toContainText("chromium")
  await expect(editor).toContainText('go: "1.26"')
  await expect(editor).not.toContainText("postgresql-client")
})
