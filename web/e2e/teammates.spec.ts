import {
  addTeammate,
  layoutButton,
  openApprovals,
  expect,
  sandboxName,
  teammateMenu,
  test,
} from "./fixtures"

test("a teammate is copied to another environment from its settings, and opens there without a provider", async ({
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
  await teammateMenu(page, name, "Settings")
  const settings = page.getByRole("region", { name: `${name}'s settings` })
  await settings
    .getByRole("button", { name: /^Copy to another environment/ })
    .click()
  await settings
    .getByRole("button", { name: "Copy to another environment…" })
    .click()
  const dialog = page.getByRole("dialog", {
    name: `Copy ${name} to another environment`,
  })
  await dialog.getByRole("combobox").click()
  await page.getByRole("option", { name: target!.name, exact: true }).click()
  await dialog.getByRole("button", { name: "Copy teammate" }).click()

  await page.getByRole("button", { name: "Open", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${target!.id}/teammates/[^/]+$`))
  await expect(
    page
      .getByRole("navigation", { name: "Where you are" })
      .getByRole("group", { name: `${name} now` })
  ).toContainText("No provider")
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

test("a teammate without a computer is offered one, the screen's menu forks it and the crumb adds another", async ({
  page,
  api,
  env,
}) => {
  const name = `Rail ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  await page.goto(`/e/${env}/teammates/${id}`)
  const none = page.getByRole("region", { name: `${name}'s computer` })
  await expect(none.getByText(`${name} has no computer yet`)).toBeVisible()
  await none.getByRole("button", { name: `Give ${name} a computer` }).click()
  await expect(page.getByRole("dialog", { name: "New computer" })).toBeVisible()
  await page.keyboard.press("Escape")

  const sandbox = sandboxName("rail")
  await api.createSandbox(sandbox, id)
  // The home lists it as a card, whose Terminal opens the desk on it.
  await page
    .getByRole("button", { name: `Terminal of ${sandbox}` })
    .click({ timeout: 15_000 })
  const screen = page.getByRole("region", { name: `${sandbox}'s screen` })
  await expect(screen).toBeVisible()
  await screen.getByRole("button", { name: `More for ${sandbox}` }).click()
  await expect(page.getByRole("menuitem", { name: "Fork…" })).toBeVisible()
  await page.keyboard.press("Escape")
  // Another computer is the crumb's: it switches between them.
  await page
    .getByRole("navigation", { name: "Where you are" })
    .getByRole("button", { name: `Switch computer: ${sandbox}` })
    .click()
  await page
    .getByRole("menuitem", { name: `Give ${name} another computer…` })
    .click()
  await expect(page.getByRole("dialog", { name: "New computer" })).toBeVisible()
})

// An off computer's card leads with Start, and its Terminal starts it too; the folded screen
// has a Start of its own that leaves it folded.
test("an off computer starts from its card, its Terminal, or the folded screen", async ({
  page,
  api,
  env,
}) => {
  const name = `Off ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const sandbox = sandboxName("off")
  const sb = await api.createSandbox(sandbox, id)
  await api.guestReady(sb)
  const base = `/api/environments/${env}/sandboxes/${sb}`
  const status = async () =>
    (await api.json<{ status: string }>("GET", base))!.status
  const stop = async () => {
    // The create holds the computer a moment after its guest connects: Stop then says busy.
    await expect(() => api.json("POST", `${base}/stop`)).toPass()
    await expect.poll(status).toBe("stopped")
  }
  const started = () =>
    page.waitForRequest(
      (r) => r.method() === "POST" && r.url().endsWith(`${base}/start`)
    )
  await stop()

  await page.goto(`/e/${env}/teammates/${id}`)
  const card = page.getByTestId("computer-card").filter({ hasText: sandbox })
  await expect(card).toContainText("Off", { timeout: 15_000 })
  await expect(
    card.getByRole("button", { name: `Browser of ${sandbox}` })
  ).toHaveCount(0)
  await expect(page.getByText("· off, it starts as you type")).toBeVisible()
  let start = started()
  await card.getByRole("button", { name: `Start ${sandbox}` }).click()
  await start
  await expect.poll(status).toBe("running")
  await expect(card).toContainText("Running", { timeout: 15_000 })
  await expect(
    card.getByRole("button", { name: `Browser of ${sandbox}` })
  ).toBeVisible()
  await expect(page.getByText("· off, it starts as you type")).toBeHidden()

  // Terminal starts it, and opens the screen, which shows it starting.
  await stop()
  await expect(card).toContainText("Off")
  start = started()
  await card.getByRole("button", { name: `Terminal of ${sandbox}` }).click()
  await start
  const screen = page.getByRole("region", { name: `${sandbox}'s screen` })
  await expect(screen).toBeVisible()
  await expect.poll(status).toBe("running")

  // Folded, the screen's Start starts it and it stays folded.
  await layoutButton(page, "Home only").click()
  const folded = page.getByRole("region", { name: `${sandbox}, folded` })
  await expect(folded).toBeVisible()
  await expect(
    folded.getByRole("button", { name: `Start ${sandbox}` })
  ).toHaveCount(0)
  await stop()
  start = started()
  await folded.getByRole("button", { name: `Start ${sandbox}` }).click()
  await start
  await expect.poll(status).toBe("running")
  await expect(folded).toBeVisible()
  await expect(
    folded.getByRole("button", { name: `Start ${sandbox}` })
  ).toHaveCount(0)

  // The card's name opens the computer's details.
  await page.goto(`/e/${env}/teammates/${id}`)
  await card.getByRole("button", { name: sandbox, exact: true }).click()
  await expect(
    page
      .getByRole("region", { name: `${sandbox}'s screen` })
      .getByRole("tab", { name: "Details", selected: true })
  ).toBeVisible()
})

// A harness printing in a terminal counts as work, on the home's Now card and in the sidebar;
// another program there leaves the teammate idle, with the program beside it.
test("a teammate's status counts what runs in its terminals", async ({
  page,
  api,
  env,
}) => {
  const name = `Term ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const sandbox = sandboxName("status")
  const sb = await api.createSandbox(sandbox, id)
  await api.setTerminal(sb, "2", {
    command: "node",
    foreground: ["node", "/usr/local/bin/opencode"],
  })
  await page.goto(`/e/${env}/teammates/${id}`)
  const header = page
    .getByRole("navigation", { name: "Where you are" })
    .getByRole("group", { name: `${name} now` })
  // The row's own link: its + is "New thread with" the teammate.
  const row = page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("link", { name: new RegExp(`^${name}`) })
  await expect(header).toContainText("working · opencode in a terminal", {
    timeout: 15_000,
  })
  await expect(row).toContainText("working")

  await header.getByRole("link", { name: "opencode in a terminal" }).click()
  await expect
    .poll(() => new URL(page.url()).searchParams.get("term"))
    .toMatch(/^"?2"?$/)
  const sessions = page.getByRole("tablist", { name: "Terminal sessions" })
  await expect(sessions.getByRole("tab", { name: "opencode" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  await expect(sessions.getByLabel("working")).toBeVisible()

  // A dev server that printed long ago isn't work; the top bar says so.
  await api.setTerminal(sb, "2", {
    command: "npm",
    foreground: ["npm run dev"],
    idle: 120,
  })
  await page.goto(`/e/${env}/teammates/${id}`)
  await expect(header).toContainText("idle · npm run dev running", {
    timeout: 15_000,
  })
  await expect(row).toContainText("npm")
})

// One switch in the top bar sets the layout: the conversation alone, both, or the computer
// alone, which keeps the conversation mounted. The screen's rail opens a tab beside it.
test("the top bar's layout switch gives the page to the conversation, both or the computer", async ({
  page,
  api,
  env,
}) => {
  const name = `Wide ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const sandbox = sandboxName("wide")
  await api.createSandbox(sandbox, id)
  // The desk: the computer beside the home.
  await page.goto(`/e/${env}/teammates/${id}?tab=terminal`)
  const screen = page.getByRole("region", { name: `${sandbox}'s screen` })
  await expect(screen).toBeVisible({ timeout: 15_000 })
  const composer = page.getByPlaceholder(`Message ${name}…`)
  await composer.fill("a draft")
  const split = (await screen.boundingBox())!.width
  await expect(layoutButton(page, "Both")).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  // The bezel has no panel buttons of its own any more.
  await expect(
    screen.getByRole("button", { name: /Enlarge|Fold the screen/ })
  ).toHaveCount(0)

  await layoutButton(page, "Computer only").click()
  await expect(composer).toBeHidden()
  await expect(page).toHaveURL(/full=/)
  expect((await screen.boundingBox())!.width).toBeGreaterThan(split * 1.5)
  await expect(layoutButton(page, "Computer only")).toHaveAttribute(
    "aria-pressed",
    "true"
  )

  await layoutButton(page, "Both").click()
  await expect(composer).toHaveValue("a draft")
  await expect(page).not.toHaveURL(/full=/)

  await layoutButton(page, "Home only").click()
  const folded = page.getByRole("region", { name: `${sandbox}, folded` })
  await expect(folded).toBeVisible()
  await expect(composer).toBeVisible()
  await expect(layoutButton(page, "Home only")).toHaveAttribute(
    "aria-pressed",
    "true"
  )

  // A tab on the rail opens it beside the conversation.
  await folded.getByRole("button", { name: "Files" }).click()
  await expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  await expect(layoutButton(page, "Both")).toHaveAttribute(
    "aria-pressed",
    "true"
  )

  // Off the teammate's page the switch goes.
  await page.goto(`/e/${env}/secrets`)
  await expect(
    page
      .getByRole("navigation", { name: "Where you are" })
      .getByRole("group", { name: "Layout" })
  ).toHaveCount(0)
})

// The conversation stays mounted while the screen takes the page or covers it on a phone, and
// the home in it shows the teammate's requests. Out of sight it leaves them to the top bar's
// list, so a request from a computer off the screen isn't shown nowhere.
for (const phone of [false, true]) {
  test(`a request is in the top bar's list while the screen hides the conversation${phone ? " on a phone" : ""}`, async ({
    page,
    api,
    env,
  }) => {
    if (phone) await page.setViewportSize({ width: 390, height: 844 })
    await api.dismissApprovals()
    const name = `Hidden ${Math.random().toString(36).slice(2, 8)}`
    const id = await addTeammate(api, env, name)
    const shownName = sandboxName("shown")
    const shown = await api.createSandbox(shownName, id)
    const other = await api.createSandbox(sandboxName("other"), id)
    await page.goto(
      `/e/${env}/teammates/${id}?computer=${shown}&tab=terminal${phone ? "" : "&full=1"}`
    )
    await expect(
      page.getByRole("region", { name: `${shownName}'s screen` })
    ).toBeVisible({ timeout: 15_000 })

    const host = `hidden-${Math.random().toString(36).slice(2, 8)}.example.org`
    await api.connect(other, host)
    const card = (await openApprovals(page))
      .getByTestId("approval-card")
      .filter({ hasText: host })
    await expect(card).toBeVisible()
    await page.keyboard.press("Escape")

    // With the conversation back, its home shows the request in place again.
    await layoutButton(page, "Home only").click()
    await expect(
      page.getByRole("region", { name: "Needs you" }).filter({ hasText: host })
    ).toBeVisible()
    await expect(card).toBeHidden()
    await api.dismissApprovals()
  })
}

test("a teammate's settings save as you type, and closing them with a change that can't save asks", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Saver")
  const id = await addTeammate(api, env, name)
  const teammate = () =>
    api.json<{ name: string; role: string; gitName: string }>(
      "GET",
      `/api/environments/${env}/teammates/${id}`
    )
  await page.goto(`/e/${env}/teammates/${id}?profile=settings&open=identity`)
  const status = page.getByRole("status").filter({ hasText: /Sav|Not saved/ })
  await expect(page.getByLabel("Role")).toBeVisible()

  // A change saves once typing pauses; opening another section and changing it saves that too.
  await page.getByLabel("Role").fill("Reviewer")
  await expect(status).toContainText("Saved")
  await expect.poll(async () => (await teammate())?.role).toBe("Reviewer")
  await page.getByRole("button", { name: /^Git identity/ }).click()
  await page.getByLabel("Git name").fill("Saver Bot")
  await expect.poll(async () => (await teammate())?.gitName).toBe("Saver Bot")

  // Closing the sheet right after a change saves it on the way out.
  await page.getByRole("button", { name: /^Identity/ }).click()
  await page.getByLabel("Role").fill("Writer")
  await page.keyboard.press("Escape")
  await expect(
    page.getByRole("region", { name: `${name}'s settings` })
  ).toBeHidden()
  await expect.poll(async () => (await teammate())?.role).toBe("Writer")

  // An empty name can't save: it says so, and closing asks. Stay keeps it, Leave goes.
  await page.goto(`/e/${env}/teammates/${id}?profile=settings&open=identity`)
  await page.getByLabel("Name", { exact: true }).fill("")
  await expect(status).toContainText("Not saved until the form is complete")
  await page.keyboard.press("Escape")
  const ask = page.getByRole("alertdialog", { name: "Leave without saving?" })
  await ask.getByRole("button", { name: "Stay" }).click()
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue("")
  await page.keyboard.press("Escape")
  await ask.getByRole("button", { name: "Leave" }).click()
  await expect(
    page.getByRole("region", { name: `${name}'s settings` })
  ).toBeHidden()
  expect((await teammate())?.name).toBe(name)
})

test("a teammate wears the sprite its name picks until another avatar is chosen", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Sprite")
  const id = await addTeammate(api, env, name)
  const avatar = async () =>
    (
      await api.json<{ avatar?: string }>(
        "GET",
        `/api/environments/${env}/teammates/${id}`
      )
    )?.avatar ?? ""
  await page.goto(`/e/${env}/teammates/${id}?profile=settings&open=identity`)
  const picker = page.getByRole("group", { name: "Avatar", exact: true })
  const header = page.locator('h1 [data-slot="avatar"]')
  // Without a chosen avatar, the sprite the name picks is marked and drawn in the header.
  await expect(page.getByText("Until you pick one")).toBeVisible()
  const marked = picker
    .getByRole("group", { name: "Sprites" })
    .getByRole("button", { pressed: true })
  await expect(marked).toHaveCount(1)
  await expect(header.locator("svg[data-sprite]")).toHaveCount(1)

  // Picking one stores it; the initials and an emoji are choices too.
  await picker.getByRole("button", { name: "Owl", exact: true }).click()
  await expect.poll(avatar).toBe("sprite:owl")
  await expect(header.locator('svg[data-sprite="owl"]')).toBeVisible()
  await picker.getByRole("button", { name: "Initials", exact: true }).click()
  await expect.poll(avatar).toBe("initials")
  await expect(header.locator("svg")).toHaveCount(0)
  await picker.getByRole("button", { name: "🦊", exact: true }).click()
  await expect.poll(avatar).toBe("🦊")
  await expect(header).toHaveText("🦊")
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
  await page.goto(`/e/${env}/teammates/${id}?profile=settings&open=network`)
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

// The screen follows the open chat; another computer picked beside it is pinned there, and
// the screen and the chat say which computer the chat works on until it is shown again.
test("a computer pinned beside a chat says the chat works elsewhere", async ({
  page,
  api,
  env,
}) => {
  const name = `Pin ${Math.random().toString(36).slice(2, 8)}`
  const teammate = await addTeammate(api, env, name)
  const shop = sandboxName("shop")
  const docs = sandboxName("docs")
  const id = await api.createSandbox(shop, teammate)
  await api.createSandbox(docs, teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!

  await page.goto(`/e/${env}/teammates/${teammate}?thread=${chat.id}`)
  const onShop = page.getByRole("region", { name: `${shop}'s screen` })
  await expect(onShop).toBeVisible({ timeout: 15_000 })
  // The breadcrumb's chip names the computer and switches it; the screen has no switcher.
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  await expect(onShop.getByTitle(new RegExp(`^${shop}:`))).toHaveCount(0)
  await crumbs.getByRole("button", { name: `Switch computer: ${shop}` }).click()
  await page.getByRole("menuitem", { name: new RegExp(docs) }).click()

  const onDocs = page.getByRole("region", { name: `${docs}'s screen` })
  await expect(onDocs.getByRole("status")).toHaveText(
    `The open chat works on ${shop}. This screen shows ${docs}.Show ${shop}`
  )
  await crumbs.getByRole("button", { name: `Switch computer: ${docs}` }).click()
  await expect(
    page.getByRole("menuitem", { name: new RegExp(shop) })
  ).toContainText("this thread's")
  await page.keyboard.press("Escape")
  await onDocs.getByRole("button", { name: `Show ${shop}` }).click()
  await expect(onShop).toBeVisible()
  await expect(onShop.getByRole("status")).toHaveCount(0)
})

test("a teammate's settings show what it knows and works on, its own rules, and turn its memory's learning and dreaming off", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Knowing")
  const id = await addTeammate(api, env, name)
  const scope = `teammate:${id}`
  await api.json("POST", `/api/environments/${env}/memory/facts`, {
    scope,
    kind: "fact",
    text: "The staging database is reset every Monday",
  })
  await api.json("POST", `/api/environments/${env}/teammates/${id}/tasks`, {
    title: "Rotate the staging keys",
  })
  const host = `api-${Math.random().toString(36).slice(2, 8)}.test`
  await api.json("POST", `/api/environments/${env}/rules`, {
    host,
    action: "allow",
    teammateId: id,
  })
  const teammate = () =>
    api.json<{ memoryLearn: boolean; memoryDream: boolean }>(
      "GET",
      `/api/environments/${env}/teammates/${id}`
    )

  await page.goto(`/e/${env}/teammates/${id}?profile=settings`)
  const memoryRow = page.getByRole("button", { name: /^Memory/ })
  await expect(memoryRow).toContainText("1 fact")
  await expect(page.getByRole("button", { name: /^Tasks/ })).toContainText(
    "1 open"
  )
  await expect(
    page.getByRole("button", { name: /^Network rules/ })
  ).toContainText(/1 its own · \d+ from the environment/)

  await memoryRow.click()
  const memory = page.locator("#teammate-memory-panel")
  await expect(memory).toContainText("1fact")
  await memory.getByRole("button", { name: "Add a fact" }).click()
  await memory
    .getByLabel("A fact to remember")
    .fill("Releases go out on Thursdays")
  await memory.getByRole("button", { name: "Remember" }).click()
  await expect(memoryRow).toContainText("2 facts")

  await memory.getByRole("checkbox", { name: /Learn from its chats/ }).click()
  await expect.poll(async () => (await teammate())!.memoryLearn).toBe(false)
  await memory.getByRole("checkbox", { name: /Dream every night/ }).click()
  await expect.poll(async () => (await teammate())!.memoryDream).toBe(false)
  await expect(
    memory.getByRole("checkbox", { name: /Learn from its chats/ })
  ).not.toBeChecked()

  // The memory opens in the sheet, and its back arrow leads to the row.
  await memory.getByRole("button", { name: `Open ${name}'s memory` }).click()
  await expect(page).toHaveURL(/profile=memory/)
  await page.getByRole("button", { name: `Back to ${name}'s settings` }).click()
  await expect(page).toHaveURL(/profile=settings/)
  await expect(page).toHaveURL(/open=memory/)
  await expect(page.locator("#teammate-memory-panel")).toBeVisible()

  await page.getByRole("button", { name: /^Network rules/ }).click()
  const rules = page.locator("#teammate-rules-panel")
  await rules.getByRole("link", { name: new RegExp(host) }).click()
  // The rule opens on the Network page, which then drops ?rule= from the URL.
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network`))
  const edit = page.getByRole("dialog", { name: "Edit rule" })
  await expect(edit).toBeVisible()
  await expect(edit.getByLabel("Host")).toHaveValue(host)
})

// The sidebar's >_ opens a terminal on the teammate's computer; its + and ⌘K's "New thread
// with" open its home with the composer, which starts threads, ready to type in.
test("the sidebar's + and ⌘K start a new thread with a teammate in its home composer", async ({
  page,
  api,
  env,
}) => {
  const name = `Threader ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const computer = sandboxName("thread")
  const sb = await api.createSandbox(computer, id)
  await api.guestReady(sb)
  const row = page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("listitem")
    .filter({ has: page.getByRole("link", { name: new RegExp(name) }) })
    .first()
  const composer = page.getByPlaceholder(`Message ${name}…`)

  await page.goto(`/e/${env}`)
  await row.hover()
  await row.getByRole("link", { name: `A terminal on ${computer}` }).click()
  await expect(page).toHaveURL(
    new RegExp(`/teammates/${id}\\?.*computer=${sb}.*tab=terminal`)
  )

  await page.goto(`/e/${env}`)
  await row.hover()
  await row.getByRole("link", { name: `New thread with ${name}` }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}$`))
  await expect(composer).toBeFocused()
  await expect(page.getByRole("button", { name: "Start work" })).toHaveCount(0)

  await page.goto(`/e/${env}`)
  // The palette's shortcut is there once the sidebar is.
  await expect(
    page
      .locator('[data-sidebar="sidebar"]')
      .getByRole("button", { name: /^Search/ })
  ).toBeVisible()
  const palette = page.getByRole("dialog", { name: "Search Studio" })
  await page.keyboard.press("Control+k")
  await expect(palette).toBeVisible()
  await palette
    .getByRole("combobox", { name: "Search Studio" })
    .fill(`new thread with ${name}`)
  await palette
    .getByRole("option", { name: new RegExp(`^New thread with ${name}`) })
    .click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${id}$`))
  await expect(composer).toBeFocused()
})
