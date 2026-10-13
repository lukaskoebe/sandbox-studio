import { addTeammate, expect, test } from "./fixtures"

test("the sidebar opens a teammate, whose tabs each render", async ({
  page,
  api,
  env,
}) => {
  const name = `Nav ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)

  await page.goto(`/e/${env}`)
  await page.getByRole("link", { name, exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/teammates/${id}$`))
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible()

  await page.getByRole("tab", { name: "Chats" }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}/chats$`))
  await expect(page.getByRole("link", { name: "New chat" })).toBeVisible()

  await page.getByRole("tab", { name: "Memory" }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}/memory$`))
  await expect(page.getByRole("tab", { name: "Facts" })).toBeVisible()

  await page.getByRole("tab", { name: "Browser" }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}/browser$`))
  await expect(
    page.getByText("Each teammate has its own browser")
  ).toBeVisible()

  await page.getByRole("tab", { name: "Settings" }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}/settings$`))
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(name)
  await expect(
    page.getByRole("button", { name: "Delete teammate" })
  ).toBeVisible()

  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}$`))
  await expect(page.getByText("Git identity")).toBeVisible()
})

test("old addresses redirect to the pages that replaced them", async ({
  page,
  api,
  env,
}) => {
  const id = await addTeammate(
    api,
    env,
    `Old ${Math.random().toString(36).slice(2, 8)}`
  )

  await page.goto(`/e/${env}/browser?teammate=${id}`)
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}/browser$`))

  // Without a teammate the browser opens the first one's; the page is the same either way.
  await page.goto(`/e/${env}/browser`)
  await expect(page).toHaveURL(/\/teammates\/[^/]+\/browser$/)

  for (const [from, to] of [
    ["git", "settings/git"],
    ["providers", "settings/providers"],
    ["builds", "settings/templates"],
    ["settings", "settings/general"],
  ] as const) {
    await page.goto(`/e/${env}/${from}`)
    await expect(page).toHaveURL(new RegExp(`/e/${env}/${to}$`))
  }
})
