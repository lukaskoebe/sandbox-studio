import { expect, test } from "./fixtures"

test("a teammate is copied to another environment from its options menu, and opens there without a provider", async ({
  page,
  api,
  env,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const name = `Copy ${suffix}`
  const providers = await api.json<{ id: string; harnesses: string[] }[]>(
    "GET",
    `/api/environments/${env}/providers`
  )
  const provider =
    providers?.at(0) ??
    (await api.json<{ id: string; harnesses: string[] }>(
      "POST",
      `/api/environments/${env}/providers`,
      { name: "Anthropic", kind: "anthropic_api", apiKey: "sk-ant-e2e-fake" }
    ))!
  const teammate = await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/teammates`,
    {
      name,
      harness: provider.harnesses.at(0) ?? "claude",
      providerId: provider.id,
      model: "claude-sonnet-5-5",
    }
  )
  // The target has no Anthropic provider, so the copy has none either.
  const target = await api.json<{ id: string; name: string }>(
    "POST",
    "/api/environments",
    { name: `Private ${suffix}` }
  )

  await page.goto(`/e/${env}/teammates/${teammate!.id}`)
  await page.getByRole("button", { name: "Teammate options" }).click()
  await page.getByRole("menuitem", { name: "Copy to environment…" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("combobox").click()
  await page.getByRole("option", { name: target!.name, exact: true }).click()
  await dialog.getByRole("button", { name: "Copy teammate" }).click()

  await page.getByRole("button", { name: "Open", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${target!.id}/teammates/[^/]+$`))
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible()
  await expect(page.getByText("No provider", { exact: true })).toBeVisible()
})

// Without a provider there is no model to choose, so adding a teammate says so and leads to the
// providers, from the sidebar and from the teammates page alike.
test("adding a teammate without a provider explains why and leads to the providers", async ({
  page,
  api,
}) => {
  const fresh = await api.json<{ id: string }>("POST", "/api/environments", {
    name: `Empty ${Math.random().toString(36).slice(2, 8)}`,
  })
  await page.goto(`/e/${fresh!.id}/teammates`)
  await page.getByTitle("New teammate").click()
  const dialog = page.getByRole("dialog", { name: "Add a provider first" })
  await expect(dialog.getByText(/this environment has none yet/)).toBeVisible()
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toBeHidden()

  await page.getByRole("button", { name: "Add a teammate" }).first().click()
  await dialog.getByRole("link", { name: "Add a provider" }).click()
  await expect(page).toHaveURL(
    new RegExp(`/e/${fresh!.id}/settings/providers$`)
  )
  await expect(dialog).toBeHidden()
})
