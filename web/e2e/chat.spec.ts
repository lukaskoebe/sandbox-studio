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
  // Closing the popup while it loads can hang in CI; it closes with the context.
  await popup
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

test("a message sent while the agent works goes into its turn, or with Alt waits for it", async ({
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

  // Enter steers: the message goes into the running turn and shows there.
  await box.fill("Then fix the lint")
  await box.press("Enter")
  await expect(box).toHaveValue("")
  await expect(main.getByText("Then fix the lint")).toBeVisible()
  const queued = main.getByRole("region", { name: "Queued messages" })
  await expect(queued).toBeHidden()

  // Alt waits for the turn to end.
  await box.fill("Then update the docs")
  await box.press("Alt+Enter")
  await expect(box).toHaveValue("")
  await box.fill("And write a changelog")
  await main
    .getByRole("button", { name: "Send now", exact: true })
    .click({ modifiers: ["Alt"] })
  await expect(queued).toContainText("2 queued")
  await expect(queued).toContainText("Then update the docs")
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
  expect(JSON.stringify(agents[0].steers)).toContain("Then fix the lint")
  expect(agents[0].prompts).toHaveLength(2)
  expect(JSON.stringify(agents[0].prompts[1])).toContain("Then update the docs")
  expect(JSON.stringify(agents[0].prompts)).not.toContain("changelog")
})

test("a reply in a long chat keeps the chat at its end", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  await page.setViewportSize({ width: 1280, height: 640 })
  const name = `Long ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("long"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!

  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  const box = main.getByRole("textbox", { name: "Message" })
  const allow = main.getByRole("button", { name: "Allow", exact: true })
  for (const [i, text] of ["First request", "Second request"].entries()) {
    await box.fill(text)
    await box.press("Enter")
    await expect(allow).toHaveCount(1)
    await allow.click()
    await expect(main.getByText(done)).toHaveCount(i + 1)
  }

  // A reply scrolls to itself, not back to a message from when the page opened.
  await page.reload()
  const messages = main.getByRole("region", { name: "Messages" })
  await expect(messages.getByText(done)).toHaveCount(2)
  await box.fill("Third request")
  await box.press("Enter")
  // The agent thinks a moment before it answers, as real ones do.
  await expect(allow).toHaveCount(1)
  await expect(messages.getByText("Third request")).toBeInViewport()
  await expect(messages.getByText("First request")).not.toBeInViewport()
})

test("a chat keeps what was typed but not sent while the user is elsewhere", async ({
  page,
  api,
  env,
}) => {
  const teammate = await addTeammate(
    api,
    env,
    `Drafty ${Math.random().toString(36).slice(2, 6)}`
  )
  const id = await api.createSandbox(sandboxName("draft"), teammate)
  await api.guestReady(id)
  const chats = await Promise.all(
    [0, 1].map(
      async () =>
        (await api.json<{ id: string }>(
          "POST",
          `/api/environments/${env}/sandboxes/${id}/chats`
        ))!
    )
  )
  const box = page.getByRole("main").getByRole("textbox", { name: "Message" })

  await page.goto(`/e/${env}/chats/${chats[0].id}`)
  await box.fill("Half a thought")
  // Each chat has its own draft.
  await page.goto(`/e/${env}/chats/${chats[1].id}`)
  await expect(box).toHaveValue("")
  await page.goBack()
  await expect(box).toHaveValue("Half a thought")
  // It outlives a reload of the tab, and a sent message clears it.
  await page.reload()
  await expect(box).toHaveValue("Half a thought")
  await box.press("Enter")
  await expect(box).toHaveValue("")
  await page.reload()
  await expect(page.getByRole("main")).toContainText("Half a thought")
  await expect(box).toHaveValue("")
})

// The teammate's home is quiet until a thread has something this browser hasn't seen: then its
// card says New, until it is opened.
test("a long chat opens on its last turns and loads the earlier ones", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 1280, height: 640 })
  const teammate = await addTeammate(
    api,
    env,
    `Paged ${Math.random().toString(36).slice(2, 6)}`
  )
  const id = await api.createSandbox(sandboxName("paged"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await api.json("POST", `/api/e2e/chats/${chat.id}/turns`, { count: 45 })

  await page.goto(`/e/${env}/chats/${chat.id}`)
  const messages = page
    .getByRole("main")
    .getByRole("region", { name: "Messages" })
  await expect(messages.getByText("Answer 45", { exact: true })).toBeVisible()
  // The last 40 turns are loaded, the 5 before them only counted.
  await expect(messages.getByText("Request 6", { exact: true })).toHaveCount(1)
  await expect(messages.getByText("Request 5", { exact: true })).toHaveCount(0)
  const earlier = messages.getByRole("button", {
    name: "Load earlier · 5 more turns",
  })
  await expect(earlier).toHaveCount(1)
  await expect(messages).toContainText(
    "Scrolling up to this line loads them too"
  )

  // Scrolling up to the row loads them.
  await messages.hover()
  await page.mouse.wheel(0, -100_000)
  await expect(messages.getByText("Request 1", { exact: true })).toHaveCount(1)
  await expect(earlier).toHaveCount(0)

  // So does the button, after a reload, and the reading place stays.
  await page.reload()
  await expect(messages.getByText("Answer 45", { exact: true })).toBeVisible()
  await earlier.click()
  await expect(messages.getByText("Request 1", { exact: true })).toHaveCount(1)
  await expect(earlier).toHaveCount(0)
  await expect(
    messages.getByText("Request 6", { exact: true })
  ).toBeInViewport()
  await expect(
    messages.getByText("Request 1", { exact: true })
  ).not.toBeInViewport()
})

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

test("a message's changes show each file's diff", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const teammate = await addTeammate(api, env, "Editor")
  const id = await api.createSandbox(sandboxName("changes"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  await main.getByRole("textbox", { name: "Message" }).fill("Edit the file")
  await main.getByRole("button", { name: "Send" }).click()
  await main.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(main).toContainText(done)

  // The scripted agent edits nothing, so the turn comes as if it had.
  const file = {
    path: "src/app.go",
    status: "modified",
    added: 1,
    removed: 1,
  }
  await page.route(/\/chats\/[^/]+(\?.*)?$/, async (route) => {
    const r = await route.fetch()
    const body = await r.json()
    body.turns.at(-1).changes = { files: [file] }
    await route.fulfill({ response: r, json: body })
  })
  await page.route(/\/turns\/[^/]+\/changes/, (route) =>
    route.fulfill({
      json: {
        files: [
          {
            ...file,
            // A snapshot's diff has no diff --git line, and isn't a rename from a/ to b/.
            diff: "--- a/src/app.go\n+++ b/src/app.go\n@@ -1,2 +1,2 @@\n package main\n-var old = 1\n+var fresh = 2\n",
          },
        ],
      },
    })
  )
  await page.reload()
  await main.getByRole("tab", { name: /^Changes/ }).click()
  await main.getByRole("button", { name: /src\/app\.go/ }).click()
  const line = main.locator("[data-line]")
  await expect(line.filter({ hasText: "var fresh = 2" })).toBeVisible()
  await expect(line.filter({ hasText: "var old = 1" })).toBeVisible()
  await expect(line).toHaveCount(3)
  await expect(main.locator("diffs-container")).not.toContainText(
    "a/src/app.go"
  )
})
