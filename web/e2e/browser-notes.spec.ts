import type { Page } from "@playwright/test"
import { addTeammate, expect, sandboxName, test, type Api } from "./fixtures"

type Status = { vm: string; url?: string }
type Block = { type: string; text?: string; mimeType?: string }

/** The point of a note in a message, after its start and before its text. */
function spot(message: string, start: string, note: string) {
  const m = message.match(
    new RegExp(String.raw`${start} \[(\d+), (\d+)\] — ${note}`)
  )
  expect(m, `${start} … ${note} in ${message}`).not.toBeNull()
  return m!.slice(1).map(Number)
}
type FakeBrowser = {
  browser?: { inputs: { type: string; eventType?: string; button?: string }[] }
}

/**
 * A teammate with a sandbox and a running browser on the login page, on its Browser tab. add
 * adds the teammate; by default it runs on the environment's first provider.
 */
async function browsing(
  page: Page,
  api: Api,
  env: string,
  add = (name: string) => addTeammate(api, env, name)
) {
  // The chats sent before ask for permissions, whose cards would cover the screen's corner.
  await api.dismissApprovals()
  const name = `Noa ${Math.random().toString(36).slice(2, 8)}`
  const tm = await add(name)
  const sb = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes`,
    { name: sandboxName("notes"), teammateId: tm }
  ))!.id
  await api.guestReady(sb)
  const base = `/api/environments/${env}/teammates/${tm}/browser`
  await api.json("POST", `${base}/start`)
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.vm)
    .toBe("running")
  await page.goto(`/e/${env}/teammates/${tm}?tab=browser`)
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

/**
 * The open note n. A note that just closed can still be on the page while it fades out, with
 * its own Remove and Done, so its buttons are found within the note.
 */
function noteDialog(page: Page, n: number) {
  return page
    .getByRole("dialog")
    .filter({ has: page.getByRole("textbox", { name: `Note ${n}` }) })
}

/** Drives the browser to the fake sign-in page. */
async function signInPage(page: Page, api: Api, base: string) {
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/login")
  await page.waitForTimeout(300) // the frame of the new page
}

// A right-click pins a numbered note instead of reaching the page; an empty note goes when it
// closes, a marker opens its note again, and Send posts the frame with the numbers on it and
// the notes as the user's message, then clears them. Each note names the element under it,
// which Studio asks the page for, and shows its outline while open.
test("right-clicks pin numbered notes, and Send posts them with a screenshot", async ({
  page,
  api,
  env,
}) => {
  const { name, tm, sb, base } = await browsing(page, api, env)
  await signInPage(page, api, base)
  // What the user types into a field never goes with a note on it.
  const canvas = page.getByLabel("Live view of the browser")
  const view = (await canvas.boundingBox())!
  await page.mouse.click(
    view.x + (300 / 640) * view.width,
    view.y + (128 / 400) * view.height
  )
  await page.keyboard.type("ada-secret@example.com")

  // Escape closes an empty note, which goes.
  await rightClick(page, 320, 40)
  await expect(page.getByRole("textbox", { name: "Note 1" })).toBeFocused()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("button", { name: /^Note 1/ })).toHaveCount(0)

  // The note names the button under it and outlines it on the frame.
  await rightClick(page, 120, 306)
  await expect(page.getByText("On button “Sign in”")).toBeVisible()
  const outline = page.getByTestId("note-element")
  await expect(outline).toBeVisible()
  // The notes bar came in above the view, which may have shrunk the frame.
  const held = (await canvas.boundingBox())!
  const drawn = (await outline.boundingBox())!
  const px = held.width / 640
  expect(Math.abs(drawn.x - (held.x + 40 * px))).toBeLessThanOrEqual(2)
  expect(Math.abs(drawn.y - (held.y + 286 * px))).toBeLessThanOrEqual(2)
  expect(Math.abs(drawn.width - 160 * px)).toBeLessThanOrEqual(2)
  expect(Math.abs(drawn.height - 40 * px)).toBeLessThanOrEqual(2)
  await page
    .getByRole("textbox", { name: "Note 1" })
    .fill("This button does nothing")
  await page.keyboard.press("Enter")
  await expect(outline).toHaveCount(0)
  // Hovering its marker outlines the element again.
  await page
    .getByRole("button", { name: "Note 1: This button does nothing" })
    .hover()
  await expect(outline).toBeVisible()

  await rightClick(page, 500, 128)
  await expect(page.getByText("On textbox “Email”")).toBeVisible()
  await page.getByRole("textbox", { name: "Note 2" }).fill("Which email")
  await page.keyboard.press("Enter")
  await expect(page.getByRole("textbox", { name: /^Note/ })).toHaveCount(0)
  await expect(page.getByText("2 notes", { exact: true })).toBeVisible()

  // A marker opens its note, to edit it.
  await page.getByRole("button", { name: "Note 2: Which email" }).click()
  await page
    .getByRole("textbox", { name: "Note 2" })
    .fill("Which email does it want?")
  await noteDialog(page, 2).getByRole("button", { name: "Done" }).click()
  await expect(
    page.getByRole("button", { name: "Note 2: Which email does it want?" })
  ).toBeVisible()

  // The right-clicks stayed on the live view.
  const fake = (await api.json<FakeBrowser>(
    "GET",
    `/api/e2e/teammates/${tm}/browser`
  ))!
  expect(
    fake.browser!.inputs.filter(
      (i) => i.eventType === "mousePressed" && i.button !== "left"
    )
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
  const blocks = (await api.guest(sb)).acp[0].prompts[0] as Block[]
  const text = blocks.find((b) => b.type === "text")!.text!
  expect(text).toContain(
    "I marked 2 spots on your browser's page, https://example.com/login. " +
      "The attached screenshot shows them as numbered circles, with a box around the element under each. " +
      "Each note names the element under it"
  )
  expect(text).toContain("What form fields hold is left out.")
  // Each note names its element, then its spot in page pixels, as a click there would land.
  const [x1, y1] = spot(
    text,
    String.raw`1\. button "Sign in" \(#sign-in\), at`,
    "This button does nothing"
  )
  expect(Math.abs(x1 - 120)).toBeLessThanOrEqual(2)
  expect(Math.abs(y1 - 306)).toBeLessThanOrEqual(2)
  const [x2, y2] = spot(
    text,
    String.raw`2\. textbox "Email" \(#email\), a field of type email named "email", at`,
    "Which email does it want\\?"
  )
  expect(Math.abs(x2 - 500)).toBeLessThanOrEqual(2)
  expect(Math.abs(y2 - 128)).toBeLessThanOrEqual(2)
  expect(JSON.stringify(blocks)).not.toContain("ada-secret")
  const image = blocks.find((b) => b.type === "image")
  expect(image?.mimeType).toBe("image/jpeg")
})

// An agent that can't take images gets the notes as text alone, and the user is told; each
// note still names its element and point, and one the page couldn't name keeps its point.
test("notes go as text alone to an agent that can't take images", async ({
  page,
  api,
  env,
}) => {
  const { name, sb, base } = await browsing(page, api, env)
  await api.json("PUT", `/api/e2e/sandboxes/${sb}/images`, { on: false })
  await signInPage(page, api, base)

  await rightClick(page, 120, 306)
  await expect(page.getByText("On button “Sign in”")).toBeVisible()
  await page.getByRole("textbox", { name: "Note 1" }).fill("Press this")
  await page.keyboard.press("Enter")
  // A note whose lookup fails keeps its point alone.
  await page.route("**/browser/element?*", (route) => route.abort())
  await rightClick(page, 500, 360)
  await page.getByRole("textbox", { name: "Note 2" }).fill("Empty here")
  await page.keyboard.press("Enter")
  await page.unroute("**/browser/element?*")

  await page.getByRole("button", { name: "Send to chat" }).click()
  await expect(page.getByText(/Notes sent to a new chat in/)).toBeVisible()
  await expect(
    page.getByText(
      `${name}'s agent can't take images, so the screenshot stayed behind.`,
      { exact: false }
    )
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /^Note \d/ })).toHaveCount(0)

  // The chat the screenshot was refused for went; its agent got no prompt.
  const prompts = async () =>
    (await api.guest(sb)).acp.flatMap((a) => a.prompts) as Block[][]
  await expect.poll(async () => (await prompts()).length).toBe(1)
  const blocks = (await prompts())[0]
  expect(blocks.map((b) => b.type)).toEqual(["text"])
  const text = blocks[0].text!
  expect(text).not.toContain("screenshot")
  expect(text).toContain(
    "A note names the element under it where Studio could tell"
  )
  expect(text).toContain(
    "your browser_snapshot names its elements the same way"
  )
  spot(text, String.raw`1\. button "Sign in" \(#sign-in\), at`, "Press this")
  const [x2, y2] = spot(text, String.raw`2\. At`, "Empty here")
  expect(Math.abs(x2 - 500)).toBeLessThanOrEqual(2)
  expect(Math.abs(y2 - 360)).toBeLessThanOrEqual(2)
})

// A model that its provider says reads text only gets the notes as text alone too, though its
// agent takes images: Studio holds the screenshot back rather than let the agent swap it for
// an error the model reads.
test("notes go as text alone to a model that reads text only", async ({
  page,
  api,
}) => {
  // An environment of its own: the other tests' teammates run on the first provider there is.
  const env = (await api.json<{ id: string }>("POST", "/api/environments", {
    name: `text-only-${Date.now()}`,
  }))!.id
  const local = (await api.json<{ id: string; images?: boolean }>(
    "POST",
    `/api/environments/${env}/providers`,
    {
      name: "Local",
      kind: "openai_compatible",
      apiKey: "sk-local-e2e-fake",
      baseUrl: "https://llm.example.com/v1",
      model: "qwen3-coder",
    }
  ))!
  expect(local.images ?? false).toBe(false)
  const { name, sb, base } = await browsing(
    page,
    api,
    env,
    async (teammate) =>
      (await api.json<{ id: string }>(
        "POST",
        `/api/environments/${env}/teammates`,
        { name: teammate, harness: "opencode", providerId: local.id }
      ))!.id
  )
  await signInPage(page, api, base)
  await rightClick(page, 120, 306)
  await expect(page.getByText("On button “Sign in”")).toBeVisible()
  await page.getByRole("textbox", { name: "Note 1" }).fill("Press this")
  await page.keyboard.press("Enter")

  await page.getByRole("button", { name: "Send to chat" }).click()
  await expect(
    page.getByText(
      `${name}'s agent can't take images, so the screenshot stayed behind.`,
      { exact: false }
    )
  ).toBeVisible()
  const prompts = async () =>
    (await api.guest(sb)).acp.flatMap((a) => a.prompts) as Block[][]
  await expect.poll(async () => (await prompts()).length).toBe(1)
  const blocks = (await prompts())[0]
  expect(blocks.map((b) => b.type)).toEqual(["text"])
  spot(
    blocks[0].text!,
    String.raw`1\. button "Sign in" \(#sign-in\), at`,
    "Press this"
  )
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
  await noteDialog(page, 1).getByRole("button", { name: "Remove" }).click()
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
