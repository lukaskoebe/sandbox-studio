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

  // Its chats are a page of their own, linked from the conversation's rail.
  await page.goto(`/e/${env}/teammates/${id}/chats`)
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
  // Each section is a card that opens to its fields, one at a time, kept in the URL.
  await page
    .getByRole("button", { name: `Identity ${name}`, exact: true })
    .click()
  await expect(page).toHaveURL(/[?&]open=identity/)
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(name)
  await page
    .getByRole("button", { name: new RegExp(`^Delete ${name}`) })
    .click()
  await expect(page.getByLabel("Name", { exact: true })).toBeHidden()
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

  // The pages that moved into Settings, and the settings dialog's ?settings= links, open
  // the section that replaced them.
  for (const [from, to, label] of [
    ["providers", "providers", "AI providers"],
    ["builds", "templates", "Templates"],
    ["secrets", "secrets", "Secrets"],
    ["memory", "memory", "Shared memory"],
    ["git", "activity?kind=git", "Activity"],
    ["settings", "general", "General"],
    ["settings/git", "forges", "Git forges"],
    ["?settings=git", "forges", "Git forges"],
    ["?settings=network", "ports", "Ports and LAN"],
    ["?settings=runtime", "runtime", "Runtime"],
  ] as const) {
    await page.goto(`/e/${env}${from.startsWith("?") ? "" : "/"}${from}`)
    await expect(page).toHaveURL(
      new RegExp(`/e/${env}/settings/${to.replace("?", "\\?")}$`)
    )
    await expect(
      page
        .getByRole("navigation", { name: "Settings" })
        .getByRole("link", { name: label, exact: true })
    ).toHaveAttribute("aria-current", "page")
  }
})

test("a page opened while Studio is down connects by itself once it answers", async ({
  page,
  env,
}) => {
  await page.route("**/api/auth/status", (route) => route.abort())
  await page.goto(`/e/${env}`)
  await expect(
    page.getByRole("heading", { name: "Connect to Studio" })
  ).toBeVisible()
  await expect(page.getByText("Could not reach Studio")).toBeVisible()

  await page.unroute("**/api/auth/status")
  await expect(
    page.getByRole("heading", { name: "Home", level: 1 })
  ).toBeVisible({ timeout: 10_000 })
})
