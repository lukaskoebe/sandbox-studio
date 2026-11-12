import { expect, test } from "./fixtures"

test("memory pages link each other: a link opens its page, which lists what links to it, and a wanted page is created from its link", async ({
  page,
  api,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const env = await api.json<{ id: string }>("POST", "/api/environments", {
    name: `Memory pages ${suffix}`,
  })
  const id = env!.id
  const pages = `/api/environments/${id}/memory/pages`
  await api.json("POST", pages, {
    scope: "shared",
    slug: "project/webshop",
    title: "The webshop",
    kind: "project",
    compiled:
      "The storefront. It deploys with [[topic/deploys|the deploy steps]] and pays through [[topic/payments]].",
  })
  await api.json("POST", pages, {
    scope: "shared",
    slug: "topic/deploys",
    title: "Deploys",
    kind: "topic",
    compiled: "Deploys go through staging first.",
  })

  await page.goto(`/e/${id}/settings/memory`)
  const index = page.getByRole("navigation", { name: "Memory index" })
  // The link to a page that doesn't exist is a loose end, with what needs the user.
  await index.getByRole("button", { name: /^Needs you/ }).click()
  await expect(page.getByText("Wanted page · from The webshop")).toBeVisible()

  await index.getByRole("button", { name: /^The webshop/ }).click()
  await expect(page.getByRole("heading", { name: "The webshop" })).toBeVisible()
  await expect(page.getByText("(no page yet)")).toBeVisible()
  await expect(
    page.getByText("~/memory/shared/pages/project/webshop.md")
  ).toBeVisible()

  await page.getByRole("button", { name: "the deploy steps" }).click()
  await expect(page.getByRole("heading", { name: "Deploys" })).toBeVisible()
  const linked = page.getByRole("button", {
    name: /^The webshop project\/webshop/,
  })
  await expect(linked).toBeVisible()
  await linked.click()

  await page.getByRole("button", { name: "topic/payments" }).click()
  const dialog = page.getByRole("dialog", { name: "New page" })
  await expect(dialog.getByLabel("Slug")).toHaveValue("topic/payments")
  await expect(dialog.getByLabel("Title")).toHaveValue("Payments")
  await dialog
    .getByLabel("Compiled truth")
    .fill("Payments use the hosted checkout.")
  await dialog.getByRole("button", { name: "Save" }).click()
  await expect(page.getByRole("heading", { name: "Payments" })).toBeVisible()
  await expect(
    page.getByRole("button", { name: /^The webshop project\/webshop/ })
  ).toBeVisible()

  await index.getByRole("button", { name: /^Needs you/ }).click()
  await expect(page.getByText("Wanted page")).toBeHidden()
})

test("memory is an index and a reader: pages folded by kind, a page's facts by kind, the facts on no page, and on a narrow screen one at a time", async ({
  page,
  api,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const env = await api.json<{ id: string }>("POST", "/api/environments", {
    name: `Memory index ${suffix}`,
  })
  const id = env!.id
  const shop = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${id}/memory/pages`,
    {
      scope: "shared",
      slug: "project/webshop",
      title: "The webshop",
      kind: "project",
      compiled: "The storefront.",
    }
  ))!
  await api.json("POST", `/api/environments/${id}/memory/pages`, {
    scope: "shared",
    slug: "person/ada",
    title: "Ada",
    kind: "person",
    compiled: "Runs the payments team.",
  })
  const facts = `/api/environments/${id}/memory/facts`
  for (let i = 1; i <= 11; i++)
    await api.json("POST", facts, {
      scope: "shared",
      kind: "decision",
      text: `Webshop decision number ${i}`,
      entityIds: ["project/webshop"],
      observedAt: new Date(Date.now() - i * 3_600_000).toISOString(),
    })
  await api.json("POST", facts, {
    scope: "shared",
    kind: "preference",
    text: "The webshop prefers hosted checkout",
    entityIds: ["project/webshop"],
  })
  await api.json("POST", facts, {
    scope: "shared",
    kind: "preference",
    text: "Indent with tabs",
  })
  await api.json("POST", facts, {
    scope: "shared",
    kind: "event",
    text: "The office moved in October",
  })

  await page.goto(`/e/${id}/settings/memory?open=page:${shop.id}`)
  const index = page.getByRole("navigation", { name: "Memory index" })
  await expect(
    index.getByRole("button", { name: /^Projects/ })
  ).toHaveAttribute("aria-expanded", "true")
  await expect(index.getByRole("button", { name: /^Ada/ })).toBeVisible()
  await expect(page.getByRole("heading", { name: "The webshop" })).toBeVisible()
  const kinds = page.getByRole("group", { name: "Facts by kind" })
  await expect(kinds.getByRole("button", { name: "All 12" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  // Ten show, newest first: the preference and nine decisions.
  await expect(page.getByText(/^Webshop decision number/)).toHaveCount(9)
  await page.getByRole("button", { name: "Show 2 more" }).click()
  await expect(page.getByText(/^Webshop decision number/)).toHaveCount(11)
  await kinds.getByRole("button", { name: "Preferences 1" }).click()
  await expect(page.getByText(/^Webshop decision number/)).toHaveCount(0)
  await expect(
    page.getByText("The webshop prefers hosted checkout")
  ).toBeVisible()

  // The facts on no page, by kind; the open group is in the URL.
  const loose = index.getByRole("region", { name: "Facts on no page" })
  await expect(
    loose.getByRole("button", { name: "Preferences 1" })
  ).toBeVisible()
  await loose.getByRole("button", { name: "Facts and events 1" }).click()
  await expect(page).toHaveURL(/open=facts(:|%3A)other/)
  await expect(
    page.getByRole("heading", { name: "Facts and events" })
  ).toBeVisible()
  await expect(page.getByText("The office moved in October")).toBeVisible()
  await expect(page.getByText("Indent with tabs")).toBeHidden()

  await index.getByRole("button", { name: "This week 14" }).click()
  await expect(page.getByRole("heading", { name: "This week" })).toBeVisible()
  await expect(page.getByText("Indent with tabs")).toBeVisible()

  // The filter narrows the index; Enter searches.
  await page.getByRole("textbox", { name: "Search memory" }).fill("ada")
  await expect(index.getByRole("button", { name: /^The webshop/ })).toBeHidden()
  await expect(index.getByRole("button", { name: /^Ada/ })).toBeVisible()
  await page.getByRole("textbox", { name: "Search memory" }).press("Enter")
  await expect(
    page.getByRole("heading", { name: "Search results" })
  ).toBeVisible()
  await page.getByRole("button", { name: "Clear the search" }).click()

  // On a narrow screen the index comes first, and a page opens over it.
  await page.setViewportSize({ width: 560, height: 900 })
  await page.goto(`/e/${id}/settings/memory`)
  await expect(index).toBeVisible()
  await index.getByRole("button", { name: /^The webshop/ }).click()
  await expect(index).toBeHidden()
  await expect(page.getByRole("heading", { name: "The webshop" })).toBeVisible()
  await page.getByRole("button", { name: "Back to the memory index" }).click()
  await expect(index).toBeVisible()
  await expect(page).not.toHaveURL(/open=/)
})
