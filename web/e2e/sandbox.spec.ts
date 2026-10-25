import { expect, sandboxName, terminal, test } from "./fixtures"

test("computer lifecycle: create, terminal, stop, start, delete", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("life")
  await page.goto(`/e/${env}`)
  await page.getByRole("button", { name: "New computer" }).click()
  await page.getByRole("menuitem", { name: /^New computer/ }).click()
  const create = page.getByRole("dialog", { name: "New computer" })
  await create.getByLabel("Name", { exact: true }).fill(name)
  await create.getByRole("button", { name: "Create" }).click()
  // A computer without a teammate opens on its screen.
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await expect(screen).toBeVisible()
  await expect(page).toHaveURL(/\/sandboxes\/[a-z0-9]+$/)
  const id = page.url().match(/\/sandboxes\/([a-z0-9]+)$/)![1]

  const term = terminal(page)
  await term.run("echo hi")
  await expect(term.line("hi")).toHaveCount(1)

  // The bezel's power button stops it, and the dark screen offers Start.
  await screen
    .getByRole("button", { name: `Stop ${name}; its disk is kept` })
    .click()
  await expect(
    screen.getByText(
      "Its workspace, home directory and Docker images are kept."
    )
  ).toBeVisible()
  expect((await api.guest(id)).connected).toBe(false)
  await expect(
    screen.getByRole("button", { name: `Start ${name}` })
  ).toBeVisible()
  await screen.getByRole("button", { name: "Start", exact: true }).click()
  await term.run("echo back")
  await expect(term.line("back")).toHaveCount(1)

  await screen.getByRole("button", { name: `More for ${name}` }).click()
  await page.getByRole("menuitem", { name: `Delete ${name}` }).click()
  const confirm = page.getByRole("alertdialog", { name: `Delete ${name}?` })
  await confirm.getByRole("button", { name: "Delete" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}$`))
  const left = await api.json<{ id: string }[]>(
    "GET",
    `/api/environments/${env}/sandboxes`
  )
  expect(left!.map((s) => s.id)).not.toContain(id)
})

test("terminal: a tmux copy reaches the clipboard, and right-click opens Copy and Paste", async ({
  page,
  context,
  api,
  env,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  const id = await api.createSandbox(sandboxName("copy"))
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const term = terminal(page)
  // tmux copies a mouse selection with OSC 52 and an empty clipboard name.
  await term.run(`printf '\\e]52;;Y29waWVkIGZyb20gdG11eA==\\a'`)
  await expect(
    page.getByRole("status").filter({ hasText: "Copied" })
  ).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    "copied from tmux"
  )
  // A program in the sandbox can't read the clipboard: the answer comes back empty.
  await page.evaluate(() => navigator.clipboard.writeText("host secret"))
  await term.run(`printf '\\e]52;c;?\\a'`)
  await expect(term.rows).toContainText("]52;c;")
  await expect(term.rows).not.toContainText("host secret")
  await page.keyboard.press("Control+C")
  await page
    .locator(".xterm")
    .filter({ visible: true })
    .click({ button: "right" })
  await expect(page.getByRole("menuitem", { name: /Copy/ })).toBeDisabled()
  await page.evaluate(() => navigator.clipboard.writeText("echo pasted"))
  await page.getByRole("menuitem", { name: /Paste/ }).click()
  // The paste reads the clipboard first: Enter goes after it, or it runs an empty line.
  await expect(term.rows).toContainText("$ echo pasted")
  await page.keyboard.press("Enter")
  await expect(term.line("pasted")).toHaveCount(1)
})

test("terminal: follows the light and dark theme, unless it is kept dark", async ({
  page,
  api,
  env,
}) => {
  const light = "rgb(251, 252, 252)"
  const dark = "rgb(21, 23, 27)"
  const id = await api.createSandbox(sandboxName("theme"))
  await page.emulateMedia({ colorScheme: "light" })
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const term = terminal(page)
  await expect(term.rows).toContainText("$")
  const viewport = page
    .locator(".xterm-scrollable-element")
    .filter({ visible: true })
  await expect(viewport).toHaveCSS("background-color", light)
  await page.emulateMedia({ colorScheme: "dark" })
  await expect(viewport).toHaveCSS("background-color", dark)
  await page.emulateMedia({ colorScheme: "light" })
  await expect(viewport).toHaveCSS("background-color", light)
  await page
    .locator(".xterm")
    .filter({ visible: true })
    .click({ button: "right" })
  await page.getByRole("menuitemcheckbox", { name: "Always dark" }).click()
  await expect(viewport).toHaveCSS("background-color", dark)
  // The choice is kept for every terminal, after a reload too.
  await page.reload()
  await expect(term.rows).toContainText("$")
  await expect(viewport).toHaveCSS("background-color", dark)
})

test("terminal: a pasted screenshot goes into the sandbox, and its path into the terminal", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("paste"))
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const term = terminal(page)
  await expect(term.rows).toContainText("$")
  // A screenshot on the clipboard pastes as a file named image.png, with no text.
  await page
    .locator(".xterm textarea")
    .filter({ visible: true })
    .evaluate((ta) => {
      const data = new DataTransfer()
      data.items.add(
        new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "image.png", {
          type: "image/png",
        })
      )
      ta.dispatchEvent(
        new ClipboardEvent("paste", {
          clipboardData: data,
          bubbles: true,
          cancelable: true,
        })
      )
    })
  await expect(term.rows).toContainText(
    /\/tmp\/studio-pastes\/paste-\d{14}-\w{4}\.png/
  )
})
