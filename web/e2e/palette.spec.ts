import { addTeammate, expect, sandboxName, test } from "./fixtures"

test("Ctrl+K and the sidebar's Search open the palette, which finds and opens places", async ({
  page,
  api,
  env,
}) => {
  const name = `Pal ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const computer = sandboxName("pal")
  const sb = await api.createSandbox(computer)
  await page.goto(`/e/${env}`)
  const search = page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("button", { name: /^Search/ })
  await expect(search).toBeVisible()
  const palette = page.getByRole("dialog", { name: "Search Studio" })
  const input = palette.getByRole("combobox", { name: "Search Studio" })

  await page.keyboard.press("Control+k")
  await expect(palette).toBeVisible()
  await input.fill(name.toLowerCase())
  const option = palette.getByRole("option", { name: new RegExp(`^${name}`) })
  await expect(option.first()).toHaveAttribute("aria-selected", "true")
  await page.keyboard.press("Enter")
  await expect(palette).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/teammates/${id}$`))

  // A computer says whose it is.
  await search.click()
  await expect(palette).toBeVisible()
  await input.fill(computer)
  await expect(
    palette.getByRole("option", { name: new RegExp(computer) }).first()
  ).toContainText("your computer")
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(new RegExp(`/e/${env}/sandboxes/${sb}`))

  // A page answers to what it holds, not only its name.
  await page.keyboard.press("Control+k")
  await input.fill("rules")
  await expect(
    palette.getByRole("group", { name: "Environment" }).getByRole("option")
  ).toContainText(["Network"])
  await palette
    .getByRole("group", { name: "Environment" })
    .getByRole("option", { name: /^Network/ })
    .click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network$`))

  // With nothing typed, it lists what was opened last.
  await page.keyboard.press("Control+k")
  const recent = palette.getByRole("group", { name: "Recent" })
  await expect(recent.getByRole("option")).toHaveCount(3)
  await expect(recent.getByRole("option").first()).toContainText("Network")
  await page.keyboard.press("Escape")
  await expect(palette).toBeHidden()
})

test("> keeps the palette to actions, which run where they are", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/general`)
  await expect(
    page.getByRole("heading", { name: "General", exact: true })
  ).toBeVisible()
  await page.keyboard.press("Control+k")
  const palette = page.getByRole("dialog", { name: "Search Studio" })
  await palette.getByRole("combobox").fill(">quiet")
  await expect(palette.getByRole("group")).toHaveCount(1)
  await expect(palette.getByRole("group", { name: "Actions" })).toBeVisible()
  await page.keyboard.press("Enter")
  await expect(page.locator("html")).toHaveAttribute("data-style", "quiet")

  await page.keyboard.press("Control+k")
  await palette.getByRole("combobox").fill(">zzzz")
  await expect(palette.getByText(/Nothing matches/)).toBeVisible()
})

test("/ searches the files of the computer in view and opens the pick in Files", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("palette-files")
  const id = await api.createSandbox(name)
  await api.guestReady(id)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  await expect(
    page.getByRole("region", { name: `${name}'s screen` })
  ).toBeVisible()
  // The terminal keeps Ctrl+K, so the sidebar's Search opens it here.
  await page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("button", { name: /^Search/ })
    .click()
  const palette = page.getByRole("dialog", { name: "Search Studio" })
  await expect(palette.getByText(/^\/\s*files$/)).toBeVisible()
  await palette.getByRole("combobox").fill("/coupon")
  const files = palette.getByRole("group", { name: "Files" })
  await expect(files.getByRole("option").first()).toContainText(
    "web/src/checkout/coupon.ts"
  )
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/tab=files/)
  await expect(page).toHaveURL(/file=web%2Fsrc%2Fcheckout%2Fcoupon\.ts/)

  // Away from a computer, / says what it needs.
  await page.goto(`/e/${env}/settings/general`)
  await expect(
    page.getByRole("heading", { name: "General", exact: true })
  ).toBeVisible()
  await page.keyboard.press("Control+k")
  await palette.getByRole("combobox").fill("/x")
  await expect(
    palette.getByText("Open a computer to search its files.")
  ).toBeVisible()
})
