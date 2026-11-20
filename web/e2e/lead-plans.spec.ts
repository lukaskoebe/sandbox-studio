import {
  addTeammate,
  expect,
  hideApprovalStack,
  sandboxName,
  test,
} from "./fixtures"

type Pending = {
  kind: string
  leadPlan?: { title: string; planId: string }
}

type Api = Parameters<typeof addTeammate>[0]

/** Waits for the chat's turn to end, answering the permission the fake agent asks first. */
async function turnEnded(api: Api, env: string, chat: string) {
  await expect
    .poll(
      async () => {
        await api.dismissApprovals()
        const log = await api.json<{ chat: { state: string } }>(
          "GET",
          `/api/environments/${env}/chats/${chat}`
        )
        return log?.chat.state
      },
      { timeout: 30_000 }
    )
    .toBe("idle")
}

// Lead's plans with the e2e Studio's fake model (internal/e2e/lead.go): a message in the
// named teammate's thread, saying "Please " and the user's words; a question when the user's
// message says "ask me"; and a report "<name> answered in <thread>." once the turn ended.
test("Lead's plan waits for approval, then sends its message in the teammate's thread and reports the answer", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(180_000)
  await api.dismissApprovals()
  const tag = Math.random().toString(36).slice(2, 6)
  const name = `Bea ${tag}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("plan"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await api.json("POST", `/api/environments/${env}/chats/${chat.id}/prompt`, {
    text: `Tidy the ${tag} tests`,
  })
  // The fake agent asks a permission before its turn ends; answering it ends the turn.
  await turnEnded(api, env, chat.id)

  // New plan from under Lead in the sidebar.
  await page.goto(`/e/${env}/lead`)
  await hideApprovalStack(page)
  const sidebar = page.locator("[data-sidebar=sidebar]")
  await sidebar.getByRole("link", { name: "New plan" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/lead/plans/new$`))
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  await expect(crumbs.getByRole("listitem")).toHaveText(["Lead", "New plan"])
  const ask = page.getByRole("textbox", { name: "Ask Lead for a plan" })
  const words = `Ask ${name} whether the coupon tests pass`
  await ask.fill(words)
  await ask.press("Enter")

  // The proposal: one message in Bea's thread, waiting for approval. Nothing is sent yet.
  await expect(page).toHaveURL(
    new RegExp(`/e/${env}/lead/plans/(?!new$)[^/]+$`)
  )
  const planId = new URL(page.url()).pathname.split("/").at(-1)!
  const heading = page.getByRole("heading", { level: 1 })
  await expect(heading).toHaveText("Check the tests · Waits for approval", {
    timeout: 30_000,
  })
  await expect(crumbs.getByRole("listitem")).toHaveText([
    "Lead",
    "Check the tests",
  ])
  await expect(page.getByText(words, { exact: true })).toBeVisible()
  const says = page.getByRole("article", { name: "Lead says" })
  await expect(says).toContainText(`I'll ask in Tidy the ${tag} tests.`)
  const steps = page.getByRole("list", { name: "Steps" })
  await expect(steps).toContainText(`To ${name} in “Tidy the ${tag} tests”`)
  await expect(steps).toContainText(`Please ${words}`)
  await expect(steps).toContainText("Wait for the teammates to answer")
  await expect(
    sidebar.getByRole("link", { name: /Check the tests\s*Waits for approval/ })
  ).toBeVisible()
  const pending = (await api.json<Pending[]>(
    "GET",
    "/api/approvals?status=pending"
  ))!
  expect(
    pending.filter(
      (a) => a.kind === "lead.plan" && a.leadPlan?.planId === planId
    )
  ).toHaveLength(1)

  // Approve: the message goes out, and once Bea's turn ended Lead reports what came of it.
  await page.getByRole("button", { name: "Approve and send" }).click()
  await expect(page.getByText("You approved the plan.")).toBeVisible()
  await expect(steps.locator("..")).toContainText(
    /Approved by you at \d\d:\d\d/
  )
  await expect(steps).toContainText(/sent \d\d:\d\d/, { timeout: 60_000 })
  const report = page.getByRole("article", { name: "Lead's report" })
  await expect
    .poll(
      async () => {
        await api.dismissApprovals()
        return report.count()
      },
      { timeout: 90_000, intervals: [2_000] }
    )
    .toBe(1)
  await expect(report).toContainText(
    `${name} answered in Tidy the ${tag} tests.`
  )
  await expect(heading).toHaveText("Check the tests · Done")
  await expect(page.getByText("This plan is done.")).toBeVisible()

  // The thread shows the message as Lead's, approved by the user, with a link to the plan.
  await report.getByRole("link", { name: new RegExp(name) }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/teammates/${teammate}`))
  await expect(page.getByText(`Please ${words}`)).toBeVisible()
  await expect(
    page.getByText(/Lead, approved by you at \d\d:\d\d/)
  ).toBeVisible()
  await page.getByRole("link", { name: "Check the tests" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/lead/plans/${planId}$`))
  await api.dismissApprovals()
})

test("Lead asks back with answers to pick, and a cancelled plan withdraws its approval", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(120_000)
  await api.dismissApprovals()
  const tag = Math.random().toString(36).slice(2, 6)
  const name = `Cy ${tag}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("ask"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await api.json("POST", `/api/environments/${env}/chats/${chat.id}/prompt`, {
    text: `Build the ${tag} app`,
  })
  await turnEnded(api, env, chat.id)

  // A day's suggestion, or the Ask caption, starts a plan with words of its own.
  await page.goto(
    `/e/${env}/lead/plans/new?text=${encodeURIComponent(`Have ${name} rebuild, but ask me which branch`)}`
  )
  await hideApprovalStack(page)
  const ask = page.getByRole("textbox", { name: "Ask Lead for a plan" })
  await expect(ask).toHaveValue(`Have ${name} rebuild, but ask me which branch`)
  await ask.press("Enter")

  // Lead asks back; an answer is one click.
  const heading = page.getByRole("heading", { level: 1 })
  await expect(heading).toHaveText("Check the tests · Your turn", {
    timeout: 30_000,
  })
  const answers = page.getByRole("group", { name: "Answers" })
  await expect(answers.getByRole("button")).toHaveText(["main", "release"])
  await answers.getByRole("button", { name: "main" }).click()

  // "main" names no teammate, so the fake asks the first one; a proposal and its approval.
  await expect(heading).toHaveText("Check the tests · Waits for approval", {
    timeout: 30_000,
  })
  await expect(answers.getByRole("button", { name: "main" })).toBeDisabled()
  await expect(answers.getByRole("button", { name: "main" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  const planId = new URL(page.url()).pathname.split("/").at(-1)!
  const mine = async () =>
    (
      (await api.json<Pending[]>("GET", "/api/approvals?status=pending")) ?? []
    ).filter((a) => a.kind === "lead.plan" && a.leadPlan?.planId === planId)
      .length
  expect(await mine()).toBe(1)

  // Cancel: the approval goes, nothing was sent, and the page offers a new plan.
  await page.getByRole("button", { name: "Cancel plan" }).click()
  await expect(heading).toHaveText("Check the tests · Cancelled")
  await expect(
    page.getByText("Cancelled. Messages already sent stay in their threads.")
  ).toBeVisible()
  await expect(
    page.getByRole("link", { name: "Make a new plan" })
  ).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Approve and send" })
  ).toHaveCount(0)
  expect(await mine()).toBe(0)
  await api.dismissApprovals()
})
