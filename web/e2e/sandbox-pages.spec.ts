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

test("the options menu opens fork and rebase, and links the export", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("menu-dialogs")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  await expect(page.getByTitle("microsandbox status: running")).toHaveText(
    "Running"
  )
  // The fake runtime doesn't copy disks, so a fork or rebase is only checked to open its dialog.
  for (const action of ["Fork", "Rebase"]) {
    await page.getByRole("button", { name: "Sandbox options" }).click()
    await page.getByRole("menuitem", { name: action, exact: true }).click()
    const dialog = page.getByRole("dialog", { name: `${action} ${name}` })
    await expect(dialog).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(dialog).toBeHidden()
  }

  await page.getByRole("button", { name: "Sandbox options" }).click()
  const exported = page.getByRole("menuitem", { name: "Export", exact: true })
  await expect(exported).toHaveAttribute(
    "href",
    `/api/environments/${env}/sandboxes/${id}/export`
  )
  await expect(exported).toHaveAttribute("download", `${name}.studio-sandbox`)
  await page.keyboard.press("Escape")
})

test("the options menu suspends, resumes and stops a sandbox", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("menu-state"))
  await page.goto(`/e/${env}/sandboxes/${id}`)
  await expect(page.getByTitle("microsandbox status: running")).toHaveText(
    "Running"
  )
  const choose = async (action: string) => {
    await page.getByRole("button", { name: "Sandbox options" }).click()
    await page.getByRole("menuitem", { name: action, exact: true }).click()
  }

  await choose("Suspend")
  await expect(page.getByTitle("microsandbox status: suspended")).toHaveText(
    "Suspended"
  )
  await choose("Resume")
  await expect(page.getByTitle("microsandbox status: running")).toHaveText(
    "Running"
  )
  await choose("Stop")
  await expect(page.getByTitle("microsandbox status: stopped")).toHaveText(
    "Stopped"
  )
  expect((await api.guest(id)).connected).toBe(false)
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

test("the workspace status line shows CPU and memory use while the sandbox runs", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("usage"))
  const use = await api.json<{
    cpuPercent: number
    cpus: number
    memoryBytes: number
    memoryTotalBytes: number
  }>("GET", `/api/environments/${env}/sandboxes/${id}/usage`)
  // The e2e runtime reports half a vCPU and 768 MiB for every running VM.
  expect(use!.cpuPercent).toBeCloseTo(50 / use!.cpus)
  expect(use!.memoryBytes).toBe(768 << 20)

  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto(`/e/${env}/sandboxes/${id}/work`)
  const usage = page.getByText(/^cpu \d+% · mem [\d.]+\/[\d.]+ (GiB|MiB)$/)
  await expect(usage).toHaveText(
    new RegExp(`^cpu ${Math.round(use!.cpuPercent)}% · mem `)
  )

  await page.getByRole("button", { name: "Sandbox options" }).click()
  await page.getByRole("menuitem", { name: "Stop", exact: true }).click()
  await expect(usage).toBeHidden()
  const stopped = await api.request.get(
    `/api/environments/${env}/sandboxes/${id}/usage`
  )
  expect(stopped.status()).toBe(409)
})
