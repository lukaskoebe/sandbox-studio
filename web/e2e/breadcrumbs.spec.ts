import {
  addTeammate,
  approvals,
  approvalsButton,
  expect,
  sandboxName,
  test,
} from "./fixtures"

test("the breadcrumb row says where a page is, and its parts open their neighbours", async ({
  page,
  api,
  env,
}) => {
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  const name = `Crumb ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)

  await page.goto(`/e/${env}`)
  await expect(crumbs.getByRole("listitem")).toHaveText(["Home"])

  // An environment page names its group, and switches to the others.
  await page.goto(`/e/${env}/network`)
  await expect(crumbs.getByRole("listitem")).toHaveText([
    "Environment",
    "Network",
  ])
  await crumbs.getByRole("button", { name: "Switch page: Network" }).click()
  await page.getByRole("menuitem", { name: "Secrets" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/secrets$`))

  // A settings section switches to the others, by their groups.
  await page.goto(`/e/${env}/settings/forges`)
  await crumbs
    .getByRole("button", { name: "Switch section: Git forges" })
    .click()
  await expect(page.getByRole("group", { name: "Accounts" })).toBeVisible()
  await page.getByRole("menuitem", { name: "Runtime" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/settings/runtime$`))

  // A teammate's page: the teammate, then its settings.
  await page.goto(`/e/${env}/teammates/${id}`)
  const teammate = crumbs.getByRole("button", {
    name: `Switch teammate: ${name}`,
  })
  await expect(teammate).toBeVisible()
  await teammate.click()
  await page.getByRole("menuitem", { name: "Settings" }).click()
  await expect(page).toHaveURL(/[?&]profile=settings/)
  await expect(
    page.getByRole("region", { name: `${name}'s settings` })
  ).toBeVisible()
  // The settings crumb opens their sections, by group.
  await crumbs.getByRole("button", { name: "Switch section: Settings" }).click()
  await expect(
    page.getByRole("group", { name: "Routine and access" })
  ).toBeVisible()
  await page.getByRole("menuitem", { name: "Check-ins" }).click()
  await expect(page).toHaveURL(/[?&]open=checkins/)
  // The teammate's crumb starts with its avatar's initials.
  await expect(crumbs.getByRole("listitem")).toHaveText([
    new RegExp(`${name}$`),
    "Settings",
    "Check-ins",
  ])
  await page.keyboard.press("Escape")

  // A thread: the teammate › the thread, and a chip for the computer it shows, whose menu has
  // the teammate's computers and all of them.
  const desk = sandboxName("desk")
  const deskId = await api.createSandbox(desk, id)
  await api.guestReady(deskId)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${deskId}/chats`
  ))!
  await page.goto(`/e/${env}/teammates/${id}?thread=${chat.id}`)
  // The teammate › the thread, its state, and its computer.
  await expect(crumbs.getByRole("listitem")).toHaveCount(4)
  await expect(
    crumbs.getByRole("button", { name: /^Thread options:/ })
  ).toBeVisible()
  await expect(crumbs.getByTestId("thread-status")).toHaveText("done")
  await expect(crumbs.getByRole("listitem").last()).toHaveText(
    new RegExp(`^on.*${desk}`)
  )
  await crumbs.getByRole("button", { name: `Switch computer: ${desk}` }).click()
  await expect(
    page.getByRole("menuitem", { name: `Give ${name} another computer…` })
  ).toBeVisible()
  await page.getByRole("menuitem", { name: "All computers" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/computers$`))
  await expect(crumbs.getByRole("listitem")).toHaveText([
    "Environment",
    "Computers",
  ])

  // The home names the teammate and what it does; its computers are on the page.
  await page.goto(`/e/${env}/teammates/${id}`)
  await expect(crumbs.getByRole("listitem")).toHaveText([
    new RegExp(`${name}$`),
    /^idle since /,
  ])

  // A computer of the user's own: Computers › the computer, which switches to the others.
  const computer = sandboxName("crumb")
  const sb = await api.createSandbox(computer)
  await page.goto(`/e/${env}/sandboxes/${sb}`)
  await expect(crumbs.getByRole("listitem")).toHaveText([
    "Computers",
    new RegExp(`${computer}$`),
  ])
  await crumbs
    .getByRole("button", { name: `Switch computer: ${computer}` })
    .click()
  await expect(
    page.getByRole("group", { name: "Your computers" })
  ).toBeVisible()
  await page.getByRole("menuitem", { name: "All computers" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/computers$`))
})

// The breadcrumb row and the sidebar's header are one band across the window.
// The teammate's crumb is a link home with its switcher on the caret; its settings are a step in
// the history, which their ← link, Esc and Back leave, whatever row is open.
test("a teammate's crumb goes home, and its settings lead back", async ({
  page,
  api,
  env,
}) => {
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  const name = `Back ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const sb = await api.createSandbox(sandboxName("back"), id)
  await api.guestReady(sb)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${sb}/chats`
  ))!
  const settings = page.getByRole("region", { name: `${name}'s settings` })
  const openSettings = async () => {
    await crumbs
      .getByRole("button", { name: `Switch teammate: ${name}` })
      .click()
    await page.getByRole("menuitem", { name: "Settings", exact: true }).click()
    await expect(settings).toBeVisible()
  }

  // From a thread, the name goes to the teammate's home.
  await page.goto(`/e/${env}/teammates/${id}?thread=${chat.id}`)
  await crumbs.getByRole("button", { name, exact: true }).click()
  await expect(page).not.toHaveURL(/[?&]thread=/)
  await expect(crumbs.getByRole("listitem")).toHaveText([
    new RegExp(`${name}$`),
    /^idle since /,
  ])

  // Back, after moving between rows, leaves the settings for where they were opened.
  await page.goto(`/e/${env}/teammates/${id}?thread=${chat.id}`)
  await openSettings()
  await crumbs.getByRole("button", { name: "Switch section: Settings" }).click()
  await page.getByRole("menuitem", { name: "Check-ins" }).click()
  await expect(page).toHaveURL(/[?&]open=checkins/)
  await page.goBack()
  await expect(settings).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`[?&]thread=${chat.id}`))

  // Its ← link goes back the same way, and so does Esc.
  await openSettings()
  await settings.getByRole("button", { name: `Back to ${name}` }).click()
  await expect(settings).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`[?&]thread=${chat.id}`))
  await openSettings()
  await page.keyboard.press("Escape")
  await expect(settings).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`[?&]thread=${chat.id}`))

  // Opened from a link, the ← link goes to the teammate, without a step to go back to.
  await page.goto(`/e/${env}/teammates/${id}?profile=settings`)
  await settings.getByRole("button", { name: `Back to ${name}` }).click()
  await expect(settings).toBeHidden()
  await expect(page).not.toHaveURL(/[?&]profile=/)
})

test("the breadcrumb row lines up with the sidebar's header", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/network`)
  const header = (await page.locator('[data-sidebar="header"]').boundingBox())!
  const row = (await page
    .getByRole("navigation", { name: "Where you are" })
    .boundingBox())!
  expect(row.y).toBe(header.y)
  expect(row.height).toBe(header.height)
})

test("on a phone the breadcrumb row holds the sidebar's toggle and search", async ({
  page,
  env,
}) => {
  await page.setViewportSize({ width: 390, height: 800 })
  await page.goto(`/e/${env}/network`)
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  await crumbs.getByRole("button", { name: "Search" }).click()
  await expect(
    page.getByRole("dialog", { name: "Search Studio" })
  ).toBeVisible()
  await page.keyboard.press("Escape")
  await crumbs.getByRole("button", { name: "Toggle Sidebar" }).click()
  await expect(
    page.locator('[data-sidebar="sidebar"]').getByRole("link", {
      name: "Secrets",
    })
  ).toBeVisible()
})

// The approvals wait in the crumb row at every width, so nothing floats over a composer's Send
// button, a terminal's last lines or a table; their list opens below the row, as a menu does.
for (const width of [390, 1280])
  test(`the approvals are a button in the breadcrumb row at ${width}px, with a compact list`, async ({
    page,
    api,
    env,
  }) => {
    await page.setViewportSize({ width, height: 800 })
    await api.dismissApprovals()
    await page.goto(`/e/${env}/secrets`)
    const crumbs = page.getByRole("navigation", { name: "Where you are" })
    const button = approvalsButton(page)
    await expect(crumbs).toBeVisible()
    await expect(button).toHaveCount(0)

    // One that arrives while the page is open flashes the count.
    const host = `crumb-${Math.random().toString(36).slice(2, 8)}.example.org`
    const sb = await api.createSandbox(sandboxName("crumbask"))
    await api.connect(sb, host)
    await expect(button).toHaveText(/^1 needs you$/, { timeout: 20_000 })
    await expect(button.locator("[data-status=waiting]")).toHaveClass(
      /animate-count-arrival/
    )

    await button.click()
    await expect(button).toHaveAttribute("aria-expanded", "true")
    const list = approvals(page)
    const row = list.getByTestId("approval-card").filter({ hasText: host })
    await expect(row).toBeVisible()
    const bar = (await crumbs.boundingBox())!
    // The list slides in from above: measure it where it settles.
    await expect
      .poll(async () => (await row.boundingBox())!.y)
      .toBeGreaterThanOrEqual(bar.y + bar.height - 0.5)
    await expect(list).toContainText("Choose a request to see all its choices.")
    // Escape closes it, as it does a menu.
    await page.keyboard.press("Escape")
    await expect(row).toBeHidden()

    // Allow in the row decides as the opened row would untouched.
    await button.click()
    await row.getByRole("button", { name: "Allow", exact: true }).click()
    await expect(button).toHaveCount(0)
    await expect
      .poll(async () =>
        (await api.guest(sb)).connections.find((c) => c.host === host)
      )
      .toMatchObject({ done: true, status: 200 })
  })

// Home lists the rows itself, so up there the button is only the count; a row that arrives
// slides into the list.
test("on Home the approvals' count opens nothing, and a new row slides in", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  await page.goto(`/e/${env}`)
  await expect(approvals(page)).toBeVisible()
  const host = `slide-${Math.random().toString(36).slice(2, 8)}.example.org`
  await api.connect(await api.createSandbox(sandboxName("slidein")), host)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card).toBeVisible({ timeout: 20_000 })
  await expect(card).toHaveClass(/animate-card-arrival/)
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  await expect(crumbs.getByText(/^1 needs you$/)).toBeVisible()
  await expect(approvalsButton(page)).toHaveCount(0)

  // Rows already waiting when the page loads don't move.
  await page.reload()
  await expect(card).toBeVisible()
  await expect(card).not.toHaveClass(/animate-card-arrival/)
  await api.dismissApprovals()
})
