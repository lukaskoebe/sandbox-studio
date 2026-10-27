import { expect, sandboxName, test } from "./fixtures"

// Every e2e computer's /workspace has the same few files (internal/e2e/files.go).
test("Files highlights a file, and Back and Forward step through the places it showed", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("files")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await screen.getByRole("tab", { name: "Changes" }).click()
  await screen.getByRole("button", { name: "Files", exact: true }).click()

  const row = (n: string) =>
    screen.getByRole("button", { name: new RegExp(`^${n}`) })
  await row("web").click()
  await row("src").click()
  await row("checkout").click()
  await row("coupon.ts").click()
  await expect(page).toHaveURL(/file=web%2Fsrc%2Fcheckout%2Fcoupon\.ts/)
  const code = screen.locator("pre")
  await expect(code).toContainText("applyCoupon")
  await expect(code.locator(".tok-keyword", { hasText: "import" })).toHaveCount(
    1
  )
  await expect(code.locator(".tok-comment")).toContainText(
    "A code applies once per cart"
  )

  // Back, as Alt+← and the mouse's back button do, goes to the places Files showed rather
  // than another page; Forward comes back.
  await page.goBack()
  await expect(row("coupon.ts")).toBeVisible()
  await page.goBack()
  await expect(row("checkout")).toBeVisible()
  await expect(page).toHaveURL(/files=web%2Fsrc(&|$)/)
  await page.goForward()
  await page.goForward()
  await expect(code).toContainText("applyCoupon")

  // A place in Files is a link: it opens there again, and Back returns to it from its folder.
  await page.reload()
  await expect(code).toContainText("applyCoupon")
  await screen.getByTitle("Back to the folder").click()
  await expect(row("coupon.ts")).toBeVisible()
  await page.goBack()
  await expect(code).toContainText("applyCoupon")

  // Go files are Go, Markdown is Markdown.
  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes&file=main.go`)
  await expect(
    screen.locator("pre .tok-keyword", { hasText: "package" })
  ).toBeVisible()
  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes&file=README.md`)
  await expect(
    screen.locator("pre .tok-keyword", { hasText: "Webshop" })
  ).toBeVisible()
})
