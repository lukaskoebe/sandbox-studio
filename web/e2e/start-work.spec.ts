import { addTeammate, expect, sandboxName, test } from "./fixtures"

const greeting = "Hello from the scripted agent. I need to run the tests first."

// Start work asks how the teammate should work. In a chat it starts a thread with the message,
// on the computer it suggests.
test("Start work begins a thread with the message, on the teammate's computer", async ({
  page,
  api,
  env,
}) => {
  const name = `Starter ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const computer = sandboxName("start")
  await api.guestReady(await api.createSandbox(computer, id))

  await page.goto(`/e/${env}/teammates/${id}`)
  await page
    .locator("header")
    .filter({ hasText: name })
    .getByRole("button", { name: "Start work" })
    .click()
  const dialog = page.getByRole("dialog", { name: `Start work with ${name}` })
  await expect(
    dialog.getByRole("radio", { name: /^Ask in chat/ })
  ).toHaveAttribute("aria-checked", "true")
  await expect(dialog.getByLabel("The computer")).toContainText(computer)
  const start = dialog.getByRole("button", { name: "Start the thread" })
  await expect(start).toBeDisabled()
  await dialog
    .getByLabel(`What should ${name} do?`)
    .fill("Please run the tests")
  await start.click()

  await expect(page).toHaveURL(/[?&]thread=/)
  await expect(dialog).toBeHidden()
  const main = page.getByRole("main")
  await expect(main).toContainText("Please run the tests")
  await expect(main).toContainText(greeting)
  // The scripted agent asks to run a tool; answered, its card leaves the other tests alone.
  await main.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(
    main.getByRole("button", { name: "Allow", exact: true })
  ).toHaveCount(0)
})

// The harness in a terminal opens the desk on a session of its own; just the computer opens its
// terminal, and in a fresh fork on a new computer of the teammate's. The sidebar's >_ and + do
// the same from anywhere.
test("Start work opens the harness in a terminal, or just the computer, and forks it first", async ({
  page,
  api,
  env,
}) => {
  const name = `Desk ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const computer = sandboxName("desk")
  const sb = await api.createSandbox(computer, id)
  await api.guestReady(sb)
  const sidebar = page.locator('[data-sidebar="sidebar"]')
  const row = sidebar
    .getByRole("listitem")
    .filter({ has: page.getByRole("link", { name: new RegExp(name) }) })
    .first()

  await page.goto(`/e/${env}`)
  await row.hover()
  await row.getByRole("link", { name: `A terminal on ${computer}` }).click()
  await expect(page).toHaveURL(
    new RegExp(`/teammates/${id}\\?.*computer=${sb}.*tab=terminal`)
  )

  await row.hover()
  await row.getByRole("button", { name: `Start work with ${name}` }).click()
  const dialog = page.getByRole("dialog", { name: `Start work with ${name}` })
  await dialog.getByRole("radio", { name: /in a terminal/ }).click()
  await expect(dialog.getByLabel(`What should ${name} do?`)).toHaveCount(0)
  await dialog.getByRole("button", { name: /^Open / }).click()
  await expect(dialog).toBeHidden()
  await expect(page).toHaveURL(/[?&]tab=terminal/)
  await expect(page).toHaveURL(/[?&]term=/)

  await page.goto(`/e/${env}/computers`)
  await row.hover()
  await row.getByRole("button", { name: `Start work with ${name}` }).click()
  await dialog.getByRole("radio", { name: /^Just the computer/ }).click()
  await dialog.getByRole("checkbox", { name: /In a fresh fork/ }).click()
  // The fake runtime doesn't copy disks, so the fork is checked up to the request it sends and
  // the dialog keeping its answer.
  const forked = page.waitForRequest(/\/fork$/)
  await dialog.getByRole("button", { name: "Open the computer" }).click()
  expect((await forked).postDataJSON()).toEqual({
    name: `${computer}-fork`,
    docker: false,
    tools: true,
    script: false,
    teammateId: id,
  })
  await expect(dialog.getByRole("alert")).toContainText(
    "this runtime cannot copy workspaces"
  )
  await expect(
    dialog.getByRole("button", { name: "Open the computer" })
  ).toBeEnabled()

  // Without the fork it opens the computer's terminal.
  await dialog.getByRole("checkbox", { name: /In a fresh fork/ }).click()
  await dialog.getByRole("button", { name: "Open the computer" }).click()
  await expect(dialog).toBeHidden()
  await expect(page).toHaveURL(
    new RegExp(`/teammates/${id}\\?.*computer=${sb}.*tab=terminal`)
  )
})

// Bring in, on the desk, asks the teammate in on the computer shown, as a chat.
test("Bring in starts a thread on the computer the desk shows", async ({
  page,
  api,
  env,
}) => {
  const name = `Bring ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const sb = await api.createSandbox(sandboxName("bring"), id)
  await api.guestReady(sb)

  await page.goto(`/e/${env}/teammates/${id}?computer=${sb}&tab=terminal`)
  await page.getByRole("button", { name: `Bring ${name} in` }).click()
  const dialog = page.getByRole("dialog", { name: `Bring ${name} in` })
  await expect(
    dialog.getByRole("radio", { name: /^Ask in chat/ })
  ).toHaveAttribute("aria-checked", "true")
  await dialog
    .getByLabel(`What should ${name} do?`)
    .fill("The test fails after my change")
  await dialog.getByRole("button", { name: "Start the thread" }).click()
  await expect(page).toHaveURL(/[?&]thread=/)
  const main = page.getByRole("main")
  await expect(main).toContainText("The test fails after my change")
  await expect(main).toContainText(greeting)
  await main.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(
    main.getByRole("button", { name: "Allow", exact: true })
  ).toHaveCount(0)
})

// A teammate with no computer is offered one instead.
test("Start work offers a computer to a teammate without one", async ({
  page,
  api,
  env,
}) => {
  const name = `Bare ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  await page.goto(`/e/${env}/teammates/${id}`)
  await page
    .locator("header")
    .filter({ hasText: name })
    .getByRole("button", { name: "Start work" })
    .click()
  const dialog = page.getByRole("dialog", { name: `Start work with ${name}` })
  await expect(dialog).toContainText(`${name} has no computer yet`)
  await dialog.getByRole("button", { name: `Give ${name} a computer` }).click()
  await expect(page.getByRole("dialog", { name: "New computer" })).toBeVisible()
})
