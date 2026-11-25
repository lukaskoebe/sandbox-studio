import { expect, sandboxName, test } from "./fixtures"

// The fake guest answers with the base image's harnesses and a few tools from mise
// (internal/e2e defaultTools), and the fake upstream stands in for npm: Claude Code's newest
// stable release is 2.1.340. Its computers have no template, so the global config's tools were
// added in them (internal/sandboxes TestToolOrigins has a template's).
test("Details lists a computer's tools, where each came from, and updates Claude Code", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("tools")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await screen.getByRole("tab", { name: "Details" }).click()
  const card = screen.getByRole("region", { name: "Tools", exact: true })

  const harnesses = card.getByRole("list", { name: "Harnesses" })
  await expect(harnesses).toContainText("Claude Code2.1.325", {
    timeout: 15_000,
  })
  await expect(harnesses).toContainText("Codex0.162.1")
  await expect(harnesses).toContainText("OpenCode1.18.35")
  // Only Claude Code updates.
  await expect(card.getByRole("button", { name: /^Update/ })).toHaveCount(1)

  const mise = card.getByRole("list", { name: "Tools from mise" })
  const rows = mise.getByRole("listitem")
  await expect(rows).toHaveText([
    "node24.11.0added",
    "node22.1.0unused",
    "npm:prettier3.6.2kept",
    "python3.12.12added",
  ])
  await expect(rows.first().getByText("added")).toHaveAttribute(
    "title",
    "Pinned in this computer, with mise use -g. It asks for 24."
  )

  await card.getByRole("button", { name: "Update Claude Code…" }).click()
  const dialog = page.getByRole("alertdialog", {
    name: `Update Claude Code on ${name}?`,
  })
  await expect(dialog).toContainText(
    "Installs Claude Code 2.1.340, the newest stable release, in place of 2.1.325."
  )
  await expect(dialog).toContainText("registry.npmjs.org")
  await dialog.getByRole("button", { name: "Update" }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText(`Claude Code 2.1.340 on ${name}`)).toBeVisible()
  await expect(harnesses).toContainText("Claude Code2.1.340")

  // It's the newest already now, so the dialog has nothing to do.
  await card.getByRole("button", { name: "Update Claude Code…" }).click()
  await expect(dialog).toContainText(
    "2.1.340 is the newest stable release already."
  )
  await expect(dialog.getByRole("button", { name: "Update" })).toBeDisabled()
  await dialog.getByRole("button", { name: "Cancel" }).click()

  // npm's failure is the answer's; a version that isn't one never reaches the guest.
  const update = `/api/environments/${env}/sandboxes/${id}/harnesses/claude/update`
  let res = await api.request.post(update, { data: { version: "9.9.9" } })
  expect(res.status()).toBe(502)
  expect(await res.text()).toContain("ECONNRESET")
  res = await api.request.post(update, { data: { version: "latest" } })
  expect(res.status()).toBe(422)
  res = await api.request.post(
    `/api/environments/${env}/sandboxes/${id}/harnesses/codex/update`,
    { data: { version: "0.170.0" } }
  )
  expect(res.status()).toBe(422)
})

test("Details' tools say what to do with an empty mise, and with a guest from before them", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("old-tools")
  const id = await api.createSandbox(name)
  await api.json("PUT", `/api/e2e/sandboxes/${id}/tools`, { old: true })
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await screen.getByRole("tab", { name: "Details" }).click()
  const card = screen.getByRole("region", { name: "Tools", exact: true })
  await expect(card).toContainText(
    "It started before Studio was upgraded. Restart the computer to list its tools.",
    { timeout: 15_000 }
  )

  await api.json("PUT", `/api/e2e/sandboxes/${id}/tools`, {
    tools: {
      harnesses: [{ harness: "claude", version: "2.1.325" }],
      mise: [],
    },
  })
  await page.reload()
  await screen.getByRole("tab", { name: "Details" }).click()
  await expect(card).toContainText("None yet. mise use -g node@24 adds one", {
    timeout: 15_000,
  })
})
