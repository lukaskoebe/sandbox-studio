import { addTeammate, expect, sandboxName, test } from "./fixtures"

type Api = Parameters<typeof addTeammate>[0]

type Approval = { id: string; environmentId: string; kind: string }

/**
 * Answers the permissions the fake agent asks before its turns end, and leaves Lead's plans
 * alone: a dismissed lead.plan approval cancels its plan.
 */
async function answerPermissions(api: Api) {
  const pending =
    (await api.json<Approval[]>("GET", "/api/approvals?status=pending")) ?? []
  for (const a of pending)
    if (a.kind !== "lead.plan")
      await api.json(
        "POST",
        `/api/environments/${a.environmentId}/approvals/${a.id}/decide`,
        { action: "dismiss", scope: "environment" }
      )
}

/** A teammate with a computer and a thread whose first turn has ended. */
async function teammateWithThread(api: Api, env: string, name: string) {
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("lead"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await api.json("POST", `/api/environments/${env}/chats/${chat.id}/prompt`, {
    text: `Tidy the ${name} tests`,
  })
  await expect
    .poll(
      async () => {
        await answerPermissions(api)
        const log = await api.json<{ chat: { state: string } }>(
          "GET",
          `/api/environments/${env}/chats/${chat.id}`
        )
        return log?.chat.state
      },
      { timeout: 30_000 }
    )
    .toBe("idle")
  return { teammate, chat: chat.id }
}

type Settings = Record<string, unknown> & { followUps?: boolean }

async function setFollowUps(api: Api, env: string, on: boolean) {
  const v = (await api.json<{ settings: Settings }>(
    "GET",
    `/api/environments/${env}/lead`
  ))!
  const s = v.settings
  await api.json("PUT", `/api/environments/${env}/lead/settings`, {
    name: s.name,
    writeAt: s.writeAt,
    days: s.days,
    skipQuiet: s.skipQuiet,
    ownComputers: s.ownComputers,
    showCosts: s.showCosts,
    heading: s.heading,
    dailyLimit: s.dailyLimit,
    notify: s.notify,
    followUps: on,
  })
}

// Follow-ups with the e2e Studio's fake model (internal/e2e/lead.go): when the user's words
// say "follow up", the first report follows up once in the round's first thread with "Please
// also run the slow tests."; the second report is plain sentences.
test("a follow-up in a teammate's own thread waits for approval, then goes and is reported", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(240_000)
  const tag = Math.random().toString(36).slice(2, 6)
  const name = `Dee ${tag}`
  const { teammate, chat } = await teammateWithThread(api, env, name)

  await page.goto(`/e/${env}/lead/plans/new`)
  const ask = page.getByRole("textbox", { name: "Ask Lead for a plan" })
  await ask.fill(`Ask ${name} whether the tests pass, and follow up`)
  await ask.press("Enter")
  const heading = page.getByRole("heading", { level: 1 })
  await expect(heading).toHaveText("Check the tests · Waits for approval", {
    timeout: 30_000,
  })
  await page.getByRole("button", { name: "Approve and send" }).click()

  // The first report asks to follow up; the plan waits for approval again.
  const followUp = page.getByRole("group", { name: "Follow-up 1" })
  await expect
    .poll(
      async () => {
        await answerPermissions(api)
        return followUp.count()
      },
      { timeout: 90_000, intervals: [2_000] }
    )
    .toBe(1)
  await expect(heading).toHaveText("Check the tests · Waits for approval")
  await expect(
    page.getByText(
      "I'd follow up before this is done. Nothing goes out until you approve it."
    )
  ).toBeVisible()
  await expect(followUp).toContainText(`To ${name} in “Tidy the ${name} tests”`)
  await expect(followUp).toContainText("Please also run the slow tests.")
  const first = page.getByRole("group", { name: "The plan's messages" })
  await expect(first).toContainText(/Approved by you at \d?\d:\d\d/)
  await expect(page.getByRole("article", { name: "Lead's report" })).toHaveText(
    new RegExp(`${name} answered in Tidy the ${name} tests.`)
  )

  await followUp.getByRole("button", { name: "Approve and send" }).click()
  await expect(page.getByText("You approved the follow-up.")).toBeVisible()
  await expect(followUp).toContainText(
    /Follow-up approved by you at \d?\d:\d\d/
  )
  await expect
    .poll(
      async () => {
        await answerPermissions(api)
        return page.getByRole("article", { name: "Lead's report" }).count()
      },
      { timeout: 90_000, intervals: [2_000] }
    )
    .toBe(2)
  await expect(heading).toHaveText("Check the tests · Done")

  // A done plan offers to become a standing order with the same words.
  await page
    .getByRole("link", { name: "make this one a standing order" })
    .click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/lead/orders\\?text=`))
  await expect(
    page.getByRole("textbox", { name: "What Lead should do" })
  ).toHaveValue(`Ask ${name} whether the tests pass, and follow up`)

  // The thread has both messages, each approved by the user.
  await page.goto(`/e/${env}/teammates/${teammate}?thread=${chat}`)
  await expect(page.getByText("Please also run the slow tests.")).toBeVisible()
  await expect(page.getByText(/Lead, approved by you at/)).toHaveCount(2)
})

test("with follow-ups allowed, Lead follows up in a thread its plan started without asking", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(240_000)
  const tag = Math.random().toString(36).slice(2, 6)
  const name = `Eve ${tag}`
  await teammateWithThread(api, env, name)
  await setFollowUps(api, env, true)
  try {
    await page.goto(`/e/${env}/lead/settings`)
    await expect(
      page.getByRole("combobox", { name: "When Lead messages teammates" })
    ).toContainText("Follow-ups in its own threads")

    await page.goto(`/e/${env}/lead/plans/new`)
    const ask = page.getByRole("textbox", { name: "Ask Lead for a plan" })
    await ask.fill(`Have ${name} check in a new thread, and follow up`)
    await ask.press("Enter")
    const heading = page.getByRole("heading", { level: 1 })
    await expect(heading).toHaveText("Check the tests · Waits for approval", {
      timeout: 30_000,
    })
    await page.getByRole("button", { name: "Approve and send" }).click()

    // The follow-up goes at once, and the plan ends after its second report.
    await expect
      .poll(
        async () => {
          await answerPermissions(api)
          return page.getByRole("article", { name: "Lead's report" }).count()
        },
        { timeout: 120_000, intervals: [2_000] }
      )
      .toBe(2)
    await expect(heading).toHaveText("Check the tests · Done")
    const followUp = page.getByRole("group", { name: "Follow-up 1" })
    await expect(followUp).toContainText(`To ${name} in “Lead's errand”`)
    await expect(followUp).toContainText(
      "Sent without asking, in a thread this plan started, as Lead's settings allow."
    )
    await expect(
      page.getByText(
        "I'm following up in the threads this plan started, without asking, as my settings allow."
      )
    ).toBeVisible()

    await page
      .getByRole("article", { name: "Lead's report" })
      .last()
      .getByRole("link", { name: /Lead's errand/ })
      .click()
    await expect(
      page.getByText("Please also run the slow tests.")
    ).toBeVisible()
    await expect(page.getByText("Lead, follow-up without asking")).toBeVisible()
  } finally {
    await setFollowUps(api, env, false)
  }
})

test("a standing order drafts its plan on Run now, waits for approval, and can be turned off and deleted", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(120_000)
  const tag = Math.random().toString(36).slice(2, 6)
  const name = `Fay ${tag}`
  await teammateWithThread(api, env, name)
  const words = `Ask ${name} whether the ${tag} tests still pass`

  await page.goto(`/e/${env}/lead`)
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  await crumbs.getByRole("button", { name: "Lead's pages: Lead" }).click()
  await page.getByRole("menuitem", { name: "Standing orders" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/lead/orders$`))
  await expect(crumbs.getByRole("listitem")).toHaveText([
    "Lead",
    "Standing orders",
  ])

  // Midnight has passed today, so the order first comes due tomorrow or later.
  const form = page.getByRole("form", { name: "New standing order" })
  await form.getByRole("textbox", { name: "What Lead should do" }).fill(words)
  await form.getByLabel("Comes due at").fill("00:00")
  await form.getByRole("button", { name: "Add order" }).click()
  const order = page.getByRole("group", { name: `Standing order: ${words}` })
  await expect(order).toContainText(/Comes due next (tomorrow|Mon) /)

  // Run now drafts the plan at once; it waits for approval like any other.
  await order.getByRole("button", { name: "Run now" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/lead/plans/[^/]+$`))
  const heading = page.getByRole("heading", { level: 1 })
  await expect(heading).toHaveText("Check the tests · Waits for approval", {
    timeout: 30_000,
  })
  await expect(
    page.getByText("Lead drafted this plan for your standing order.")
  ).toBeVisible()
  await expect(page.getByText(words, { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Cancel plan" }).click()
  await expect(heading).toHaveText("Check the tests · Cancelled")

  // Back on the order: its last plan, then off, then gone.
  await page.getByRole("link", { name: "your standing order" }).click()
  await expect(order).toContainText("last plan Check the tests, cancelled")
  await order.getByRole("switch", { name: "On" }).click()
  await expect(order).toContainText(
    "Off: it doesn't come due until you turn it on."
  )
  await order.getByRole("button", { name: "Delete" }).click()
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: "Delete" })
    .click()
  await expect(order).toHaveCount(0)
})
