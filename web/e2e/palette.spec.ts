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

  // A page answers to what it holds, not only its name. The computer's terminal takes the
  // focus once it connects, and Ctrl+K in a terminal is the shell's, so Search opens it here.
  await search.click()
  await expect(palette).toBeVisible()
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

test("the palette opens a teammate's browser, a computer's tabs, a settings row and Home", async ({
  page,
  api,
  env,
}) => {
  const name = `Places ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const computer = sandboxName("places")
  const sb = await api.createSandbox(computer, id)
  await api.guestReady(sb)
  await page.goto(`/e/${env}/settings/general`)
  await expect(
    page.getByRole("heading", { name: "General", exact: true })
  ).toBeVisible()
  const palette = page.getByRole("dialog", { name: "Search Studio" })
  const input = palette.getByRole("combobox", { name: "Search Studio" })
  const teammates = palette.getByRole("group", { name: "Teammates" })

  // Its name lists the teammate and its places, not every row of its settings.
  await page.keyboard.press("Control+k")
  await input.fill(name)
  const options = teammates.getByRole("option")
  await expect(options).toHaveCount(5)
  await expect(options.first()).toContainText(`${name}teammate`)
  for (const place of ["settings", "tasks", "memory", "browser"])
    await expect(options.filter({ hasText: `${name}'s ${place}` })).toHaveCount(
      1
    )
  // A title that names its place doesn't repeat the place beside it.
  await expect(options.filter({ hasText: `${name}'s tasks` })).toHaveText(
    `${name}'s tasks`
  )
  // Its computer, but none of the computer's tabs.
  const computers = palette
    .getByRole("group", { name: "Computers" })
    .getByRole("option")
  await expect(computers).toHaveCount(1)
  await expect(computers).toContainText(computer)

  await input.fill(`${name} browser`)
  await expect(teammates.getByRole("option").first()).toContainText(
    `${name}'s browser`
  )
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(
    new RegExp(`/e/${env}/teammates/${id}\\?tab=browser$`)
  )

  // A computer's tab, by its name and the computer's.
  await page.keyboard.press("Control+k")
  await input.fill(`${computer} changes`)
  const tab = palette
    .getByRole("group", { name: "Computers" })
    .getByRole("option")
    .first()
  await expect(tab).toContainText("Changes")
  await expect(tab).toContainText(computer)
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(new RegExp(`computer=${sb}`))
  await expect(page).toHaveURL(/tab=changes/)

  // A row of the teammate's settings, by what it holds.
  await page.keyboard.press("Control+k")
  await input.fill(`${name} stealth`)
  await expect(teammates.getByRole("option").first()).toHaveText(
    new RegExp(`^Browser${name}'s settings`)
  )
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/profile=settings/)
  await expect(page).toHaveURL(/open=browser/)
  await expect(
    page.getByRole("button", { name: /Browser.*Stealth mode/ })
  ).toHaveAttribute("aria-expanded", "true")

  await page.keyboard.press("Escape")
  await page.keyboard.press("Control+k")
  await input.fill("home")
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(new RegExp(`/e/${env}$`))
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

test("the palette forgives a typo and finds letters in order", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/general`)
  await expect(
    page.getByRole("heading", { name: "General", exact: true })
  ).toBeVisible()
  await page.keyboard.press("Control+k")
  const palette = page.getByRole("dialog", { name: "Search Studio" })
  const input = palette.getByRole("combobox")
  const environment = palette.getByRole("group", { name: "Environment" })
  // Two letters swapped.
  await input.fill("netwrok")
  await expect(environment.getByRole("option").first()).toContainText("Network")
  await input.fill("secerts")
  await expect(environment.getByRole("option").first()).toContainText("Secrets")
  // Letters left out.
  await input.fill("actvty")
  await expect(environment.getByRole("option").first()).toContainText(
    "Activity"
  )
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(new RegExp(`/e/${env}/activity$`))
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

  // Its letters in order find it too, with the letters marked.
  await page
    .locator('[data-sidebar="sidebar"]')
    .getByRole("button", { name: /^Search/ })
    .click()
  await palette.getByRole("combobox").fill("/chkcoup")
  const fuzzy = files.getByRole("option").first()
  await expect(fuzzy).toContainText("web/src/checkout/coupon.ts")
  await expect(fuzzy.locator("mark")).toHaveText([..."chkcoup"])
  await page.keyboard.press("Escape")

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
