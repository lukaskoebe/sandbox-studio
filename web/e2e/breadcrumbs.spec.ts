import { addTeammate, approvals, expect, sandboxName, test } from "./fixtures"

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
  await expect(crumbs.getByRole("listitem")).toHaveCount(3)
  await expect(
    crumbs.getByRole("button", { name: /^Switch thread:/ })
  ).toBeVisible()
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

  // The home names the teammate alone; its computers are on the page.
  await page.goto(`/e/${env}/teammates/${id}`)
  await expect(crumbs.getByRole("listitem")).toHaveText([
    new RegExp(`${name}$`),
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

// The approvals wait in the crumb row on a phone, so no floating pill covers a composer's send
// button or the terminal's keys; their cards open below the row.
test("on a phone the approvals are a button in the breadcrumb row", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 390, height: 800 })
  await api.dismissApprovals()
  const host = `crumb-${Math.random().toString(36).slice(2, 8)}.example.org`
  await api.connect(await api.createSandbox(sandboxName("crumbask")), host)
  await page.goto(`/e/${env}/secrets`)
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  const button = crumbs.getByRole("button", { name: /^\d+ approvals?$/ })
  await expect(button).toBeVisible()
  await expect(
    approvals(page).getByRole("button", { name: /^\d+ approvals?$/ })
  ).toBeHidden()

  await button.click()
  await expect(button).toHaveAttribute("aria-expanded", "true")
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card).toBeVisible()
  const row = (await crumbs.boundingBox())!
  expect((await card.boundingBox())!.y).toBeGreaterThanOrEqual(
    row.y + row.height
  )
  await button.click()
  await expect(card).toBeHidden()
  await api.dismissApprovals()
})

// On a wide page the stack floats in the corner, and above the composer when there is one, so
// its cards never cover the Send button.
test("the approval stack sits above a composer, not over its Send button", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const name = `Stack ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const host = `stack-${Math.random().toString(36).slice(2, 8)}.example.org`
  await api.connect(await api.createSandbox(sandboxName("stackask")), host)

  const stack = approvals(page)
  await page.goto(`/e/${env}/secrets`)
  await expect(stack).toBeVisible()
  await expect
    .poll(async () =>
      Math.round(
        800 -
          (await stack.boundingBox())!.y -
          (await stack.boundingBox())!.height
      )
    )
    .toBe(16)

  await page.goto(`/e/${env}/teammates/${id}`)
  const send = page.getByRole("button", { name: "Send" })
  await expect(send).toBeVisible()
  await expect(stack).toBeVisible()
  const top = (await send.boundingBox())!.y
  await expect
    .poll(async () => {
      const box = (await stack.boundingBox())!
      return box.y + box.height
    })
    .toBeLessThanOrEqual(top)
})
