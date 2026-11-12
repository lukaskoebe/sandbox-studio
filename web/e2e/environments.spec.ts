import { Api, expect, test } from "./fixtures"

test("a new environment opens at once, and it can be renamed in Settings", async ({
  page,
  env,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  await page.goto(`/e/${env}`)
  const sidebar = page.locator("[data-sidebar=sidebar]").first()
  await sidebar.locator("[data-sidebar=menu-button]").first().click()
  await page.getByRole("menuitem", { name: "New environment" }).click()
  const dialog = page.getByRole("dialog", { name: "New environment" })
  await dialog.getByLabel("Name").fill(`Client ${suffix}`)
  await dialog.getByRole("button", { name: "Create" }).click()

  // The new environment's page, not Not found: its list was cached before it existed.
  await expect(page).not.toHaveURL(new RegExp(`/e/${env}(/|$)`))
  await expect(page.getByText("Not found")).toHaveCount(0)
  await expect(sidebar).toContainText(`Client ${suffix}`)

  const id = new URL(page.url()).pathname.split("/")[2]
  await page.goto(`/e/${id}/settings/general`)
  const field = page.getByRole("textbox", { name: "Environment name" })
  await expect(field).toHaveValue(`Client ${suffix}`)
  await field.fill(`Renamed ${suffix}`)
  await page.getByRole("button", { name: "Rename", exact: true }).click()
  await expect(sidebar).toContainText(`Renamed ${suffix}`)
  await expect(page).toHaveURL(new RegExp(`/e/${id}/settings/general`))
})

test("an environment is deleted with its computers after its name is typed", async ({
  page,
  api,
  env,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const created = await api.json<{ id: string }>("POST", "/api/environments", {
    name: `Gone ${suffix}`,
  })
  const id = created!.id
  const sandbox = await new Api(api.request, id).createSandbox(
    `doomed-${suffix}`
  )

  await page.goto(`/e/${id}/settings/general`)
  await page.getByRole("button", { name: "Delete…" }).click()
  const dialog = page.getByRole("dialog", { name: `Delete Gone ${suffix}?` })
  const confirm = dialog.getByRole("button", { name: "Delete environment" })
  await expect(confirm).toBeDisabled()
  await dialog.getByLabel("to confirm").fill(`Gone ${suffix}`)
  await confirm.click()

  // Studio moves to another environment, and nothing of the deleted one is left.
  await expect(page).not.toHaveURL(new RegExp(`/e/${id}(/|$)`))
  const sidebar = page.locator("[data-sidebar=sidebar]").first()
  await expect(sidebar).not.toContainText(`Gone ${suffix}`)
  const envs = await api.json<{ id: string }[]>("GET", "/api/environments")
  expect(envs!.map((e) => e.id)).not.toContain(id)
  const res = await api.request.get(
    `/api/environments/${id}/sandboxes/${sandbox}`
  )
  expect(res.status()).toBe(404)
  // The environment the tests share is still there.
  expect(envs!.map((e) => e.id)).toContain(env)
})

test("a page that doesn't exist leads back to Studio", async ({ page }) => {
  await page.goto("/e/no-such-environment")
  await expect(page.getByText("Not found")).toBeVisible()
  await page.getByRole("link", { name: "Back to Studio" }).click()
  await expect(page).toHaveURL(/\/e\/[^/]+/)
})
