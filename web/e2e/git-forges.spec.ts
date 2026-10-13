import { expect, test } from "./fixtures"

// GitLab is a forge kind of its own: the Add forge dialog fills in its address and says which
// token scopes it needs.
test("the forge dialog offers GitLab with its own address and token scopes", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/git`)
  await page.getByRole("button", { name: "Add forge" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add forge" })
  await dialog.getByRole("combobox").filter({ hasText: "GitHub" }).click()
  await page.getByRole("option", { name: "GitLab", exact: true }).click()

  await expect(dialog.locator("#forge-url")).toHaveValue("https://gitlab.com")
  await expect(
    dialog.getByLabel("Short name", { exact: true })
  ).toHaveAttribute("placeholder", "gitlab")
  await expect(dialog).toContainText("api and write_repository scopes")
  await expect(dialog.getByLabel("Username", { exact: true })).toHaveCount(0)
})
