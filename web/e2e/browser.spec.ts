import { addTeammate, expect, sandboxName, test } from "./fixtures"

type Pattern = { id: string; origin: string; action: string; verdict: string }

// Without a browser VM the window says so and offers to open a page; the rail beside it has the
// activity and the sites, whose rules are set in plain words.
test("a teammate's browser tab shows the sleeping window, its activity and its sites", async ({
  page,
  api,
  env,
}) => {
  const name = `Browse ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const patterns = `/api/environments/${env}/teammates/${id}/browser/patterns`
  await api.json("POST", patterns, {
    origin: "https://shop.example.com",
    action: "*",
    verdict: "allow",
  })

  await page.goto(`/e/${env}/teammates/${id}/browser`)
  await expect(page.getByText(`${name} has no browser yet`)).toBeVisible()
  await expect(page.getByLabel("Open a page")).toBeVisible()
  await expect(page.getByText(/Nothing yet\. Every step/)).toBeVisible()

  await page.getByRole("tab", { name: "Sites" }).click()
  const shop = page.getByRole("region", { name: "shop.example.com" })
  await expect(shop.getByRole("radio", { name: "Allowed" })).toBeChecked()

  // Blocked replaces the site-wide rule; Ask first removes it.
  await shop.getByRole("radio", { name: "Blocked" }).click()
  await expect(shop.getByRole("radio", { name: "Blocked" })).toBeChecked()
  let list = (await api.json<Pattern[]>("GET", patterns))!
  expect(list.map((p) => [p.origin, p.action, p.verdict])).toEqual([
    ["https://shop.example.com", "*", "deny"],
  ])
  await shop.getByRole("radio", { name: "Ask first" }).click()
  await expect
    .poll(async () => (await api.json<Pattern[]>("GET", patterns))!)
    .toEqual([])
  // A site without rules or visits leaves the list.
  await expect(shop).toBeHidden()

  // A field to hide, from the advanced form, shows as a chip on its site and can be removed.
  await page.getByRole("button", { name: /Add a rule or hide a field/ }).click()
  const form = page.getByRole("form", { name: "Add a browser rule" })
  await form.getByRole("combobox", { name: "Rule" }).click()
  await page.getByRole("option", { name: "Hide field" }).click()
  await form.getByLabel("Site").fill("https://bank.example.com")
  await form.getByLabel("Label").fill("Card number")
  await form.getByRole("button", { name: "Add rule" }).click()
  const bank = page.getByRole("region", { name: "bank.example.com" })
  await expect(bank.getByText("“Card number”")).toBeVisible()
  await bank.getByRole("button", { name: "Remove the rule" }).click()
  await expect(bank).toBeHidden()
  list = (await api.json<Pattern[]>("GET", patterns))!
  expect(list).toEqual([])
})

// The same window sits beside a sandbox's terminal, and opens on the teammate's page.
test("a teammate's sandbox opens the browser as a side panel", async ({
  page,
  api,
  env,
}) => {
  const name = `Panel ${Math.random().toString(36).slice(2, 8)}`
  const tm = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("browse"), tm)
  await page.goto(`/e/${env}/sandboxes/${id}/work`)
  await page.getByRole("button", { name: "Browser", exact: true }).click()
  const panel = page.getByRole("complementary", { name: "Teammate browser" })
  await expect(panel.getByText(`${name}'s browser`)).toBeVisible()
  await expect(panel.getByText(`${name} has no browser yet`)).toBeVisible()
  await panel.getByRole("link", { name: `Open on ${name}'s page` }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${tm}/browser$`))
  await expect(page.getByRole("tab", { name: "Activity" })).toBeVisible()
})
