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
  await expect(
    screen.getByRole("tablist", { name: `${name}'s screen` })
  ).toBeVisible()
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

// The strip over the terminal lists its sessions by what runs in them, opens a new shell,
// switches, renames and closes them, and the URL names the one in view.
test("the terminal's tabs open, switch, rename and close sessions", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("terms")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  const sessions = screen.getByRole("tablist", { name: "Terminal sessions" })
  await terminal(page).run("echo first")
  // A shell is named for its directory, the fake guest's /workspace.
  await expect(
    sessions.getByRole("tab", { name: "workspace" })
  ).toHaveAttribute("aria-selected", "true")
  await sessions.getByRole("button", { name: "New shell" }).click()
  await expect.poll(() => termOf(page)).toBe("2")
  await terminal(page).run("echo second")
  const second = sessions.getByRole("tab", { name: "workspace 2" })
  await expect(second).toHaveAttribute("aria-selected", "true")
  await expect(
    screen.getByRole("tab", { name: /^Terminal/ }).getByLabel("2 sessions")
  ).toBeVisible()

  await sessions.getByRole("tab", { name: "workspace", exact: true }).click()
  await expect.poll(() => termOf(page)).toBe("main")
  await expect(terminal(page).line("first")).toHaveCount(1)
  await page.keyboard.press("Alt+Shift+BracketRight")
  await expect.poll(() => termOf(page)).toBe("2")
  // The session switched to has the keys, so they go on switching.
  await page.keyboard.press("Alt+Shift+BracketRight")
  await expect.poll(() => termOf(page)).toBe("main")
  await page.keyboard.press("Alt+Shift+BracketLeft")
  await expect.poll(() => termOf(page)).toBe("2")
  await expect(page.locator(".xterm-helper-textarea:focus")).toHaveCount(1)

  // A program in the foreground names the tab, until the user names it.
  await api.setTerminal(id, "2", {
    command: "npm",
    foreground: ["npm run dev"],
  })
  const dev = sessions.getByRole("tab", { name: "npm run dev" })
  await expect(dev).toBeVisible({ timeout: 10_000 })
  await expect(dev.getByLabel("a program runs")).toBeVisible()
  await dev.dblclick()
  const field = sessions.getByRole("textbox", { name: "Name of npm run dev" })
  await field.fill("dev server")
  await field.press("Enter")
  await expect(sessions.getByRole("tab", { name: "dev server" })).toBeVisible()
  expect(termOf(page)).toBe("2")

  // Closing a session a program runs in asks first.
  await sessions.getByRole("tab", { name: "dev server" }).hover()
  await sessions.getByRole("button", { name: "Close dev server" }).click()
  const ask = page.getByRole("alertdialog", { name: "Close dev server?" })
  await expect(ask).toContainText("npm run dev runs in it")
  await ask.getByRole("button", { name: "Close" }).click()
  await expect(sessions.getByRole("tab")).toHaveCount(1)
  await expect.poll(() => termOf(page)).toBe("main")
  await expect(screen.getByLabel("2 sessions")).toHaveCount(0)
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

// The fake runtime boots with room for twice its size, as Studio asks msb to.
test("a running computer takes what fits now and the rest on a restart", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("resize")
  const id = await api.createSandbox(name)
  const screen = await screenOf(page, env, id, name)
  const more = screen.getByRole("button", { name: `More for ${name}` })
  await more.click()
  await page.getByRole("menuitem", { name: /^Resources/ }).click()
  const dialog = page.getByRole("dialog", { name: `Resources of ${name}` })
  await dialog.getByLabel("CPUs").fill("3")
  await dialog.getByLabel("Memory (MiB)").fill("12288")
  await expect(dialog.getByText("Has 2 now. 3 CPUs applies now.")).toBeVisible()
  await expect(
    dialog.getByText("Has 4 GiB now. 12 GiB needs a restart", { exact: false })
  ).toBeVisible()
  await expect(
    dialog.getByRole("button", { name: "Save and restart" })
  ).toBeVisible()
  await dialog.getByRole("button", { name: "Save", exact: true }).click()
  await expect(
    page.getByText(
      "The CPUs changed live. The memory applies on the next start."
    )
  ).toBeVisible()

  await more.click()
  await expect(
    page.getByRole("menuitem", {
      name: /^Resources\s*3 CPU · 4 GiB · 12 GiB after restart$/,
    })
  ).toBeVisible()
  await page.getByRole("menuitem", { name: /^Resources/ }).click()
  await expect(
    dialog.getByText("Has 4 GiB now, 12 GiB after a restart.")
  ).toBeVisible()
  await dialog.getByRole("button", { name: "Save and restart" }).click()
  await expect(page.getByText("Restarted with 3 CPU · 12 GiB.")).toBeVisible()
  await more.click()
  await expect(
    page.getByRole("menuitem", { name: /^Resources\s*3 CPU · 12 GiB$/ })
  ).toBeVisible()
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

// A sandbox made by an older Studio runs that release's base image: its menu says so and
// rebases it onto the current one, and Home lists it until the notice is dismissed.
test("a computer on an older base says so and rebases onto the current one", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("old-base")
  const id = await api.createSandbox(name)
  // Once it is up: the creation's last event would carry the base it was made with.
  const screen = await screenOf(page, env, id, name)
  await api.json("PUT", `/api/e2e/sandboxes/${id}/base`, {
    image: "ghcr.io/lukaskoebe/sandbox-studio-base:v0.7.0",
    version: "v0.7.0",
  })
  await page.reload()
  const more = screen.getByRole("button", { name: `More for ${name}` })
  await expect(more).toHaveAttribute(
    "title",
    `More for ${name}: built on an older base`
  )
  await more.click()
  await expect(page.getByText("Built on an older base")).toBeVisible()
  await expect(
    page.getByText("From v0.7.0. Rebase for the newer harness versions", {
      exact: false,
    })
  ).toBeVisible()
  await page.getByRole("button", { name: "Rebase…" }).click()
  const dialog = page.getByRole("dialog", { name: `Rebase ${name}` })
  await expect(
    dialog.getByRole("radio", { name: "The current base image" })
  ).toBeChecked()
  await expect(dialog.getByText("Keeps its resources: 2 CPU")).toBeVisible()
  const lost = dialog.getByRole("region", { name: "What the rebase changes" })
  await expect(lost).toContainText("Docker images and volumes")
  await expect(dialog.getByRole("button", { name: "Rebase" })).toBeEnabled()
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()

  await page.goto(`/e/${env}`)
  const notice = page.getByRole("region", { name: "Base image" })
  await expect(notice.getByRole("link", { name })).toHaveAttribute(
    "href",
    `/e/${env}/sandboxes/${id}`
  )
  await notice.getByRole("button", { name: "Dismiss" }).click()
  await expect(notice).toBeHidden()
  await page.reload()
  await expect(page.getByRole("heading").first()).toBeVisible()
  await expect(notice).toBeHidden()
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
  await expect(
    screen.getByRole("tablist", { name: `${name}'s screen` })
  ).toBeVisible()
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
      .getByRole("tablist", { name: "Terminal sessions" })
      .getByRole("tab", { selected: true })
  ).toBeVisible()
  await expect
    .poll(async () => (await api.guest(id)).sessions.map((s) => s.name))
    .toContain("logs")
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

  // After every task, it waits for an agent to change files.
  await section.getByRole("combobox", { name: "Take checkpoints" }).click()
  await page.getByRole("option", { name: "After every task" }).click()
  await expect(
    section.getByText(/when an agent finishes a task that changed files/)
  ).toBeVisible()
  await expect
    .poll(() =>
      api.json(
        "GET",
        `/api/environments/${env}/sandboxes/${id}/checkpoint-schedule`
      )
    )
    .toMatchObject({ everyHours: 0, afterTasks: true })
})
