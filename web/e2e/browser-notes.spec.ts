import type { Page } from "@playwright/test"
import { addTeammate, expect, sandboxName, test, type Api } from "./fixtures"

type Status = { vm: string; url?: string }
type FakeBrowser = {
  browser?: { inputs: { type: string; eventType?: string; button?: string }[] }
}

/** A teammate with a sandbox and a running browser on the login page, on its Browser tab. */
async function browsing(page: Page, api: Api, env: string) {
  const name = `Noa ${Math.random().toString(36).slice(2, 8)}`
  const tm = await addTeammate(api, env, name)
  const sb = await api.createSandbox(sandboxName("notes"), tm)
  await api.guestReady(sb)
  const base = `/api/environments/${env}/teammates/${tm}/browser`
  await api.json("POST", `${base}/start`)
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.vm)
    .toBe("running")
  await page.goto(`/e/${env}/teammates/${tm}/browser`)
  return { name, tm, sb, base }
}

/** Right-clicks the live view at (x, y), in the fake page's 640x400 CSS pixels. */
async function rightClick(page: Page, x: number, y: number) {
  const canvas = page.getByLabel("Live view of the browser")
  const box = (await canvas.boundingBox())!
  await page.mouse.click(
    box.x + (x / 640) * box.width,
    box.y + (y / 400) * box.height,
    { button: "right" }
  )
}

// A right-click pins a numbered note instead of reaching the page; an empty note goes when it
// closes, a marker opens its note again, and Send posts the frame with the numbers on it and
// the notes as the user's message, then clears them.
test("right-clicks pin numbered notes, and Send posts them with a screenshot", async ({
  page,
  api,
  env,
}) => {
  const { name, tm, sb, base } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  await expect(page.getByText("You're driving.")).toBeVisible()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/login")
  await page.waitForTimeout(300) // the frame of the new page

  // Escape closes an empty note, which goes.
  await rightClick(page, 320, 40)
  await expect(page.getByRole("textbox", { name: "Note 1" })).toBeFocused()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("button", { name: /^Note 1/ })).toHaveCount(0)

  await rightClick(page, 120, 306)
  await page
    .getByRole("textbox", { name: "Note 1" })
    .fill("This button does nothing")
  await page.keyboard.press("Enter")
  await rightClick(page, 500, 100)
  await page.getByRole("textbox", { name: "Note 2" }).fill("Too much space")
  await page.keyboard.press("Enter")
  await expect(page.getByRole("textbox", { name: /^Note/ })).toHaveCount(0)
  await expect(page.getByText("2 notes", { exact: true })).toBeVisible()

  // A marker opens its note, to edit it.
  await page.getByRole("button", { name: "Note 2: Too much space" }).click()
  await page
    .getByRole("textbox", { name: "Note 2" })
    .fill("Too much space above the title")
  await page.getByRole("button", { name: "Done" }).click()
  await expect(
    page.getByRole("button", { name: "Note 2: Too much space above the title" })
  ).toBeVisible()

  // The right-clicks stayed on the live view.
  const fake = (await api.json<FakeBrowser>(
    "GET",
    `/api/e2e/teammates/${tm}/browser`
  ))!
  expect(
    fake.browser!.inputs.filter((i) => i.eventType === "mousePressed")
  ).toEqual([])

  await expect(
    page.getByRole("combobox", { name: "Send the notes to" })
  ).toContainText("A new chat in notes-")
  await page.getByRole("button", { name: "Send to chat" }).click()
  await expect(page.getByText(/Notes sent to a new chat in/)).toBeVisible()
  await expect(page.getByRole("button", { name: /^Note \d/ })).toHaveCount(0)
  await expect(page.getByText("2 notes", { exact: true })).toBeHidden()
  await expect(
    page.getByText(`Right-click to add a note for ${name}`)
  ).toBeAttached()

  await expect.poll(async () => (await api.guest(sb)).acp.length).toBe(1)
  await expect
    .poll(async () => (await api.guest(sb)).acp[0].prompts.length)
    .toBe(1)
  const prompt = JSON.stringify((await api.guest(sb)).acp[0].prompts[0])
  expect(prompt).toContain(
    "I marked 2 spots on your browser's page, https://example.com/login."
  )
  // Each note carries its spot in page pixels, as a click there would land.
  const spot = (text: string) =>
    prompt
      .match(new RegExp(String.raw`\d\. \[(\d+), (\d+)\] ${text}`))!
      .slice(1)
      .map(Number)
  const [x1, y1] = spot("This button does nothing")
  expect(Math.abs(x1 - 120)).toBeLessThanOrEqual(2)
  expect(Math.abs(y1 - 306)).toBeLessThanOrEqual(2)
  const [x2, y2] = spot("Too much space above the title")
  expect(Math.abs(x2 - 500)).toBeLessThanOrEqual(2)
  expect(Math.abs(y2 - 100)).toBeLessThanOrEqual(2)
  expect(prompt).toMatch(/1\. \[\d+, \d+\] This button does nothing/)
  expect(prompt).toMatch(/2\. \[\d+, \d+\] Too much space above the title/)
  expect(prompt).toContain('"type":"image"')
  expect(prompt).toContain('"mimeType":"image/jpeg"')
})

// Remove takes a note away and renumbers the others; Clear takes them all.
test("Remove takes one note away, and Clear the rest", async ({
  page,
  api,
  env,
}) => {
  await browsing(page, api, env)
  const canvas = page.getByLabel("Live view of the browser")
  await expect(canvas).toBeVisible()
  await page.waitForTimeout(300) // the first frame

  for (const [i, [x, y]] of [
    [100, 100],
    [300, 200],
    [500, 300],
  ].entries()) {
    await rightClick(page, x, y)
    await page
      .getByRole("textbox", { name: `Note ${i + 1}` })
      .fill(`Spot ${i + 1}`)
    await page.keyboard.press("Enter")
  }
  await expect(page.getByText("3 notes", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Note 1: Spot 1" }).click()
  await page.getByRole("button", { name: "Remove" }).click()
  await expect(page.getByText("2 notes", { exact: true })).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Note 1: Spot 2" })
  ).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Note 2: Spot 3" })
  ).toBeVisible()

  await page.getByRole("button", { name: "Clear", exact: true }).click()
  await expect(page.getByRole("button", { name: /^Note \d/ })).toHaveCount(0)
  await expect(page.getByText("2 notes", { exact: true })).toBeHidden()
})
