import { expect, sandboxName, terminal, test } from "./fixtures"

test("sandbox lifecycle: create, terminal, stop, start, delete", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("life")
  await page.goto(`/e/${env}`)
  await page.getByRole("button", { name: "New sandbox" }).first().click()
  const create = page.getByRole("dialog", { name: "New sandbox" })
  await create.getByLabel("Name", { exact: true }).fill(name)
  await create.getByRole("button", { name: "Create" }).click()
  await expect(page.getByRole("heading", { name })).toBeVisible()
  const id = page.url().split("/").at(-1)!

  await page.getByRole("link", { name: "Workspace", exact: true }).click()
  const term = terminal(page)
  await term.run("echo hi")
  await expect(term.line("hi")).toHaveCount(1)

  await page.getByRole("link", { name: "Dashboard", exact: true }).click()
  await page.getByRole("button", { name: "Stop" }).click()
  await expect(
    page.getByText("Your workspace, home directory and Docker images are kept.")
  ).toBeVisible()
  expect((await api.guest(id)).connected).toBe(false)

  await page.getByRole("button", { name: "Start" }).click()
  await page.getByRole("link", { name: "Workspace", exact: true }).click()
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
