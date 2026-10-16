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
  await expect(main.getByRole("button", { name: "Stop" })).toBeVisible()

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
