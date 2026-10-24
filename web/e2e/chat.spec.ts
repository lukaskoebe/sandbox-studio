import { addTeammate, expect, sandboxName, test } from "./fixtures"

// The e2e build's guest answers a chat's acp stream with a scripted ACP agent
// (internal/e2e/acp.go) instead of the harness's adapter. Every prompt plays the same turn.
const greeting = "Hello from the scripted agent. I need to run the tests first."
const tool = "Run make test"
const done = "The tests passed."

test("a chat's agent answers, asks for a permission and goes on once allowed", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  // Links to the sandbox's servers are buttons only while previews are on.
  await api.json("PUT", "/api/system/previews", { enabled: true })
  const name = `Chatty ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("chat"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!

  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  await main
    .getByRole("textbox", { name: "Message" })
    .fill("Please run the tests")
  await main.getByRole("button", { name: "Send" }).click()

  await expect(main).toContainText(greeting)
  await expect(main).toContainText(`${name} asks before going on`)
  await expect(main).toContainText(tool)
  // The agent waits for the answer.
  await expect(main).not.toContainText(done)

  await main.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(main).toContainText(done)
  // The closing message's markdown table is a table.
  const table = main.getByRole("table")
  await expect(table.getByRole("columnheader")).toHaveText([
    "Package",
    "Result",
  ])
  await expect(table.getByRole("cell")).toHaveText(["./... | tee", "ok"])
  // A link to a server in the sandbox opens its preview, on that page.
  const opened = page.waitForRequest(
    (r) =>
      r.method() === "POST" &&
      r.url().includes(`/sandboxes/${id}/previews/5173/open?path=%2Flogin`)
  )
  const popup = page.waitForEvent("popup")
  await main
    .getByRole("button", { name: "http://localhost:5173/login" })
    .click()
  await opened
  await (await popup).close()
  await expect(
    main.getByRole("button", { name: "Allow", exact: true })
  ).toHaveCount(0)
  await expect(main).toContainText("Allowed")

  const agents = (await api.guest(id)).acp
  expect(agents).toHaveLength(1)
  expect(agents[0].chat).toBe(chat.id)
  expect(agents[0].prompts).toHaveLength(1)
  expect(JSON.stringify(agents[0].prompts[0])).toContain("Please run the tests")
  expect(agents[0].outcomes).toEqual([
    { outcome: { outcome: "selected", optionId: "allow" } },
  ])
})

test("a message sent while the agent works waits for its turn to end", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = `Steered ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("steer"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!

  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  const box = main.getByRole("textbox", { name: "Message" })
  await box.fill("Please run the tests")
  await box.press("Enter")
  // The turn waits for a permission, so it keeps running.
  await expect(main).toContainText(`${name} asks before going on`)
  await expect(
    main.getByRole("button", { name: "Stop", exact: true })
  ).toBeVisible()

  await box.fill("Then fix the lint")
  await box.press("Enter")
  await expect(box).toHaveValue("")
  await box.fill("And write a changelog")
  await main.getByRole("button", { name: "Queue", exact: true }).click()
  const queued = main.getByRole("region", { name: "Queued messages" })
  await expect(queued).toContainText("2 queued")
  await expect(queued).toContainText("Then fix the lint")
  await expect(queued).toContainText("And write a changelog")

  // Edit takes a message back into the composer.
  await queued
    .getByRole("listitem")
    .filter({ hasText: "And write a changelog" })
    .getByRole("button", { name: "Edit queued message" })
    .click()
  await expect(box).toHaveValue("And write a changelog")
  await expect(queued).toContainText("Queued: sent when")
  await expect(queued).not.toContainText("And write a changelog")
  await box.fill("")

  // Once the turn ends, the queued message goes as the next turn.
  await main.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(main).toContainText(done)
  await expect(queued).toBeHidden()
  await expect(
    main.getByRole("button", { name: "Allow", exact: true })
  ).toHaveCount(1)
  const agents = (await api.guest(id)).acp
  expect(agents[0].prompts).toHaveLength(2)
  expect(JSON.stringify(agents[0].prompts[1])).toContain("Then fix the lint")
  expect(JSON.stringify(agents[0].prompts)).not.toContain("changelog")
})

// The teammate's home is quiet until a thread has something this browser hasn't seen: then its
// card says New, until it is opened.
test("the teammate's home marks only the threads with something new", async ({
  page,
  api,
  env,
}) => {
  const name = `Marked ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("marks"), teammate)
  await api.guestReady(id)
  const chats = `/api/environments/${env}/sandboxes/${id}/chats`
  const first = (await api.json<{ createdAt: string }>("POST", chats))!
  // Chats from the second of the first visit count as new, as createdAt has whole seconds, so
  // the visit waits for the next one.
  const next = Date.parse(first.createdAt) + 1000 - Date.now()
  if (next > 0) await page.waitForTimeout(next)

  await page.goto(`/e/${env}/teammates/${teammate}`)
  const cards = page.getByRole("button", { name: /^New chat/ })
  const news = page.getByTitle("New since you last looked")
  await expect(cards).toHaveCount(1)
  await expect(news).toHaveCount(0)

  // A thread that starts while the page is open is new.
  const fresh = (await api.json<{ id: string }>("POST", chats))!
  await expect(cards).toHaveCount(2, { timeout: 15_000 })
  await expect(news).toHaveCount(1)
  await cards.filter({ has: news }).click()
  await expect(page).toHaveURL(new RegExp(`thread=${fresh.id}`))
  await page.getByTitle(`Back to ${name} (Esc)`).click()
  await expect(cards).toHaveCount(2)
  await expect(news).toHaveCount(0)
})

test("a message on the teammate's home starts a thread and opens it", async ({
  page,
  api,
  env,
}) => {
  const teammate = await addTeammate(
    api,
    env,
    `Opened ${Math.random().toString(36).slice(2, 6)}`
  )
  const sbName = sandboxName("opened")
  await api.guestReady(await api.createSandbox(sbName, teammate))

  await page.goto(`/e/${env}/teammates/${teammate}`)
  const main = page.getByRole("main")
  await expect(main.getByText(`Starts a new thread on ${sbName}`)).toBeVisible()
  const box = main.getByRole("textbox", { name: "Message" })
  await box.fill("Please run the tests")
  await box.press("Enter")

  await expect(page).toHaveURL(/[?&]thread=/)
  await expect(main).toContainText(greeting)
  await page.waitForTimeout(1500)
  await expect(page).toHaveURL(/[?&]thread=/)
})

// A chat whose sandbox is down says so above the composer, and its button brings it back; the
// screen of the computer that is off starts it too.
test("a chat says when its sandbox is suspended or stopped, and starts it", async ({
  page,
  api,
  env,
}) => {
  const name = `Sleepy ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const sbName = sandboxName("asleep")
  const id = await api.createSandbox(sbName, teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  const lifecycle = (action: string) =>
    api.json("POST", `/api/environments/${env}/sandboxes/${id}/${action}`)

  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  const banner = main.getByRole("status").filter({ hasText: sbName })
  await expect(main.getByRole("textbox", { name: "Message" })).toBeVisible()
  await expect(banner).toHaveCount(0)

  await lifecycle("suspend")
  await expect(banner).toContainText(`${sbName} is suspended.`)
  await banner.getByRole("button", { name: "Resume" }).click()
  await expect(banner).toHaveCount(0)

  await lifecycle("stop")
  await expect(banner).toContainText(`${sbName} is stopped.`)

  // The teammate is offline, and its computer's screen starts it.
  await page.goto(`/e/${env}/teammates/${teammate}`)
  const header = main.locator("header").first()
  await expect(header).toContainText("Offline")
  const screen = main.getByRole("region", { name: `${sbName}'s screen` })
  await expect(screen).toContainText(`${sbName} is off`)
  // The teammate's browser has a VM of its own, so its tab stays.
  await expect(screen.getByRole("tab", { name: "Browser" })).toBeVisible()
  await screen.getByRole("button", { name: "Start", exact: true }).click()
  await expect(header).not.toContainText("Offline", { timeout: 20_000 })
  await expect(
    screen.getByRole("button", { name: "Start", exact: true })
  ).toHaveCount(0)
})
