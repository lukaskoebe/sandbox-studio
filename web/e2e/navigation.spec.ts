import {
  addTeammate,
  expect,
  sandboxName,
  teammateMenu,
  test,
} from "./fixtures"

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

  // The crumb row's menu of the teammate opens the profile's card; its sections open as sheets,
  // each kept in the URL.
  const profile = { click: () => teammateMenu(page, name, "Profile") }
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
  await expect(
    memory.getByRole("navigation", { name: "Memory index" })
  ).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(memory).toBeHidden()

  await profile.click()
  await card.getByRole("button", { name: "Settings", exact: true }).click()
  await expect(page).toHaveURL(/[?&]profile=settings/)
  const settings = page.getByRole("region", { name: `${name}'s settings` })
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

  // The menu's Settings opens them at once, and a row of the card opens the same row there.
  await teammateMenu(page, name, "Settings")
  await expect(page).toHaveURL(/[?&]profile=settings/)
  await expect(settings).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(settings).toBeHidden()
  await profile.click()
  await card.getByRole("button", { name: /^New hosts/ }).click()
  await expect(page).toHaveURL(/[?&]open=network/)
  await expect(
    settings.getByRole("button", { name: /^New hosts/ })
  ).toHaveAttribute("aria-expanded", "true")
})

test("a collapsed sidebar opens Home and every teammate", async ({
  page,
  api,
  env,
}) => {
  await addTeammate(api, env, `Nav ${Math.random().toString(36).slice(2, 8)}`)
  // A sandbox without a teammate is open.
  const id = await api.createSandbox(sandboxName("plain"))
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const sidebar = page.locator('[data-slot="sidebar"]')
  await expect(sidebar).toHaveAttribute("data-state", "expanded")
  // Dragged past its minimum, not Ctrl+B: the screen's terminal takes the shortcut once it
  // has focus.
  const edge = page.getByRole("separator", { name: "Resize the sidebar" })
  const box = (await edge.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + 200)
  await page.mouse.down()
  await page.mouse.move(box.x - 300, box.y + 200, { steps: 6 })
  await page.mouse.up()
  await expect(sidebar).toHaveAttribute("data-state", "collapsed")

  // The groups' hidden labels sit over the rows above them, and must not take their clicks.
  const rows = page.locator('[data-slot="sidebar-menu-button"]')
  await rows.and(page.locator('[href*="/teammates/"]')).last().click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/teammates/[^/?]+`))
  await rows.filter({ hasText: "Home" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}$`))
})

test("the sidebar's edge drags between its ends, remembers it, and double-click resets it", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}`)
  const sidebar = page.locator('[data-slot="sidebar-container"]')
  const edge = page.getByRole("separator", { name: "Resize the sidebar" })
  const width = async () => (await sidebar.boundingBox())!.width
  await expect.poll(width).toBe(256)
  const drag = async (dx: number) => {
    const box = (await edge.boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + 200)
    await page.mouse.down()
    await page.mouse.move(box.x + box.width / 2 + dx, box.y + 200, {
      steps: 6,
    })
    await page.mouse.up()
  }
  await drag(400)
  await expect.poll(width).toBe(360)
  await drag(-140)
  await expect.poll(width).toBe(220)
  await page.reload()
  await expect.poll(width).toBe(220)
  // With focus, the arrow keys move it.
  await edge.focus()
  await page.keyboard.press("ArrowRight")
  await expect.poll(width).toBe(236)
  await edge.dblclick()
  await expect.poll(width).toBe(256)
  // Enter folds it to its icons and opens it again.
  await edge.press("Enter")
  await expect(
    page.locator('[data-slot="sidebar"][data-state="collapsed"]')
  ).toHaveCount(1)
  await edge.press("Enter")
  await expect.poll(width).toBe(256)
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
  await teammateMenu(page, name, "Profile")
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
  // The environment's pages, which left Settings, are the sidebar's; ?settings= for one of them
  // and the old git page open it there.
  for (const [from, to, label] of [
    ["?settings=secrets", "secrets", "Secrets"],
    ["?settings=memory", "memory", "Memory"],
    ["git", "activity?kind=git", "Activity"],
  ] as const) {
    await page.goto(`/e/${env}${from.startsWith("?") ? "" : "/"}${from}`)
    await expect(page).toHaveURL(
      new RegExp(`/e/${env}/${to.replace("?", "\\?")}$`)
    )
    await expect(
      page
        .locator('[data-sidebar="sidebar"]')
        .getByRole("link", { name: label, exact: true })
    ).toHaveAttribute("data-active", "")
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

test("the conversation and the screen split where they are dragged to, and past its minimum the screen takes the page", async ({
  page,
  api,
  env,
}) => {
  const name = `Split ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const sb = await api.createSandbox(sandboxName("split"), id)
  await api.guestReady(sb)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${sb}/chats`
  ))!
  await page.setViewportSize({ width: 1400, height: 900 })
  // The desk's home is a narrower panel of its own.
  await page.goto(`/e/${env}/teammates/${id}?tab=terminal`)
  const home = page.getByRole("separator", { name: "Resize the home" })
  await expect(home).toBeVisible()
  const deskLeft = (await home.boundingBox())!.x
  await page.goto(`/e/${env}/teammates/${id}?thread=${chat.id}`)
  const edge = page.getByRole("separator", { name: "Resize the conversation" })
  await expect(edge).toBeVisible()
  const conversation = page.getByRole("textbox", { name: "Message" })
  const left = async () => (await edge.boundingBox())!.x
  const drag = async (dx: number) => {
    const box = (await edge.boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + 300)
    await page.mouse.down()
    await page.mouse.move(box.x + box.width / 2 + dx, box.y + 300, {
      steps: 6,
    })
    await page.mouse.up()
  }
  const before = await left()
  expect(before).toBeGreaterThan(deskLeft + 50)
  await drag(120)
  await expect.poll(left).toBeGreaterThan(before + 100)
  await page.reload()
  await expect.poll(left).toBeGreaterThan(before + 100)
  await edge.dblclick()
  await expect.poll(left).toBeLessThan(before + 4)
  await drag(-1000)
  await expect(page).toHaveURL(/[?&]full=1|[?&]full=true/)
  await expect(conversation).toBeHidden()
  await page.getByRole("button", { name: "Show the conversation" }).click()
  await expect(edge).toBeVisible()
})

test("the home's rim drags the screen out of its rail, over the page and back to the rail", async ({
  page,
  api,
  env,
}) => {
  const name = `Rim ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const sb = await api.createSandbox(sandboxName("rim"), id)
  await api.guestReady(sb)
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto(`/e/${env}/teammates/${id}`)
  const now = page.getByRole("region", { name: `${name} now` })
  await expect(now).toBeVisible()
  const edge = page.getByRole("separator", { name: "Resize the home" })
  const drag = async (dx: number) => {
    const box = (await edge.boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + 300)
    await page.mouse.down()
    await page.mouse.move(box.x + box.width / 2 + dx, box.y + 300, {
      steps: 12,
    })
    await page.mouse.up()
  }
  // A little out of the rail opens the desk: the home a panel, the screen beside it.
  await drag(-300)
  await expect(page).toHaveURL(/[?&]tab=terminal/)
  await expect(now).toBeHidden()
  // All the way left the screen takes the page; all the way right it folds to the rail again.
  await drag(-2000)
  await expect(page).toHaveURL(/[?&]full=/)
  await drag(2000)
  await expect(page).toHaveURL(/[?&]fold=/)
  await expect(now).toBeVisible()
})
