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
  await page.goto(`/e/${env}/settings/secrets`)
  const other = await context.newPage()
  await other.goto(`/e/${env}/settings/memory`)
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

  await page.goto(`/e/${env}/settings/network`)
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
  await page.goto(`/e/${env}/settings/network?origin=approval`)
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
  await card.getByRole("button", { name: "1 hour", exact: true }).click()
  await card
    .getByRole("button", { name: "Allow for 1 hour", exact: true })
    .click()
  await expect(card).toBeHidden()

  await page.goto(`/e/${env}/settings/network`)
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
  await card.getByRole("button", { name: "Once", exact: true }).click()
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
  await card.getByRole("button", { name: "Once", exact: true }).click()
  await card.getByRole("button", { name: "Deny once", exact: true }).click()
  await expect(card).toBeHidden()
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.find((c) => c.host === denied)
    )
    .toMatchObject({ done: true, status: 403 })

  // Neither answer made a rule, and Activity names both.
  await page.goto(`/e/${env}/settings/network?q=once-`)
  await expect(page.getByText("No rules match these filters.")).toBeVisible()
  await page.goto(`/e/${env}/settings/activity?kind=network`)
  await expect(
    page.getByRole("row").filter({ hasText: allowed })
  ).toContainText("Approved once")
  await expect(page.getByRole("row").filter({ hasText: denied })).toContainText(
    "Denied once"
  )
})

// A browser navigation to a new site asks with the site. Allowing it for the session lets it
// through without a rule; a plain Allow makes a rule and approves the site for the session too,
// so the page's requests to other hosts pass without asking.
test("an allowed navigation approves its site, and the page's requests pass", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const teammate = await addTeammate(api, env, `browser-${freshLabel()}`)
  const id = await api.createSandbox(sandboxName("nav"), teammate)
  const label = freshLabel()
  const request = (host: string, site?: string) =>
    api.json<{ outcome: string }>("POST", "/api/e2e/requests", {
      sandboxId: id,
      host,
      port: 443,
      method: "GET",
      path: site ? "/" : "/app.js",
      navigation: !!site,
      site,
    })

  await page.goto(`/e/${env}`)
  const sessionHost = `www.${label}.example.org`
  let decided = request(sessionHost, `${label}.example.org`)
  let card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: sessionHost })
  await card.getByRole("button", { name: "Options", exact: true }).click()
  await card.getByRole("button", { name: "This session", exact: true }).click()
  await expect(card).toContainText(
    `Allows the site ${label}.example.org in this browser until it stops.`
  )
  await card
    .getByRole("button", { name: "Allow for session", exact: true })
    .click()
  await expect(card).toBeHidden()
  await expect(decided).resolves.toEqual({ outcome: "allowed" })

  const ruleHost = `docs.${label}.example.com`
  decided = request(ruleHost, `${label}.example.com`)
  card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: ruleHost })
  await card.getByRole("button", { name: ruleHost, exact: true }).click()
  await card.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(card).toBeHidden()
  await expect(decided).resolves.toEqual({ outcome: "allowed" })
  // The page is on an approved site: its requests to other hosts don't ask.
  await expect(request(`cdn.${label}.example.net`)).resolves.toEqual({
    outcome: "allowed",
  })

  await page.goto(`/e/${env}/settings/network?q=${label}`)
  await expect(
    page.getByRole("cell", { name: sessionHost, exact: true })
  ).toBeHidden()
  await expect(
    page.getByRole("row").filter({ hasText: ruleHost })
  ).toBeVisible()
})

// A browser's request can be allowed for the browser session, without a rule, or by a rule for
// the teammate's browser: the sandbox's own connections to the host still ask.
test("a browser request can be allowed for the session, or by a rule for the teammate's browser", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = `browser-${freshLabel()}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("where"), teammate)
  const label = freshLabel()
  const session = `cdn.${label}.example.net`
  const browserOnly = `api.${label}.example.org`
  const request = (host: string) =>
    api.json<{ outcome: string }>("POST", "/api/e2e/requests", {
      sandboxId: id,
      host,
      port: 443,
      method: "GET",
      path: "/lib.js",
    })

  await page.goto(`/e/${env}`)
  let decided = request(session)
  let card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: session })
  await card.getByRole("button", { name: "Options", exact: true }).click()
  await card.getByRole("button", { name: session, exact: true }).click()
  await card.getByRole("button", { name: "This session", exact: true }).click()
  await card
    .getByRole("button", { name: "Allow for session", exact: true })
    .click()
  await expect(card).toBeHidden()
  await expect(decided).resolves.toEqual({ outcome: "allowed" })
  // The next request of the browser passes without asking.
  await expect(request(session)).resolves.toEqual({ outcome: "allowed" })

  decided = request(browserOnly)
  card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: browserOnly })
  await card.getByRole("button", { name: "Options", exact: true }).click()
  // The teammate's approval scope, the environment, preselects all browsers.
  await expect(
    card.getByRole("button", { name: "All browsers", exact: true })
  ).toHaveAttribute("aria-pressed", "true")
  await card
    .getByRole("button", { name: `${name}'s browser`, exact: true })
    .click()
  await card.getByRole("button", { name: browserOnly, exact: true }).click()
  await card.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(card).toBeHidden()
  await expect(decided).resolves.toEqual({ outcome: "allowed" })

  // The sandbox's own connection to the host still asks.
  await api.connect(id, browserOnly)
  await expect(
    approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: browserOnly })
  ).toBeVisible()
  await api.dismissApprovals()

  await page.goto(`/e/${env}/settings/network?q=${label}`)
  await expect(
    page.getByRole("row").filter({ hasText: browserOnly })
  ).toContainText("browser only")
  await expect(
    page.getByRole("row").filter({ hasText: browserOnly })
  ).toContainText(name)
  await expect(
    page.getByRole("cell", { name: session, exact: true })
  ).toBeHidden()
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
  await page.goto(`/e/${env}/settings/secrets`)
  await expect(cards).toHaveCount(1)
  await api.dismissApprovals()
})

test("the sidebar's Network item counts the waiting network approvals and opens the rules", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("netside"))
  await page.goto(`/e/${env}/chats`)
  const item = page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("link", { name: /^Network/ })
  await expect(item.locator('[data-status="waiting"]')).toHaveCount(0)
  await api.connect(
    id,
    `side-${Math.random().toString(36).slice(2, 8)}.example.org`
  )
  await expect(item.locator('[data-status="waiting"]')).toHaveText("1")
  await item.click()
  await expect(page).toHaveURL(/\/settings\/network/)
  await expect(item).toHaveAttribute("data-active", "")
})
