import { addTeammate, expect, sandboxName, test } from "./fixtures"

// Environment › Computers lists every computer: who uses it and the thread on it now, an agent
// in a terminal among them. A teammate's computer opens on its desk; the user's own on its page.
test("the Computers page lists who uses each computer and what runs there", async ({
  page,
  api,
  env,
}) => {
  const base = `/api/environments/${env}`
  const name = `Lister ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const theirs = sandboxName("listed")
  const theirsId = await api.createSandbox(theirs, id)
  await api.guestReady(theirsId)
  const mine = sandboxName("own")
  await api.createSandbox(mine)
  // The teammate's agent in a terminal of its own: a thread with no chat behind it.
  await api.json("POST", `${base}/sandboxes/${theirsId}/sessions`)
  // Studio asks the guests what runs in their terminals every 10 s.
  await expect
    .poll(
      async () =>
        (await api.json<
          { sandboxId: string; terminals?: { harness?: string }[] }[]
        >("GET", `${base}/terminals`))!.some(
          (s) => s.sandboxId === theirsId && s.terminals?.some((t) => t.harness)
        ),
      { timeout: 30_000 }
    )
    .toBe(true)

  await page.goto(`/e/${env}/computers`)
  const rows = page.getByTestId("computer-row")
  const row = rows.filter({ hasText: theirs })
  await expect(row).toContainText(name)
  await expect(row).toContainText(`${name} in a terminal`)
  await expect(rows.filter({ hasText: mine })).toContainText("you")

  // Yours leaves the teammates' computers out.
  const show = page.getByRole("group", { name: "Show" })
  await show.getByRole("button", { name: /^Yours/ }).click()
  await expect(rows.filter({ hasText: mine })).toBeVisible()
  await expect(row).toHaveCount(0)
  await show.getByRole("button", { name: /^All/ }).click()

  // A teammate's computer opens on its owner's desk, at the terminal.
  await row.getByRole("cell").nth(1).click()
  await expect(page).toHaveURL(
    new RegExp(`/teammates/${id}\\?.*computer=${theirsId}`)
  )
  await expect(page).toHaveURL(/[?&]tab=terminal/)
})
