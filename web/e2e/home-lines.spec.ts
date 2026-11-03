import { addTeammate, expect, sandboxName, test } from "./fixtures"

test("home draws each sandbox's sessions as branches off its line", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(120_000)
  await api.dismissApprovals()
  const name = `Ada ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const sandbox = sandboxName("lines")
  const id = await api.createSandbox(sandbox, teammate)
  await api.guestReady(id)
  // A tab open makes Studio look at the terminals every 10 s.
  await page.goto(`/e/${env}`)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await api.json("POST", `/api/environments/${env}/chats/${chat.id}/prompt`, {
    text: "Please run the tests",
  })
  await api.setTerminal(id, "main", {
    command: "go",
    foreground: ["go", "test", "./..."],
    path: "/workspace",
  })
  await api.setTerminal(id, "dev", {
    command: "pnpm",
    foreground: ["pnpm", "dev"],
    path: "/workspace",
  })
  await api.json("PUT", `/api/e2e/sandboxes/${id}/ports`, { ports: [5173] })

  const lines = page.getByRole("region", { name: "Sandboxes" })
  await expect(
    lines.getByRole("link", { name: `chat · ${name} · Please run the tests` })
  ).toBeVisible()
  await expect(lines.getByText(/is working|waiting on you/)).toBeVisible()
  const main = lines.getByRole("link", {
    name: /^main · go test \.\/\.\.\. · you, /,
  })
  await expect(main).toBeVisible({ timeout: 30_000 })
  await expect(
    lines.getByRole("link", { name: /^dev · pnpm dev · you, / })
  ).toBeVisible()
  await expect(
    lines.getByRole("link", { name: /^preview :5173, / })
  ).toBeVisible()

  // The tests end and the dev server's terminal closes: it rejoins the line.
  await api.setTerminal(id, "main", { command: "bash", path: "/workspace" })
  await api.json(
    "DELETE",
    `/api/environments/${env}/sandboxes/${id}/terminals/dev`
  )
  await expect(
    lines.getByRole("link", { name: /^main · workspace · you, / })
  ).toBeVisible({ timeout: 30_000 })
  await expect(
    lines.getByRole("link", { name: /^dev · pnpm dev · you, .+–\d/ })
  ).toBeVisible()
  await expect(lines.getByText("3 open · 1 closed")).toBeVisible()

  // A sandbox folds into one line, and unfolds again.
  await lines.getByRole("button", { name: `Fold ${sandbox}` }).click()
  await expect(
    lines.getByRole("img", { name: `${sandbox}, folded: 4 sessions` })
  ).toBeVisible()
  await lines.getByRole("button", { name: `Unfold ${sandbox}` }).click()

  // A chat's branch opens its thread.
  await lines
    .getByRole("link", { name: `chat · ${name} · Please run the tests` })
    .locator("text")
    .last()
    .click()
  await expect(page).toHaveURL(
    new RegExp(`/teammates/${teammate}\\?thread=${chat.id}`)
  )
})
