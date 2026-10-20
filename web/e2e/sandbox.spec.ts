import { expect, sandboxName, terminal, test } from "./fixtures"

test("sandbox lifecycle: create, terminal, stop, start, delete", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("life")
  await page.goto(`/e/${env}`)
  await page.getByRole("button", { name: "More to create" }).click()
  await page
    .getByRole("menuitem", { name: /Sandbox without a teammate/ })
    .click()
  const create = page.getByRole("dialog", { name: "New sandbox" })
  await create.getByLabel("Name", { exact: true }).fill(name)
  await create.getByRole("button", { name: "Create" }).click()
  await expect(page.getByRole("heading", { name })).toBeVisible()
  // A sandbox opens on its workspace.
  await expect(page).toHaveURL(/\/sandboxes\/[a-z0-9]+\/work/)
  const id = page.url().match(/\/sandboxes\/([a-z0-9]+)\//)![1]

  const term = terminal(page)
  await term.run("echo hi")
  await expect(term.line("hi")).toHaveCount(1)

  // Stop and Start are in the details behind the header's status pill.
  const details = page.getByRole("button", { name: "Sandbox details" })
  await details.click()
  await page.getByRole("button", { name: "Stop", exact: true }).click()
  await expect(
    page
      .getByRole("main")
      .getByText("Your workspace, home directory and Docker images are kept.")
  ).toBeVisible()
  expect((await api.guest(id)).connected).toBe(false)

  // The stopped workspace offers Start too.
  await page.keyboard.press("Escape")
  await page
    .getByRole("main")
    .getByRole("button", { name: "Start", exact: true })
    .click()
  await term.run("echo back")
  await expect(term.line("back")).toHaveCount(1)

  await page.getByRole("button", { name: "Sandbox options" }).click()
  await page.getByRole("menuitem", { name: "Delete", exact: true }).click()
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
  await page.goto(`/e/${env}/sandboxes/${id}/work`)
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
  await page.keyboard.press("Enter")
  await expect(term.line("pasted")).toHaveCount(1)
})

test("terminal: a pasted screenshot goes into the sandbox, and its path into the terminal", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("paste"))
  await page.goto(`/e/${env}/sandboxes/${id}/work`)
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
