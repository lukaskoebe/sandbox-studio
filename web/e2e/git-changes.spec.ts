import { expect, sandboxName, test } from "./fixtures"

// Every e2e computer's /workspace is a repository with the same changes (internal/e2e/files.go).
test("Changes shows git's view of the computer, and a change opens its diff and its file", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("git")
  const id = await api.createSandbox(name)
  await api.guestReady(id)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  const tab = screen.getByRole("tab", { name: /^Changes/ })
  // The tab counts the changed files.
  await expect(tab).toHaveAccessibleName("Changes, 5 changed")
  await tab.click()

  const repo = screen.getByRole("region", { name: "Repository workspace" })
  await expect(repo.getByRole("heading", { level: 3 })).toContainText(
    "fix-coupon"
  )
  await expect(
    repo.getByTitle(/^2 commits origin\/main doesn't have/)
  ).toHaveText("2")
  const area = (n: string) => repo.getByRole("list", { name: n, exact: true })
  await expect(area("Staged").getByRole("listitem")).toHaveText([
    /coupon\.ts\s*\+2 −1/,
    /coupon\.test\.ts\s*\+3/,
  ])
  await expect(area("Not staged").getByRole("listitem")).toHaveText([
    /main\.go/,
    /old\.md\s*−2/,
  ])
  await expect(area("Untracked").getByRole("listitem")).toHaveText([
    /notes\.txt/,
  ])

  // A change opens its diff, and Open in Files shows the file in the Files tab.
  await area("Not staged")
    .getByRole("button", { name: /main\.go/ })
    .click()
  await expect(area("Not staged")).toContainText("hello, shop")
  // The diff shows stacked until Split is picked, which stays picked in this browser.
  const lines = area("Not staged").locator("pre[data-diff-type]")
  await expect(lines).toHaveAttribute("data-diff-type", "single")
  const layout = area("Not staged").getByRole("group", { name: "Diff layout" })
  await layout.getByRole("button", { name: "Split" }).click()
  await expect(lines).toHaveAttribute("data-diff-type", "split")
  expect(
    await page.evaluate(() => localStorage.getItem("studio.diff.style"))
  ).toBe("split")
  await layout.getByRole("button", { name: "Stacked" }).click()
  await expect(lines).toHaveAttribute("data-diff-type", "single")
  await area("Not staged")
    .getByRole("button", { name: "Open in Files" })
    .click()
  await expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  await expect(page).toHaveURL(/file=main\.go/)
  await expect(
    screen.locator("pre .tok-keyword", { hasText: "package" })
  ).toBeVisible()
  // Back returns to Changes.
  await page.goBack()
  await expect(tab).toHaveAttribute("aria-selected", "true")
  await area("Untracked")
    .getByRole("button", { name: /notes\.txt/ })
    .click()
  await expect(area("Untracked")).toContainText("Ask about free shipping.")

  // The Changes tab returns to Changes too, though Files keeps its file in the URL.
  await area("Untracked").getByRole("button", { name: "Open in Files" }).click()
  await expect(page).toHaveURL(/file=notes\.txt/)
  await tab.click()
  await expect(tab).toHaveAttribute("aria-selected", "true")
  await area("Staged")
    .getByRole("button", { name: /coupon\.test\.ts/ })
    .click()
  await expect(
    area("Staged").locator("pre[data-diff-type]").first()
  ).toBeVisible()
})

test("A part of Studio that fails to load says so in its place, and the rest stays", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("gitload")
  const id = await api.createSandbox(name)
  await api.guestReady(id)
  // As after an update: the page asks for a file of the version it loaded with, which is gone.
  await page.route(/\/assets\/diff-view-[^/]+\.js$/, (route) =>
    route.fulfill({ status: 404, body: "404 page not found" })
  )
  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  const notStaged = screen.getByRole("list", {
    name: "Not staged",
    exact: true,
  })
  await notStaged.getByRole("button", { name: /main\.go/ }).click()
  const notice = notStaged.getByRole("alert")
  await expect(notice).toContainText("Couldn't show the changes")
  await expect(notice).toContainText("Studio was updated")
  await expect(notice.getByRole("button", { name: "Reload" })).toBeVisible()
  await expect(notice.getByRole("button", { name: "Try again" })).toHaveCount(0)
  // The rest of the screen still works.
  await screen.getByRole("tab", { name: "Files" }).click()
  await expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
})
