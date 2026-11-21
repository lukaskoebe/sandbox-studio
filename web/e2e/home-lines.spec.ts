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

  const lines = page.getByRole("region", { name: "Computers" })
  // Other tests' sandboxes have a main terminal and a preview on :3000 too.
  const own = lines.getByRole("img", { name: `${sandbox} with its sessions` })
  await expect(
    lines.getByRole("link", { name: `chat · ${name} · Please run the tests` })
  ).toBeVisible()
  await expect(lines.getByText(/is working|needs you/)).toBeVisible()
  const main = lines.getByRole("link", {
    name: /^main · go test \.\/\.\.\. · you, /,
  })
  await expect(main).toBeVisible({ timeout: 30_000 })
  await expect(
    lines.getByRole("link", { name: /^dev · pnpm dev · you, / })
  ).toBeVisible()
  await expect(
    own.getByRole("link", { name: /^preview :3000, / })
  ).toBeVisible()

  // The tests end and the dev server's terminal closes: it rejoins the line.
  await api.setTerminal(id, "main", { command: "bash", path: "/workspace" })
  await api.json(
    "DELETE",
    `/api/environments/${env}/sandboxes/${id}/terminals/dev`
  )
  await expect(
    own.getByRole("link", { name: /^main · workspace · you, / })
  ).toBeVisible({ timeout: 30_000 })
  await expect(
    lines.getByRole("link", { name: /^dev · pnpm dev · you, .+–\d/ })
  ).toBeVisible()
  await expect(lines.getByText("3 open · 1 closed")).toBeVisible()

  // The last half hour: the terminals' output was counted, and the chat's question to the user
  // is flagged as waiting.
  const half = lines.getByRole("button", { name: "30m" })
  await half.click()
  await expect(half).toHaveAttribute("aria-pressed", "true")
  await expect
    .poll(
      async () =>
        (await api.json<{ activity: { kind: string; key: string }[] }>(
          "GET",
          `/api/environments/${env}/sessions?minutes=30`
        ))!.activity.some(
          (a) => a.kind === "terminal" && a.key.startsWith("main@")
        ),
      { timeout: 30_000 }
    )
    .toBe(true)
  await expect(
    lines.locator("title").filter({ hasText: /asked you: .+ → waiting/ })
  ).not.toHaveCount(0)
  await lines.getByRole("button", { name: "2h", exact: true }).click()

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

  // A crowd of previews goes on one branch, which opens the Preview tab on one of its ports,
  // saying that previews are off and turning them on there.
  const previews = await api.json<{ enabled: boolean }>(
    "GET",
    "/api/system/previews"
  )
  await api.json("PUT", "/api/system/previews", { enabled: false })
  try {
    await api.json("PUT", `/api/e2e/sandboxes/${id}/ports`, {
      ports: [3000, 5173, 8080, 5432],
    })
    await page.goto(`/e/${env}`)
    const crowd = lines.getByRole("link", {
      name: /^4 previews · :3000 :5173 :5432 :8080, /,
    })
    await expect(crowd).toBeVisible({ timeout: 30_000 })
    await expect(
      own.getByRole("link", { name: /^preview :5173, / })
    ).toHaveCount(0)
    await crowd.locator("text").last().click()
    await expect(page).toHaveURL(/tab=preview&port=(5173|8080|5432)/)
    const off = page.getByText("Previews are off")
    await expect(off).toBeVisible()
    await expect(
      page.getByText(/^Port (5173|8080|5432) can't open here/)
    ).toBeVisible()
    await page.getByRole("button", { name: "Turn on previews" }).click()
    await expect(off).toHaveCount(0)
    await expect(page.getByRole("combobox", { name: "Port" })).toHaveText(
      /localhost:(5173|8080|5432)/
    )
  } finally {
    await api.json("PUT", "/api/system/previews", {
      enabled: previews!.enabled,
    })
  }
})

test("a terminal stored under keys a second apart is one branch on home", async ({
  page,
  api,
  env,
}) => {
  // Before Studio named terminals by the guest's own clock, the shift into the host's came
  // out a second apart now and then: the same terminal under two keys, a row for each return.
  const sandbox = sandboxName("keys")
  const id = await api.createSandbox(sandbox)
  const now = Date.now()
  const at = (minAgo: number, sec = 0) =>
    new Date(Math.floor((now - minAgo * 60_000) / 1000) * 1000 + sec * 1000)
  const iso = (d: Date) => d.toISOString()
  const unix = (d: Date) => Math.floor(d.getTime() / 1000)
  const created = at(90)
  const key = (sec: number) => `main@${unix(created) + sec}`
  const term = (n: number, sec: number, closed?: Date) => ({
    id: `t${n}`,
    sandboxId: id,
    kind: "terminal",
    key: key(sec),
    label: "workspace",
    openedAt: iso(at(90, sec)),
    closedAt: closed && iso(closed),
  })
  const started = at(60)
  const run = (n: number, sec: number, closed: Date) => ({
    id: `r${n}`,
    sandboxId: id,
    kind: "run",
    key: `${key(sec)}@${unix(started) + sec}`,
    parent: key(sec),
    label: "pnpm dev",
    openedAt: iso(at(60, sec)),
    closedAt: iso(closed),
  })
  const spans = [
    term(1, 0, at(50)),
    term(2, 1, at(49)),
    term(3, 0, at(20)),
    term(4, 1),
    run(1, 0, at(40)),
    run(2, 1, at(40)),
    {
      id: "t5",
      sandboxId: id,
      kind: "terminal",
      key: `2@${unix(at(30))}`,
      label: "workspace",
      openedAt: iso(at(30)),
    },
  ]
  await page.route("**/api/environments/*/sessions?*", (route) =>
    route.fulfill({
      json: {
        since: iso(at(120)),
        until: iso(at(0)),
        spans,
        turns: [],
        flags: [],
        events: [],
        activity: [
          {
            kind: "terminal",
            sandboxId: id,
            key: key(1),
            at: iso(at(10)),
            count: 3,
            errors: 0,
          },
        ],
      },
    })
  )
  await page.goto(`/e/${env}`)
  const lines = page.getByRole("region", { name: "Computers" })
  await lines.getByRole("button", { name: "2h", exact: true }).click()
  const own = lines.getByRole("img", { name: `${sandbox} with its sessions` })
  await expect(
    own.getByRole("link", { name: /^2 · workspace · you, / })
  ).toBeVisible()
  await expect(
    own.getByRole("link", { name: /^main · workspace · you, / })
  ).toHaveCount(1)
  await expect(lines.getByText("2 open", { exact: true })).toBeVisible()
})
