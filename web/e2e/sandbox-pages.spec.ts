import type { Page } from "@playwright/test"
import { expect, sandboxName, terminal, test } from "./fixtures"

/** The session the URL names; the router quotes a name that reads as a number. */
function termOf(page: Page) {
  const term = new URL(page.url()).searchParams.get("term") ?? ""
  return term.replace(/^"(.*)"$/, "$1")
}

/** A computer without a teammate, on its screen once it is up. */
async function screenOf(page: Page, env: string, id: string, name: string) {
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  // Creation runs in the background; the tabs show once the computer is up.
  await expect(screen.getByRole("tablist")).toBeVisible()
  return screen
}

test("a computer opens on its screen, with its facts in its menu", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("pages")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  await expect(page).toHaveURL(new RegExp(`/sandboxes/${id}$`))
  await expect(screen.getByRole("tab", { name: "Terminal" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  const term = terminal(page)
  await term.run("echo workspace")
  await expect(term.line("workspace")).toHaveCount(1)

  await screen.getByRole("button", { name: `More for ${name}` }).click()
  const menu = page.getByRole("menu")
  await expect(menu).toContainText(/\d+ CPU · [\d.]+ GiB/)
  await expect(menu.getByText("Memory", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
})

// The chip on the terminal lists its sessions, opens a new shell, and the URL names the one in
// view.
test("the terminal's chip opens a new shell and switches between sessions", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("terms")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  await terminal(page).run("echo first")
  await screen.getByRole("button", { name: "Terminal session main" }).click()
  await page.getByRole("menuitem", { name: "New shell" }).click()
  await expect.poll(() => termOf(page)).toBe("2")
  await terminal(page).run("echo second")

  const chip = screen.getByRole("button", { name: "Terminal session 2" })
  await chip.click()
  await expect(page.getByRole("menuitem", { name: /^main/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /^2/ })).toHaveAttribute(
    "aria-current",
    "true"
  )
  await page.getByRole("menuitem", { name: /^main/ }).click()
  await expect.poll(() => termOf(page)).toBe("main")
  await expect(
    screen.getByRole("button", { name: "Terminal session main" })
  ).toBeVisible()
  await expect(terminal(page).line("first")).toHaveCount(1)
})

test("Connections opens from the computer's menu as a sheet", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("conns")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  await screen.getByRole("button", { name: `More for ${name}` }).click()
  await page.getByRole("menuitem", { name: "Connections" }).click()
  const sheet = page.getByRole("dialog", { name: "Connections" })
  await expect(sheet).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(sheet).toBeHidden()
})

test("the computer's menu lists its actions", async ({ page, api, env }) => {
  const name = sandboxName("menu")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  await screen.getByRole("button", { name: `More for ${name}` }).click()
  for (const action of [
    "Checkpoint now",
    "Fork",
    "Connections",
    "Rebase",
    "Export",
    "Suspend",
    `Delete ${name}`,
  ])
    await expect(
      page.getByRole("menuitem", { name: action, exact: true })
    ).toBeVisible()
  await expect(
    page.getByRole("menuitem", { name: /^Checkpoints/ })
  ).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /^Resources/ })).toBeVisible()
  await page.keyboard.press("Escape")
})

test("the computer's menu opens fork and rebase, and links the export", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("menu-dialogs")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  const more = screen.getByRole("button", { name: `More for ${name}` })
  // The fake runtime doesn't copy disks, so a fork or rebase is only checked to open its dialog.
  for (const action of ["Fork", "Rebase"]) {
    await more.click()
    await page.getByRole("menuitem", { name: action, exact: true }).click()
    const dialog = page.getByRole("dialog", { name: `${action} ${name}` })
    await expect(dialog).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(dialog).toBeHidden()
  }

  await more.click()
  const exported = page.getByRole("menuitem", { name: "Export", exact: true })
  await expect(exported).toHaveAttribute(
    "href",
    `/api/environments/${env}/sandboxes/${id}/export`
  )
  await expect(exported).toHaveAttribute("download", `${name}.studio-sandbox`)
  await page.keyboard.press("Escape")
})

// Suspend and Resume are in the menu; the bezel's power button stops the computer.
test("the screen suspends, resumes and stops a computer", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("menu-state")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  const choose = async (action: string) => {
    await screen.getByRole("button", { name: `More for ${name}` }).click()
    await page.getByRole("menuitem", { name: action, exact: true }).click()
  }

  await choose("Suspend")
  await expect(screen.getByText(`${name} is suspended`)).toBeVisible()
  await expect(
    screen.getByRole("button", { name: `Resume ${name}` })
  ).toBeVisible()
  await choose("Resume")
  await expect(screen.getByRole("tablist")).toBeVisible()
  await screen
    .getByRole("button", { name: `Stop ${name}; its disk is kept` })
    .click()
  await expect(screen.getByText(`${name} is off`)).toBeVisible()
  await expect(
    screen.getByRole("button", { name: `Start ${name}` })
  ).toBeVisible()
  expect((await api.guest(id)).connected).toBe(false)
})

test("an old workspace link opens the screen on the terminal it named", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("old")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}/work?terminal=logs`)
  await expect(page).toHaveURL(new RegExp(`/sandboxes/${id}\\?`))
  expect(new URL(page.url()).searchParams.get("tab")).toBe("terminal")
  expect(termOf(page)).toBe("logs")
  await expect(
    page
      .getByRole("region", { name: `${name}'s screen` })
      .getByRole("button", { name: "Terminal session logs" })
  ).toBeVisible()
})

test("the screen fits a 390px screen", async ({ page, api, env }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const name = sandboxName("narrow")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  const fits = () =>
    page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth
    )
  await expect(screen.getByRole("tab", { name: "Changes" })).toBeVisible()
  expect(await fits()).toBe(true)
  await screen.getByRole("tab", { name: "Changes" }).click()
  await expect(screen.getByRole("tab", { name: "Changes" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  expect(await fits()).toBe(true)
})

test("the computer's menu shows CPU and memory use while it runs", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("usage")
  const id = await api.createSandbox(name)
  const use = await api.json<{
    cpuPercent: number
    cpus: number
    memoryBytes: number
    memoryTotalBytes: number
  }>("GET", `/api/environments/${env}/sandboxes/${id}/usage`)
  // The e2e runtime reports half a vCPU and 768 MiB for every running VM.
  expect(use!.cpuPercent).toBeCloseTo(50 / use!.cpus)
  expect(use!.memoryBytes).toBe(768 << 20)

  const screen = await screenOf(page, env, id, name)
  const more = screen.getByRole("button", { name: `More for ${name}` })
  await more.click()
  const menu = page.getByRole("menu")
  await expect(
    menu.getByText(`${Math.round(use!.cpuPercent)}%`, { exact: true })
  ).toBeVisible()
  await expect(menu.getByText(/^0\.8 \/ [\d.]+ GiB$/)).toBeVisible()
  await page.keyboard.press("Escape")

  await screen
    .getByRole("button", { name: `Stop ${name}; its disk is kept` })
    .click()
  await expect(screen.getByText(`${name} is off`)).toBeVisible()
  await more.click()
  await expect(menu.getByText("Memory", { exact: true })).toHaveCount(0)
  await page.keyboard.press("Escape")
  const stopped = await api.request.get(
    `/api/environments/${env}/sandboxes/${id}/usage`
  )
  expect(stopped.status()).toBe(409)
})

// A schedule takes its first checkpoint as soon as it is turned on, and marks it as its own.
test("a checkpoint schedule takes a checkpoint", async ({ page, api, env }) => {
  const name = sandboxName("sched")
  const id = await api.createSandbox(name)
  const openHistory = async () => {
    await page
      .getByRole("region", { name: `${name}'s screen` })
      .getByRole("button", { name: `More for ${name}` })
      .click()
    await page.getByRole("menuitem", { name: /^Checkpoints/ }).click()
  }
  await screenOf(page, env, id, name)
  await openHistory()
  const section = page.getByRole("region", { name: "Checkpoints" })
  await section.getByRole("combobox", { name: "Take checkpoints" }).click()
  await page.getByRole("option", { name: "Every 4 hours" }).click()
  await expect(section.getByText(/keeps the newest 3/)).toBeVisible()
  const auto = section.getByRole("listitem").filter({ hasText: /^Auto / })
  await expect(auto).toHaveCount(1)
  await expect(auto.getByText("auto", { exact: true })).toBeVisible()

  // After a reload the schedule is still on.
  await page.reload()
  await openHistory()
  await expect(
    section.getByRole("combobox", { name: "Take checkpoints" })
  ).toContainText("Every 4 hours")
})
