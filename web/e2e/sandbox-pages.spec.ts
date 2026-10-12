import { expect, sandboxName, terminal, test } from "./fixtures"

test("the dashboard shows the sandbox's sections, and the workspace opens its terminal", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("pages")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible()
  await expect(page.getByRole("region", { name: "Overview" })).toBeVisible()
  for (const section of [
    "Ports",
    "Recent connections",
    "Checkpoints",
    "Terminals",
  ])
    await expect(page.getByRole("region", { name: section })).toBeVisible()

  await page.getByRole("link", { name: "Workspace", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/sandboxes/${id}/work$`))
  const term = terminal(page)
  await term.run("echo workspace")
  await expect(term.line("workspace")).toHaveCount(1)
})

test("a side panel opens and closes from the workspace toolbar", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("panel"))
  await page.goto(`/e/${env}/sandboxes/${id}/work`)
  const panel = page.getByRole("complementary", { name: "Connections" })
  await expect(panel).toBeHidden()
  await page.getByRole("button", { name: "Connections", exact: true }).click()
  await expect(panel).toBeVisible()
  await expect(page).toHaveURL(/panel=connections/)
  await panel.getByRole("button", { name: "Close panel" }).click()
  await expect(panel).toBeHidden()
  await expect(page).not.toHaveURL(/panel=/)
})

test("the options menu lists the sandbox's actions", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("menu"))
  await page.goto(`/e/${env}/sandboxes/${id}`)
  // Creation runs in the background; a sandbox that is still creating offers only Delete.
  await expect(page.getByTitle("microsandbox status: running")).toHaveText(
    "Running"
  )
  await page.getByRole("button", { name: "Sandbox options" }).click()
  for (const action of [
    "Fork",
    "Rebase",
    "Export",
    "Suspend",
    "Stop",
    "Delete",
  ])
    await expect(
      page.getByRole("menuitem", { name: action, exact: true })
    ).toBeVisible()
  await page.keyboard.press("Escape")
})

test("an old terminal link opens the workspace with the same parameters", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("old"))
  await page.goto(`/e/${env}/sandboxes/${id}?terminal=main`)
  await expect(page).toHaveURL(
    new RegExp(`/sandboxes/${id}/work\\?terminal=main$`)
  )
})

test("the dashboard and the workspace fit a 390px screen, and a panel covers it", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const id = await api.createSandbox(sandboxName("narrow"))
  const fits = () =>
    page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth
    )

  await page.goto(`/e/${env}/sandboxes/${id}`)
  await expect(page.getByRole("region", { name: "Overview" })).toBeVisible()
  expect(await fits()).toBe(true)

  await page.goto(`/e/${env}/sandboxes/${id}/work`)
  await expect(
    page.getByRole("button", { name: "Files", exact: true })
  ).toBeVisible()
  expect(await fits()).toBe(true)

  await page.getByRole("button", { name: "Files", exact: true }).click()
  const panel = page.getByRole("complementary", { name: "Files and changes" })
  await expect(panel).toBeVisible()
  const box = await panel.boundingBox()
  expect(box?.width).toBe(390)
  await panel.getByRole("button", { name: "Close panel" }).click()
  await expect(panel).toBeHidden()
})
