import type { Locator, Page } from "@playwright/test"
import { addTeammate, expect, sandboxName, test, type Api } from "./fixtures"

// The browser window's view fits the frame into the room it has, so it never scrolls: not
// while watching, not with the controls under the frame while driving, and not once the
// wheel is handed back, on the teammate's screen and on a phone's, which opens over the
// conversation.

/** A teammate with a sandbox and a running browser on the login page. */
async function running(api: Api, env: string) {
  // A long name makes the "take the wheel" overlay as wide as it gets.
  const name = `Bartholomew ${Math.random().toString(36).slice(2, 8)}`
  const tm = await addTeammate(api, env, name)
  await api.createSandbox(sandboxName("layout"), tm)
  const base = `/api/environments/${env}/teammates/${tm}/browser`
  await api.json("POST", `${base}/start`)
  await expect
    .poll(async () => (await api.json<{ vm: string }>("GET", base))!.vm)
    .toBe("running")
  return { name, tm }
}

/** How far the view's content reaches past its box, across and down. */
function overflow(view: Locator) {
  return view.evaluate((el) => ({
    x: el.scrollWidth - el.clientWidth,
    y: el.scrollHeight - el.clientHeight,
  }))
}

/**
 * Takes the wheel, opens the keyboard controls and hands back, checking after each step,
 * once the view has settled, that the view can't scroll. A phone shows the controls from the
 * start.
 */
async function takeAndHandBack(
  page: Page,
  within: Locator,
  name: string,
  phone = false
) {
  const view = within.getByTestId("browser-view")
  const settled = async (step: string) => {
    // The controls' height is measured after they render, and the frame fits itself to it.
    await page.waitForTimeout(400)
    expect(await overflow(view), step).toEqual({ x: 0, y: 0 })
  }
  await expect(within.getByLabel("Live view of the browser")).toBeVisible()
  await settled("watching")

  const address = within.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  await expect(within.getByText("You're driving.")).toBeVisible()
  if (!phone) {
    await expect(
      within.getByRole("button", { name: "Keyboard controls" })
    ).toBeVisible()
    await settled("driving")
    await within.getByRole("button", { name: "Keyboard controls" }).click()
  }
  await expect(
    within.getByRole("group", { name: "Remote browser keyboard controls" })
  ).toBeVisible()
  await settled("driving with the keyboard controls open")

  await within.getByRole("button", { name: `Hand back to ${name}` }).click()
  await expect(
    within.getByText(`${name} isn't using the browser.`)
  ).toBeVisible()
  await expect(
    within.getByRole("button", { name: /Keyboard controls/ })
  ).toHaveCount(0)
  await settled("handed back")

  // The overlay that offers the wheel shows on hover; the canvas itself takes no pointer
  // while watching.
  const box = (await within
    .getByLabel("Live view of the browser")
    .boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await expect(
    within.getByRole("button", { name: `Watching ${name} · take the wheel` })
  ).toBeVisible()
  await settled("hovered after handing back")
}

const windows = [
  { width: 1280, height: 800 },
  { width: 1600, height: 1000 },
  { width: 1100, height: 640 },
  { width: 900, height: 1000 },
]

for (const size of windows) {
  test.describe(`a ${size.width}×${size.height} window`, () => {
    test.use({ viewport: size })

    test("the screen's browser view never scrolls", async ({
      page,
      api,
      env,
    }) => {
      const { name, tm } = await running(api, env)
      await page.goto(`/e/${env}/teammates/${tm}?tab=browser`)
      await takeAndHandBack(page, page.locator("main"), name)
    })
  })
}

test.describe("a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test("the screen's browser view never scrolls", async ({
    page,
    api,
    env,
  }) => {
    const { name, tm } = await running(api, env)
    await page.goto(`/e/${env}/teammates/${tm}?tab=browser`)
    await takeAndHandBack(page, page.locator("main"), name, true)
  })
})
