import { addTeammate, expect, sandboxName, test } from "./fixtures"

test("a task is added on the Tasks tab and marked done, and the server keeps it", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Lister")
  const id = await addTeammate(api, env, name)
  const title = `Write the release notes ${name}`

  await page.goto(`/e/${env}/teammates/${id}/tasks`)
  await page.getByRole("button", { name: "Add task", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Title").fill(title)
  await dialog.getByLabel("Details").fill("Cover the tasks and the check-ins.")
  await dialog.getByRole("button", { name: "Add task", exact: true }).click()
  await expect(dialog).toBeHidden()

  const row = page.getByRole("listitem").filter({ hasText: title })
  await expect(row).toBeVisible()
  await expect(row).toContainText("Open")
  await row.getByRole("checkbox").click()
  await expect(row).toContainText("Done")

  const tasks = await api.json<{ title: string; status: string }[]>(
    "GET",
    `/api/environments/${env}/teammates/${id}/tasks`
  )
  expect(tasks?.find((t) => t.title === title)?.status).toBe("done")
})

test("check-ins start off, then save their schedule on the settings page", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Checker")
  const id = await addTeammate(api, env, name)
  const teammate = `/api/environments/${env}/teammates/${id}`
  const schedule = async () =>
    (await api.json<{ heartbeat?: { everyHours: number } }>("GET", teammate))
      ?.heartbeat

  await page.goto(`/e/${env}/teammates/${id}/settings`)
  const toggle = page.getByRole("switch", { name: "Check in on open tasks" })
  await expect(toggle).not.toBeChecked()
  expect(await schedule()).toBeUndefined()

  await toggle.click()
  await expect(toggle).toBeChecked()
  await expect(page.getByLabel("Every (hours)")).toHaveValue("4")
  await page.getByLabel("Every (hours)").fill("2")
  await page.getByRole("button", { name: "Save check-ins" }).click()
  await expect.poll(async () => (await schedule())?.everyHours).toBe(2)
  await expect(
    page.getByText("Every 2 hours, 09:00 to 18:00 local time")
  ).toBeVisible()

  await toggle.click()
  await expect(toggle).not.toBeChecked()
  await expect.poll(schedule).toBeUndefined()
})
