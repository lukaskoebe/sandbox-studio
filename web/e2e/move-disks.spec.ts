import type { Page } from "@playwright/test"
import { expect, sandboxName, test } from "./fixtures"

/** The sandbox's screen, once its creation is done. */
async function screenOf(page: Page, env: string, id: string, name: string) {
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await expect(
    screen.getByRole("tablist", { name: `${name}'s screen` })
  ).toBeVisible()
  return screen
}

// After the update to microsandbox 0.7.15 a Mac keeps the sandboxes' disks in the old layout
// until they're moved: Studio refuses a copy before it stops a sandbox that runs, says what to
// do on Home and on the stopped computer's screen, and Move disks stops, moves and restarts.
test("disks in microsandbox's old layout are moved from Home after the person confirms", async ({
  page,
  api,
  env,
}) => {
  const running = sandboxName("old-run")
  const stopped = sandboxName("old-stop")
  const runningID = await api.createSandbox(running)
  const stoppedID = await api.createSandbox(stopped)
  await screenOf(page, env, runningID, running)
  const screen = await screenOf(page, env, stoppedID, stopped)
  await api.json("POST", `/api/environments/${env}/sandboxes/${stoppedID}/stop`)
  for (const id of [runningID, stoppedID])
    await api.json("PUT", `/api/e2e/sandboxes/${id}/layout`, { old: true })

  // The stopped one can't start: its screen offers the move instead of Start.
  await page.reload()
  await expect(
    screen.getByText(/microsandbox keeps its disks in the layout from before/)
  ).toBeVisible()
  await expect(
    screen.getByRole("button", { name: "Start", exact: true })
  ).toBeHidden()
  await expect(
    screen.getByRole("button", { name: "Move disks…" })
  ).toBeVisible()

  // A rebase of the running one is refused before anything stops it.
  const rebase = await page.request.post(
    `/api/environments/${env}/sandboxes/${runningID}/rebase`,
    { data: {} }
  )
  expect(rebase.status()).toBe(409)
  expect(await rebase.text()).toContain(
    "must move this sandbox's disks to its new layout"
  )
  const still = await api.json<{ status: string; oldDiskLayout?: boolean }>(
    "GET",
    `/api/environments/${env}/sandboxes/${runningID}`
  )
  expect(still).toMatchObject({ status: "running", oldDiskLayout: true })

  const listed = await api.json<{
    sandboxes: { name: string; status: string; own?: boolean }[]
  }>("GET", "/api/system/move-disks")
  expect(listed?.sandboxes).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ name: running, status: "running" }),
      expect.objectContaining({ name: stopped, status: "stopped" }),
    ])
  )

  await page.goto(`/e/${env}`)
  const notice = page.getByRole("region", { name: "Disk layout" })
  await expect(notice).toContainText(running)
  await expect(notice).toContainText(stopped)
  await notice.getByRole("button", { name: "Move disks…" }).click()
  const dialog = page.getByRole("dialog", {
    name: "Move disks to the new layout",
  })
  const rows = dialog.getByRole("list", { name: "Sandboxes whose disks move" })
  await expect(
    rows.getByRole("listitem").filter({ hasText: running })
  ).toContainText("stops, then starts again")
  await expect(
    rows.getByRole("listitem").filter({ hasText: stopped })
  ).toContainText("stays stopped")
  // Only the person can say that nothing from before the update still writes to the disks.
  const move = dialog.getByRole("button", { name: "Move disks" })
  await expect(move).toBeDisabled()
  await dialog
    .getByRole("checkbox", {
      name: /No Studio or msb from before the update still runs/,
    })
    .click()
  await move.click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText("Moved the disks of 2 sandboxes")).toBeVisible()
  await expect(notice).toBeHidden()

  const after = await api.json<{ status: string; oldDiskLayout?: boolean }>(
    "GET",
    `/api/environments/${env}/sandboxes/${runningID}`
  )
  expect(after?.status).toBe("running")
  expect(after?.oldDiskLayout).toBeFalsy()
  await screenOf(page, env, runningID, running)
  await page.goto(`/e/${env}/sandboxes/${stoppedID}`)
  await expect(
    screen.getByRole("button", { name: "Start", exact: true })
  ).toBeVisible()
  await expect(screen.getByRole("button", { name: "Move disks…" })).toBeHidden()
})
