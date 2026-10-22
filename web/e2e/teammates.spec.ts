import { addTeammate, expect, sandboxName, test } from "./fixtures"

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

test("the rail shows a teammate's computer, and its menu forks it or adds another", async ({
  page,
  api,
  env,
}) => {
  const name = `Rail ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  await page.goto(`/e/${env}/teammates/${id}`)
  const rail = page.getByRole("complementary")
  await expect(
    rail.getByText(`None yet. ${name} works in a sandbox`)
  ).toBeVisible()
  await rail.getByRole("button", { name: "Give it a sandbox" }).click()
  await expect(page.getByRole("dialog", { name: "New sandbox" })).toBeVisible()
  await page.keyboard.press("Escape")

  const sandbox = sandboxName("rail")
  await api.createSandbox(sandbox, id)
  await expect(rail.getByRole("heading", { name: "Computer" })).toBeVisible()
  await expect(
    rail.getByRole("link", { name: new RegExp(sandbox) })
  ).toBeVisible()
  await rail
    .getByRole("button", { name: `More for ${name}'s sandboxes` })
    .click()
  await expect(
    page.getByRole("menuitem", { name: new RegExp(`^Fork ${sandbox}`) })
  ).toBeVisible()
  await page.getByRole("menuitem", { name: /^Add another sandbox/ }).click()
  await expect(page.getByRole("dialog", { name: "New sandbox" })).toBeVisible()
})

test("a teammate's settings have one save bar for the changes in every section, and leaving with changes asks", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Saver")
  const id = await addTeammate(api, env, name)
  const teammate = () =>
    api.json<{ role: string; gitName: string }>(
      "GET",
      `/api/environments/${env}/teammates/${id}`
    )
  await page.goto(`/e/${env}/teammates/${id}/settings?open=identity`)
  const bar = page.getByRole("region", { name: "Unsaved changes" })
  await expect(page.getByLabel("Role")).toBeVisible()
  await expect(bar).toBeHidden()

  // A change in one section, then another: opening a section keeps the first change.
  await page.getByLabel("Role").fill("Reviewer")
  await expect(bar).toBeVisible()
  await page.getByRole("button", { name: /^Git identity/ }).click()
  const gitName = await page.getByLabel("Git name").inputValue()
  await page.getByLabel("Git name").fill("Saver Bot")
  await bar.getByRole("button", { name: "Discard" }).click()
  await expect(bar).toBeHidden()
  await expect(page.getByLabel("Git name")).toHaveValue(gitName)

  await page.getByLabel("Git name").fill("Saver Bot")
  await page.getByRole("button", { name: /^Identity/ }).click()
  await page.getByLabel("Role").fill("Reviewer")
  await bar.getByRole("button", { name: "Save", exact: true }).click()
  await expect(bar).toBeHidden()
  expect(await teammate()).toMatchObject({
    role: "Reviewer",
    gitName: "Saver Bot",
  })

  // Leaving with a change asks; Stay keeps it, Leave goes.
  await page.getByLabel("Role").fill("Writer")
  await page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("link", { name: /^Home/ })
    .click()
  const ask = page.getByRole("alertdialog", { name: "Leave without saving?" })
  await ask.getByRole("button", { name: "Stay" }).click()
  await expect(page.getByLabel("Role")).toHaveValue("Writer")
  await page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("link", { name: /^Home/ })
    .click()
  await ask.getByRole("button", { name: "Leave" }).click()
  await expect(
    page.getByRole("heading", { name: "Home", level: 1 })
  ).toBeVisible()
  expect((await teammate())?.role).toBe("Reviewer")
})

test("a teammate's security level is a card, or the environment's default", async ({
  page,
  api,
  env,
}) => {
  const id = await addTeammate(api, env, sandboxName("Leveled"))
  const level = () =>
    api
      .json<{ securityLevel: string }>(
        "GET",
        `/api/environments/${env}/teammates/${id}`
      )
      .then((t) => t!.securityLevel)
  await page.goto(`/e/${env}/teammates/${id}/settings?open=network`)
  const picker = page.getByRole("group", { name: "Security level" })
  await expect(
    picker.getByRole("radio", { name: /^Environment default/ })
  ).toBeChecked()
  await picker.getByRole("radio", { name: "Locked", exact: true }).click()
  await expect(
    picker.getByRole("radio", { name: "Locked", exact: true })
  ).toBeChecked()
  await expect.poll(level).toBe("locked")
  await picker.getByRole("radio", { name: /^Environment default/ }).click()
  await expect.poll(level).toBe("")
})
