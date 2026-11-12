import { addTeammate, approvals, expect, sandboxName, test } from "./fixtures"

// The login fixture puts the onboarding away for every other spec; these bring it back and put
// it away again after, since a worker's specs share one Studio.
test.afterEach(async ({ api, env }) => {
  await api.json("PUT", `/api/onboarding?env=${env}`, {
    seen: [],
    dismissed: true,
    tipsOff: true,
  })
})

test("Home's Get to know Studio card starts a first task in a new chat", async ({
  page,
  api,
  env,
}) => {
  const teammate = await addTeammate(
    api,
    env,
    `Onboard ${Math.random().toString(36).slice(2, 6)}`
  )
  await api.createSandbox(sandboxName("onboard"), teammate)
  await api.json("PUT", `/api/onboarding?env=${env}`, {
    seen: [],
    dismissed: false,
    tipsOff: true,
  })

  await page.goto(`/e/${env}`)
  const card = page.getByRole("region", { name: "Get to know Studio" })
  await expect(card).toBeVisible()
  for (const name of [
    "Network",
    "Pushes and PRs",
    "Browser",
    "Previews",
    "Secrets",
  ])
    await expect(
      card.getByRole("listitem").filter({ hasText: name })
    ).toHaveCount(1)
  // Secrets links to its page until the environment has one; other specs may have added one.
  const o = (await api.json<{ features: { key: string; happened: boolean }[] }>(
    "GET",
    `/api/onboarding?env=${env}`
  ))!
  if (!o.features.find((f) => f.key === "secret")?.happened)
    await expect(card.getByRole("link", { name: "Try it" })).toHaveAttribute(
      "href",
      `/e/${env}/settings/secrets`
    )

  // The first starter is picked; Start opens a new chat with it to edit.
  const tasks = card.getByRole("radiogroup", { name: "First task" })
  await expect(tasks.getByRole("radio").first()).toHaveAttribute(
    "aria-checked",
    "true"
  )
  await card.getByRole("button", { name: /^Start with / }).click()
  await expect(page).toHaveURL(/\/chats\/new\?sandbox=/)
  await expect(
    page.getByRole("main").getByRole("textbox", { name: "Message" })
  ).toContainText("health check endpoint")

  // Not now puts the card away.
  await page.goto(`/e/${env}`)
  await card.getByRole("button", { name: "Not now" }).click()
  await expect(card).toBeHidden()
  await expect
    .poll(
      async () =>
        (await api.json<{ dismissed: boolean }>(
          "GET",
          `/api/onboarding?env=${env}`
        ))!.dismissed
    )
    .toBe(true)
})

test("a feature's tip shows the first time it happens, once, and Settings shows them again", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("tips"))
  await api.json("PUT", `/api/onboarding?env=${env}`, {
    seen: [],
    dismissed: false,
    tipsOff: false,
  })

  // The first held request: its card on Home carries the network tip.
  await page.goto(`/e/${env}`)
  await api.connect(
    id,
    `tip-${Math.random().toString(36).slice(2, 8)}.example.org`
  )
  await expect(
    approvals(page)
      .getByTestId(/^approval-(card|group)$/)
      .first()
  ).toBeVisible()
  // The request itself ticks Network off on the card.
  const card = page.getByRole("region", { name: "Get to know Studio" })
  const network = card.getByRole("listitem").filter({ hasText: "Network" })
  await expect(network.getByLabel("seen")).toBeVisible()
  const tip = page.getByTestId("feature-tip")
  await expect(tip).toHaveAttribute("data-feature", "network")
  await expect(tip).toContainText("1 of 5 · Network")
  await expect(tip).toContainText("A request waits for you")
  await tip.getByRole("button", { name: "Got it" }).click()
  await expect(tip).toBeHidden()
  await page.reload()
  await expect(
    approvals(page)
      .getByTestId(/^approval-(card|group)$/)
      .first()
  ).toBeVisible()
  await expect(tip).toBeHidden()

  // The Secrets page explains itself at Add secret; Stop tips closes every tip.
  await page.goto(`/e/${env}/settings/secrets`)
  await expect(tip).toHaveAttribute("data-feature", "secret")
  await tip.getByRole("button", { name: "Stop tips" }).click()
  await expect(tip).toBeHidden()
  await expect
    .poll(
      async () =>
        (await api.json<{ tipsOff: boolean }>(
          "GET",
          `/api/onboarding?env=${env}`
        ))!.tipsOff
    )
    .toBe(true)
  await page.reload()
  await expect(page.getByRole("button", { name: "Add secret" })).toBeVisible()
  await expect(tip).toBeHidden()

  // Settings → General shows them again.
  await page.goto(`/e/${env}/settings`)
  await page.getByRole("button", { name: "Show tips again" }).click()
  await page.goto(`/e/${env}/settings/secrets`)
  await expect(tip).toHaveAttribute("data-feature", "secret")
  await api.dismissApprovals()
})
