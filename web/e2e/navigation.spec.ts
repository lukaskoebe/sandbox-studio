import { addTeammate, expect, test } from "./fixtures"

test("the sidebar opens a teammate, whose profile opens its tasks, memory and settings", async ({
  page,
  api,
  env,
}) => {
  const name = `Nav ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)

  await page.goto(`/e/${env}`)
  await page.getByRole("link").filter({ hasText: name }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/teammates/${id}$`))
  await expect(
    page.getByText(`This is where you and ${name} talk.`)
  ).toBeVisible()

  // The name opens the profile's card; its sections open as sheets, each kept in the URL.
  const profile = page.getByRole("button", { name: `${name}'s profile` })
  const card = page.getByRole("dialog", { name: `${name}'s profile` })
  await profile.click()
  await expect(page).toHaveURL(/[?&]profile=card/)
  await card.getByRole("button", { name: /^Tasks/ }).click()
  await expect(page).toHaveURL(/[?&]profile=tasks/)
  const tasks = page.getByRole("dialog", { name: `${name}'s tasks` })
  await expect(tasks).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(tasks).toBeHidden()

  await profile.click()
  await card.getByRole("button", { name: /^Memory/ }).click()
  await expect(page).toHaveURL(/[?&]profile=memory/)
  const memory = page.getByRole("dialog", { name: `What ${name} remembers` })
  await expect(memory.getByRole("tab", { name: "Facts" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(memory).toBeHidden()

  await profile.click()
  await card.getByRole("button", { name: "Settings", exact: true }).click()
  await expect(page).toHaveURL(/[?&]profile=settings/)
  const settings = page.getByRole("dialog", { name: `${name}'s settings` })
  // Each section is a card that opens to its fields, one at a time, kept in the URL.
  await settings
    .getByRole("button", { name: `Identity ${name}`, exact: true })
    .click()
  await expect(page).toHaveURL(/[?&]open=identity/)
  await expect(settings.getByLabel("Name", { exact: true })).toHaveValue(name)
  await settings
    .getByRole("button", { name: new RegExp(`^Delete ${name}`) })
    .click()
  await expect(settings.getByLabel("Name", { exact: true })).toBeHidden()
  await expect(
    settings.getByRole("button", { name: "Delete teammate" })
  ).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(settings).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/teammates/${id}$`))
})

test("teaching a teammate puts the fact on its home, where it can be forgotten", async ({
  page,
  api,
  env,
}) => {
  const name = `Teach ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  await page.goto(`/e/${env}/teammates/${id}`)

  await page.getByRole("button", { name: "Mode: Thread" }).click()
  await page.getByRole("menuitem", { name: /^Teach/ }).click()
  await expect(page.getByText(`Saved to ${name}'s memory`)).toBeVisible()
  const fact = "The staging database needs the VPN"
  await page.getByRole("textbox", { name: "Message" }).fill(fact)
  await page.getByRole("button", { name: "Send" }).click()

  const home = page.getByLabel(`Threads with ${name}`)
  const learned = home.getByText(
    "Learned from you that the staging database needs the VPN"
  )
  await expect(learned).toBeVisible()
  // The profile counts what it knows.
  await page.getByRole("button", { name: `${name}'s profile` }).click()
  await expect(
    page.getByRole("dialog", { name: `${name}'s profile` }).getByText(fact)
  ).toBeVisible()
  await page.keyboard.press("Escape")

  await home.getByRole("button", { name: "Forget" }).click()
  await expect(
    home.getByText("Forgot that the staging database needs the VPN")
  ).toBeVisible()
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
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}\\?tab=browser$`))

  // Without a teammate the browser opens the first one's; the page is the same either way.
  await page.goto(`/e/${env}/browser`)
  await expect(page).toHaveURL(/\/teammates\/[^/?]+\?tab=browser$/)

  // The teammate's tabs are its home, its profile's sheets and its screen's Browser tab.
  for (const [from, want] of [
    ["chats", {}],
    ["tasks", { profile: "tasks" }],
    ["memory", { profile: "memory" }],
    ["settings?open=identity", { profile: "settings", open: "identity" }],
    ["browser", { tab: "browser" }],
  ] as const) {
    await page.goto(`/e/${env}/teammates/${id}/${from}`)
    await expect(page).toHaveURL(new RegExp(`/teammates/${id}(\\?|$)`))
    expect(Object.fromEntries(new URL(page.url()).searchParams), from).toEqual(
      want
    )
  }

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
