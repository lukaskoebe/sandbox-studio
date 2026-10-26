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
  await expect(page.getByRole("heading", { name: "Pages" })).toBeVisible()
  // The link to a page that doesn't exist is a loose end.
  await expect(page.getByText("Wanted page · from The webshop")).toBeVisible()

  await page
    .getByRole("button", { name: /^The webshop: The storefront/ })
    .click()
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

  await page.getByRole("button", { name: "Memory", exact: true }).click()
  await expect(page.getByText("Wanted page")).toBeHidden()
})
