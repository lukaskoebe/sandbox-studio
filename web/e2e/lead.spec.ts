import { addTeammate, expect, sandboxName, test } from "./fixtures"

// Lead writes the day with the e2e Studio's fake model (internal/e2e/lead.go): a fold note
// "Working on <thread>: <prompt>" per teammate, and a day whose heading is "a steady day".
test("Lead writes today's day from Home, the day page links its work, and the settings rename it", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(120_000)
  await api.dismissApprovals()
  const name = `Ada ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("lead"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await api.json("POST", `/api/environments/${env}/chats/${chat.id}/prompt`, {
    text: "Please run the tests",
  })

  // Home's card shows the day so far, then Summarize now writes it.
  await page.goto(`/e/${env}`)
  const card = page.getByRole("region", { name: "Today so far" })
  await expect(card).toBeVisible()
  await expect(card).toContainText(/of turns/)
  await card.getByRole("button", { name: "Summarize now" }).click()
  await expect(card.getByRole("heading", { name: "Today" })).toBeVisible({
    timeout: 30_000,
  })
  await expect(card).toContainText(/written \d\d:\d\d/)
  await expect(card).toContainText(new RegExp(`${name}.* worked today\\.`))
  await card.getByRole("link", { name: "Open the day" }).click()

  // The day: its heading, each teammate's lines with links to the threads, and the costs.
  await expect(page).toHaveURL(new RegExp(`/e/${env}/lead$`))
  const crumbs = page.getByRole("navigation", { name: "Where you are" })
  await expect(crumbs.getByRole("listitem")).toHaveText(["Lead", "Today"])
  await expect(
    page.getByRole("heading", { level: 1, name: /· a steady day$/ })
  ).toBeVisible()
  const block = page.getByRole("region", { name, exact: true })
  await expect(block).toContainText("done")
  await expect(
    block.getByRole("link", { name: /Please run the tests/ }).first()
  ).toBeVisible()
  await expect(page.getByRole("region", { name: "Costs" })).toContainText(
    "Lead"
  )
  await expect(
    page.getByText(/written \d\d:\d\d( [AP]M)? on request · e2e-writer/)
  ).toBeVisible()

  // Copy as Markdown puts the day on the clipboard.
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"])
  await page.getByRole("button", { name: "Copy as Markdown" }).click()
  await expect(page.getByText("The day is copied as Markdown")).toBeVisible()
  const markdown = await page.evaluate(() => navigator.clipboard.readText())
  expect(markdown).toMatch(/^# \w+day \d+ \w+ \d{4} · a steady day/)
  expect(markdown).toContain(`## ${name}`)

  // Ask Lead: the answer comes back under the question, linking the thread it speaks of.
  const ask = page.getByRole("form", { name: "Ask Lead" })
  const question = `What did ${name} get done?`
  const box = ask.getByLabel("Your question for Lead")
  await expect(box).toHaveAttribute("placeholder", "Ask Lead about today…")
  await box.fill(question)
  await box.press("Enter")
  const qa = page.getByRole("article", { name: question })
  await expect(qa).toContainText(`You asked: ${question}`, { timeout: 30_000 })
  await expect(qa.getByRole("link").first()).toBeVisible()
  await expect(qa).toContainText(/\d\d:\d\d( [AP]M)? · e2e-writer/)
  await expect(box).toHaveValue("")

  // The sidebar has Lead at the top of Teammates.
  const sidebar = page.locator("[data-sidebar=sidebar]")
  // A thread another test left, such as "Lead's errand", isn't Lead's row.
  await expect(sidebar.getByRole("link", { name: /^Lead(?!'s)/ })).toBeVisible()

  // The settings: a heading turned off leaves the date alone; a new name reaches the sidebar.
  await page.getByRole("link", { name: "Lead's settings" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/lead/settings$`))
  await expect(crumbs.getByRole("listitem")).toHaveText(["Lead", "Settings"])
  await expect(
    page.getByRole("group", { name: "The model it calls" })
  ).toContainText("The utility model of the Anthropic provider")
  const heading = page.getByRole("switch", { name: "A heading for the day" })
  await heading.click()
  await expect(heading).not.toBeChecked()
  const rename = page.getByRole("group", { name: "Name" })
  await rename.getByLabel("Lead's name").fill("Guide")
  await rename.getByRole("button", { name: "Rename" }).click()
  await expect(sidebar.getByRole("link", { name: /^Guide/ })).toBeVisible()
  await page.getByRole("link", { name: "Back to the day" }).click()
  await expect(page.getByRole("heading", { level: 1 })).not.toContainText(
    "a steady day"
  )

  // Put things back for the tests after this one.
  const settings = (await api.json<Record<string, unknown>>(
    "GET",
    `/api/environments/${env}/lead`
  ))!.settings as Record<string, unknown>
  await api.json("PUT", `/api/environments/${env}/lead/settings`, {
    ...settings,
    updatedAt: undefined,
    name: "Lead",
    heading: true,
  })
  await api.dismissApprovals()
})
