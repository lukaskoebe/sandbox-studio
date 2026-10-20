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
  await expect(
    page.getByRole("main").getByRole("link", { name: "New chat" })
  ).toBeVisible()

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

  await page.getByRole("tab", { name: "Conversation" }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}$`))
  await expect(
    page.getByText(`Nothing between you and ${name} in the last two weeks.`)
  ).toBeVisible()
})

test("teaching a teammate puts the fact in the conversation, where it can be forgotten", async ({
  page,
  api,
  env,
}) => {
  const name = `Teach ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  await page.goto(`/e/${env}/teammates/${id}`)

  await page.getByRole("button", { name: "Teach", exact: true }).click()
  const fact = "The staging database needs the VPN"
  await page.getByRole("textbox", { name: "Message" }).fill(fact)
  await page.getByRole("button", { name: "Send" }).click()

  const thread = page.getByRole("region", { name: `Conversation with ${name}` })
  await expect(thread.getByText(fact, { exact: true })).toBeVisible()
  await expect(thread.getByText(/Learned from you/)).toBeVisible()
  // The rail counts what it knows.
  await expect(page.getByRole("complementary").getByText(fact)).toBeVisible()

  await thread.getByRole("button", { name: "Forget" }).click()
  await expect(thread.getByText(/Forgotten/)).toBeVisible()
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

  // Settings are a dialog over Home; the old pages' addresses open it there.
  for (const [from, to] of [
    ["providers", "providers"],
    ["builds", "templates"],
    ["settings", "general"],
    ["settings/git", "git"],
  ] as const) {
    await page.goto(`/e/${env}/${from}`)
    await expect(page).toHaveURL(new RegExp(`/e/${env}\\?settings=${to}$`))
    await expect(
      page
        .getByRole("navigation", { name: "Settings" })
        .getByRole("link", { name: to[0].toUpperCase() + to.slice(1) })
    ).toHaveAttribute("aria-current", "page")
  }
})
