import { addTeammate, approvals, expect, sandboxName, test } from "./fixtures"

test("a held connection asks; Allow adds a rule and clears the card in every tab", async ({
  page,
  context,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("net"))
  const host = `api-${Math.random().toString(36).slice(2, 8)}.example.org`

  // Home lists approvals in place, so the stack shows them on other pages.
  await page.goto(`/e/${env}/secrets`)
  const other = await context.newPage()
  await other.goto(`/e/${env}/memory`)
  await api.connect(id, host)

  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  const otherCard = approvals(other)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card).toBeVisible()
  await expect(otherCard).toBeVisible()

  await card.getByRole("button", { name: "Allow" }).click()
  await expect(card).toBeHidden()
  await expect(otherCard).toBeHidden()

  // The held connection went through once allowed.
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.find((c) => c.host === host)
    )
    .toMatchObject({ done: true, status: 200, body: `hello from ${host}\n` })

  await page.goto(`/e/${env}/network/rules`)
  await expect(
    page.getByRole("cell", { name: host, exact: true })
  ).toBeVisible()
})

/** A host no rule covers yet, unique to this run, so tests don't see each other's rules. */
function freshHost(label: string) {
  return `${label}-${Math.random().toString(36).slice(2, 8)}.example.org`
}

test("requests that arrive together from one sandbox are one card; Allow all makes a rule per host", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("group"))
  const hosts = [freshHost("cdn"), freshHost("img"), freshHost("api")]

  await page.goto(`/e/${env}`)
  for (const host of hosts) await api.connect(id, host)

  const group = approvals(page).getByTestId("approval-group")
  await expect(group).toContainText("wants 3 hosts")
  for (const host of hosts) await expect(group).toContainText(host)
  await expect(approvals(page).getByTestId("approval-card")).toHaveCount(0)

  await group.getByRole("button", { name: "Allow all", exact: true }).click()
  await expect(group).toBeHidden()

  // Each host got a rule of its own, made by an approval.
  await page.goto(`/e/${env}/network/rules?origin=approval`)
  for (const host of hosts) {
    await expect(
      page.getByRole("cell", { name: host, exact: true })
    ).toBeVisible()
  }
})

test("Review individually turns a group into the cards of its hosts", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("review"))
  const hosts = [freshHost("one"), freshHost("two")]

  await page.goto(`/e/${env}`)
  for (const host of hosts) await api.connect(id, host)
  const group = approvals(page).getByTestId("approval-group")
  await expect(group).toContainText("wants 2 hosts")

  await group.getByRole("button", { name: "Review individually" }).click()
  await expect(group).toBeHidden()
  for (const host of hosts) {
    await expect(
      approvals(page).getByTestId("approval-card").filter({ hasText: host })
    ).toBeVisible()
  }

  await api.dismissApprovals()
})

test("an approval can allow a host for one hour, and the Rules tab counts the rule down", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("hour"))
  const host = freshHost("hour")

  await page.goto(`/e/${env}`)
  await api.connect(id, host)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card).toBeVisible()
  await card.getByRole("button", { name: "Options", exact: true }).click()
  await card
    .getByRole("button", { name: "Allow for 1 hour", exact: true })
    .click()
  await expect(card).toBeHidden()

  await page.goto(`/e/${env}/network/rules`)
  await expect(page.getByRole("row").filter({ hasText: host })).toContainText(
    /expires in \d+ min/
  )
})

test("allow once and deny once answer the held connection without a rule; Activity says once", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("once"))
  const allowed = freshHost("once-allow")
  const denied = freshHost("once-deny")

  await page.goto(`/e/${env}`)
  await api.connect(id, allowed)
  let card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: allowed })
  await card.getByRole("button", { name: "Options", exact: true }).click()
  await card.getByRole("button", { name: "Allow once", exact: true }).click()
  await expect(card).toBeHidden()
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.find((c) => c.host === allowed)
    )
    .toMatchObject({ done: true, status: 200, body: `hello from ${allowed}\n` })

  await api.connect(id, denied)
  card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: denied })
  await card.getByRole("button", { name: "Options", exact: true }).click()
  await card.getByRole("button", { name: "Deny once", exact: true }).click()
  await expect(card).toBeHidden()
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.find((c) => c.host === denied)
    )
    .toMatchObject({ done: true, status: 403 })

  // Neither answer made a rule, and Activity names both.
  await page.goto(`/e/${env}/network/rules?q=once-`)
  await expect(page.getByText("No rules match these filters.")).toBeVisible()
  await page.goto(`/e/${env}/network/activity`)
  await expect(
    page.getByRole("row").filter({ hasText: allowed })
  ).toContainText("Approved once")
  await expect(page.getByRole("row").filter({ hasText: denied })).toContainText(
    "Denied once"
  )
})

// A browser navigation to a new site asks with the site; allowing the site lets the navigation
// through for the browser session, and makes no rule.
test("a browser navigation asks with its site; Allow site lets it through without a rule", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const teammate = await addTeammate(api, env, `browser-${freshLabel()}`)
  const id = await api.createSandbox(sandboxName("nav"), teammate)
  const host = `www.${freshLabel()}.example.org`

  await page.goto(`/e/${env}`)
  const decided = api.json<{ outcome: string }>("POST", "/api/e2e/requests", {
    sandboxId: id,
    host,
    port: 443,
    method: "GET",
    path: "/",
    navigation: true,
    site: "example.org",
  })
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card).toBeVisible()
  await card
    .getByRole("button", {
      name: "Allow site example.org for this browser session",
    })
    .click()
  await expect(card).toBeHidden()
  await expect(decided).resolves.toEqual({ outcome: "allowed" })

  await page.goto(`/e/${env}/network/rules`)
  await expect(page.getByRole("cell", { name: host, exact: true })).toBeHidden()
})

function freshLabel() {
  return Math.random().toString(36).slice(2, 8)
}

test("an approval shows once: in the teammate's conversation, not also in the stack", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const teammate = await addTeammate(api, env, `Once ${sandboxName("t")}`)
  const id = await api.createSandbox(sandboxName("once"), teammate)
  const host = `once-${Math.random().toString(36).slice(2, 8)}.example.org`

  await page.goto(`/e/${env}/teammates/${teammate}`)
  await api.connect(id, host)
  const cards = page.getByTestId("approval-card").filter({ hasText: host })
  await expect(cards).toHaveCount(1)
  // Elsewhere the stack shows it.
  await page.goto(`/e/${env}/secrets`)
  await expect(cards).toHaveCount(1)
  await api.dismissApprovals()
})
