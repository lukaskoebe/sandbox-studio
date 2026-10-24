import { addTeammate, expect, sandboxName, test } from "./fixtures"

test("a task is added in the profile's tasks and marked done, and the server keeps it", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Lister")
  const id = await addTeammate(api, env, name)
  const title = `Write the release notes ${name}`

  await page.goto(`/e/${env}/teammates/${id}?profile=tasks`)
  await page.getByRole("button", { name: "Add task", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: /^(New|Edit) task$/ })
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

  await page.goto(`/e/${env}/teammates/${id}?profile=settings&open=checkins`)
  const toggle = page.getByRole("switch", { name: "Check in on open tasks" })
  await expect(toggle).not.toBeChecked()
  expect(await schedule()).toBeUndefined()

  await toggle.click()
  await expect(toggle).toBeChecked()
  await expect(page.getByLabel("Every (hours)")).toHaveValue("4")
  await page.getByLabel("Every (hours)").fill("2")
  // The page's one save bar saves the times.
  const bar = page.getByRole("region", { name: "Unsaved changes" })
  await bar.getByRole("button", { name: "Save", exact: true }).click()
  await expect.poll(async () => (await schedule())?.everyHours).toBe(2)
  await expect(bar).toBeHidden()
  // The card's summary says the schedule.
  await expect(
    page.getByRole("button", {
      name: "Check-ins Every 2 hours, 09:00 to 18:00 local time",
    })
  ).toBeVisible()

  await toggle.click()
  await expect(toggle).not.toBeChecked()
  await expect.poll(schedule).toBeUndefined()
  await expect(
    page.getByRole("button", { name: "Check-ins Off" })
  ).toBeVisible()
})

test("a task gets a schedule in its dialog, shows it with its next run, and Run now says why it can't start", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("Planner")
  const id = await addTeammate(api, env, name)
  const title = `CI triage ${name}`
  type Listed = {
    id: string
    title: string
    schedule?: { repeat: string; days?: string[]; everyHours?: number }
    nextRunAt?: string
  }
  const listed = async () =>
    (
      await api.json<Listed[]>(
        "GET",
        `/api/environments/${env}/teammates/${id}/tasks`
      )
    )?.find((t) => t.title === title)

  await page.goto(`/e/${env}/teammates/${id}?profile=tasks`)
  await page.getByRole("button", { name: "Add task", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: /^(New|Edit) task$/ })
  await dialog.getByLabel("Title").fill(title)
  await dialog.getByLabel("Schedule").click()
  await page.getByRole("option", { name: "Days" }).click()
  await expect(dialog.getByRole("button", { name: "Mon" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  await dialog.getByLabel("Time").fill("09:30")
  await expect(
    dialog.getByText("Every weekday at 09:30, in this Studio's local time.")
  ).toBeVisible()
  await dialog.getByRole("button", { name: "Add task", exact: true }).click()
  await expect(dialog).toBeHidden()

  const row = page.getByRole("listitem").filter({ hasText: title })
  await expect(row).toContainText("⟳ Every weekday at 09:30 · next ")
  const saved = await listed()
  expect(saved?.schedule?.days).toEqual(["mon", "tue", "wed", "thu", "fri"])
  expect(saved?.nextRunAt).toBeTruthy()
  expect(new Date(saved!.nextRunAt!).getMinutes()).toBe(30)

  // The teammate has no sandbox, so Run now is refused with why.
  await row.getByRole("button", { name: "Run now" }).click()
  await expect(page.getByText("The run did not start")).toBeVisible()
  await expect(page.getByText(/has no sandbox to work in/)).toBeVisible()

  // Every few hours instead, then no schedule at all.
  await row.getByRole("button", { name: title }).click()
  await dialog.getByLabel("Schedule").click()
  await page.getByRole("option", { name: "Every N hours" }).click()
  await expect(
    dialog.getByText("Every 4 hours, 08:00–20:00, in this Studio's local time.")
  ).toBeVisible()
  await dialog.getByRole("button", { name: "Save" }).click()
  await expect(dialog).toBeHidden()
  await expect(row).toContainText("⟳ Every 4 hours, 08:00–20:00")
  await expect.poll(async () => (await listed())?.schedule?.everyHours).toBe(4)

  await row.getByRole("button", { name: title }).click()
  await dialog.getByLabel("Schedule").click()
  await page.getByRole("option", { name: "Off" }).click()
  await dialog.getByRole("button", { name: "Save" }).click()
  await expect(dialog).toBeHidden()
  await expect(row).not.toContainText("⟳")
  await expect.poll(async () => (await listed())?.schedule).toBeUndefined()
  await expect(row.getByRole("button", { name: "Run now" })).toHaveCount(0)
})
