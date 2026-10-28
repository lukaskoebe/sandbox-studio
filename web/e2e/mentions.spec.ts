import { addTeammate, expect, sandboxName, test } from "./fixtures"

type Block = { type: string; text?: string; uri?: string; name?: string }

// Every e2e computer's /workspace has the same few files, coupon.ts changed among them
// (internal/e2e/files.go).
test("@ mentions a workspace file: the agent gets links, and the message opens the files", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = `Mentioner ${Math.random().toString(36).slice(2, 6)}`
  const teammate = await addTeammate(api, env, name)
  const sb = sandboxName("mentions")
  const id = await api.createSandbox(sb, teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!

  const box = page.getByRole("textbox", { name: "Message" })
  // On Changes, so the terminal doesn't take the keys.
  await page.goto(
    `/e/${env}/teammates/${teammate}?thread=${chat.id}&tab=changes`
  )
  await box.click()
  const picker = page.getByRole("listbox", { name: "Files to mention" })

  // A folder: Tab goes into it, and the picker lists what is there.
  await box.pressSequentially("Why does @we")
  await expect(picker.getByRole("option").first()).toContainText("web/")
  await box.press("Tab")
  await expect(box).toHaveValue("Why does @web/")
  await expect(picker.getByRole("option")).toHaveText([
    /^src\//,
    /^package\.json/,
  ])

  // A file by its name; Enter adds it.
  await box.press("Backspace")
  await box.press("Backspace")
  await box.press("Backspace")
  await box.press("Backspace")
  await box.pressSequentially("check")
  await expect(picker.getByRole("option").first()).toContainText("checkout.tsx")
  await box.press("Enter")
  await expect(box).toHaveValue("Why does @web/src/checkout/checkout.tsx ")

  // Lines, after a colon; a changed file says so.
  await box.pressSequentially("validate before @coup:20-28")
  await expect(picker.getByRole("option").first()).toContainText("changed")
  await box.press("Enter")
  await expect(box).toHaveValue(
    "Why does @web/src/checkout/checkout.tsx validate before @web/src/checkout/coupon.ts:20-28 "
  )
  // Esc closes the picker; Enter then sends.
  await box.pressSequentially("runs? @RE")
  await expect(picker).toBeVisible()
  await box.press("Escape")
  await expect(picker).toBeHidden()
  await box.press("Enter")

  // The agent gets the text as written and a link to each file.
  await expect
    .poll(async () => (await api.guest(id)).acp[0]?.prompts.length ?? 0)
    .toBe(1)
  const blocks = (await api.guest(id)).acp[0].prompts[0] as Block[]
  const text = blocks.find((b) => b.type === "text")!.text!
  expect(text).toContain(
    "Why does @web/src/checkout/checkout.tsx validate before @web/src/checkout/coupon.ts:20-28 runs? @RE"
  )
  expect(
    blocks.filter((b) => b.type === "resource_link").map((b) => [b.uri, b.name])
  ).toEqual([
    ["file:///workspace/web/src/checkout/checkout.tsx", "checkout.tsx"],
    [
      "file:///workspace/web/src/checkout/coupon.ts?start=20&end=28#L20-L28",
      "coupon.ts",
    ],
  ])

  // In the message, the files are links to the computer's Files.
  const message = page.getByRole("log").or(page.getByLabel("Messages"))
  const chip = message.getByRole("button", { name: "coupon.ts:20-28" })
  await expect(chip).toBeVisible()
  await chip.click()
  await expect(page).toHaveURL(/file=web%2Fsrc%2Fcheckout%2Fcoupon\.ts/)
  const screen = page.getByRole("region", { name: `${sb}'s screen` })
  await expect(screen.locator("pre")).toContainText("applyCoupon")

  // Files mentions the lines selected in a file.
  await screen.locator('[data-line="3"]').click({ clickCount: 3 })
  await page.evaluate(() => {
    const lines = document.querySelectorAll("[data-line]")
    const range = document.createRange()
    range.setStart(lines[2], 0)
    range.setEnd(lines[3], 1)
    const sel = window.getSelection()!
    sel.removeAllRanges()
    sel.addRange(range)
  })
  await screen.getByRole("button", { name: "Mention in chat" }).click()
  await expect(box).toHaveValue("@web/src/checkout/coupon.ts:3-4 ")
})
