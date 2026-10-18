import { expect, test } from "./fixtures"

// The pages of one memory conflict and one template build, for an address that names
// nothing. The git push review's page is covered in git-review.spec.ts.

test("a memory conflict that doesn't exist says so", async ({ page, env }) => {
  await page.goto(`/e/${env}/memory/conflicts/no-such-conflict`)
  await expect(page.getByText("No such conflict")).toBeVisible()
  await page
    .getByRole("main")
    .getByRole("link", { name: "Memory", exact: true })
    .click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/memory/?$`))
})

test("a build that doesn't exist says so", async ({ page, env }) => {
  await page.goto(`/e/${env}/builds/no-such-build`)
  await expect(page.getByText("No such build")).toBeVisible()
  await page.getByRole("link", { name: "All builds" }).click()
  await expect(page).toHaveURL(
    new RegExp(`/e/${env}/builds/no-such-build\\?settings=templates$`)
  )
})
