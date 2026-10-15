import type { Page } from "@playwright/test"
import { addTeammate, expect, sandboxName, test, type Api } from "./fixtures"

type Pattern = { id: string; origin: string; action: string; verdict: string }

// Without a browser VM the window says so and offers to open a page; the rail beside it has the
// activity and the sites, whose rules are set in plain words.
test("a teammate's browser tab shows the sleeping window, its activity and its sites", async ({
  page,
  api,
  env,
}) => {
  const name = `Browse ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  const patterns = `/api/environments/${env}/teammates/${id}/browser/patterns`
  await api.json("POST", patterns, {
    origin: "https://shop.example.com",
    action: "*",
    verdict: "allow",
  })

  await page.goto(`/e/${env}/teammates/${id}/browser`)
  await expect(page.getByText(`${name} has no browser yet`)).toBeVisible()
  await expect(page.getByLabel("Open a page")).toBeVisible()
  await expect(page.getByText(/Nothing yet\. Every step/)).toBeVisible()

  await page.getByRole("tab", { name: "Sites" }).click()
  const shop = page.getByRole("region", { name: "shop.example.com" })
  await expect(shop.getByRole("radio", { name: "Allowed" })).toBeChecked()

  // Blocked replaces the site-wide rule; Ask first removes it.
  await shop.getByRole("radio", { name: "Blocked" }).click()
  await expect(shop.getByRole("radio", { name: "Blocked" })).toBeChecked()
  let list = (await api.json<Pattern[]>("GET", patterns))!
  expect(list.map((p) => [p.origin, p.action, p.verdict])).toEqual([
    ["https://shop.example.com", "*", "deny"],
  ])
  await shop.getByRole("radio", { name: "Ask first" }).click()
  await expect
    .poll(async () => (await api.json<Pattern[]>("GET", patterns))!)
    .toEqual([])
  // A site without rules or visits leaves the list.
  await expect(shop).toBeHidden()

  // A field to hide, from the advanced form, shows as a chip on its site and can be removed.
  await page.getByRole("button", { name: /Add a rule or hide a field/ }).click()
  const form = page.getByRole("form", { name: "Add a browser rule" })
  await form.getByRole("combobox", { name: "Rule" }).click()
  await page.getByRole("option", { name: "Hide field" }).click()
  await form.getByLabel("Site").fill("https://bank.example.com")
  await form.getByLabel("Label").fill("Card number")
  await form.getByRole("button", { name: "Add rule" }).click()
  const bank = page.getByRole("region", { name: "bank.example.com" })
  await expect(bank.getByText("“Card number”")).toBeVisible()
  await bank.getByRole("button", { name: "Remove the rule" }).click()
  await expect(bank).toBeHidden()
  list = (await api.json<Pattern[]>("GET", patterns))!
  expect(list).toEqual([])
})

// The same window sits beside a sandbox's terminal, and opens on the teammate's page.
test("a teammate's sandbox opens the browser as a side panel", async ({
  page,
  api,
  env,
}) => {
  const name = `Panel ${Math.random().toString(36).slice(2, 8)}`
  const tm = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("browse"), tm)
  await page.goto(`/e/${env}/sandboxes/${id}/work`)
  await page.getByRole("button", { name: "Browser", exact: true }).click()
  const panel = page.getByRole("complementary", { name: "Teammate browser" })
  await expect(panel.getByText(`${name}'s browser`)).toBeVisible()
  await expect(panel.getByText(`${name} has no browser yet`)).toBeVisible()
  await panel.getByRole("link", { name: `Open on ${name}'s page` }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${tm}/browser$`))
  await expect(page.getByRole("tab", { name: "Activity" })).toBeVisible()
})

type Status = {
  vm: string
  url?: string
  takeover?: boolean
  sessions?: { id: string }[]
}
type Call = { id: number; done: boolean; result?: unknown; error?: string }
type FakeBrowser = {
  browser?: {
    sessions: { slot: number; url: string }[]
    inputs: { type: string; eventType?: string; x?: number; y?: number }[]
    stealth: boolean
  }
}

/** A teammate with a sandbox and a running browser, on its Browser tab. */
async function browsing(page: Page, api: Api, env: string) {
  const name = `Ada ${Math.random().toString(36).slice(2, 8)}`
  const tm = await addTeammate(api, env, name)
  const sb = await api.createSandbox(sandboxName("web"), tm)
  const base = `/api/environments/${env}/teammates/${tm}/browser`
  await api.json("POST", `${base}/start`)
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.vm)
    .toBe("running")
  const call = (method: string, params: object, wait = true) =>
    api.json<Call>("POST", `/api/e2e/sandboxes/${sb}/calls`, {
      method,
      params,
      wait,
    })
  await page.goto(`/e/${env}/teammates/${tm}/browser`)
  return { name, tm, sb, base, call }
}

// Typing an address takes the wheel and opens the page; Back moves in its history, and Hand
// back gives the browser to the teammate again.
test("the address bar takes the wheel, Back goes back, and Hand back returns it", async ({
  page,
  api,
  env,
}) => {
  const { name, base } = await browsing(page, api, env)
  await expect(page.getByText(`${name} isn't using the browser.`)).toBeVisible()

  const address = page.getByLabel("Browser address")
  await address.fill("example.com/a")
  await expect(page.getByText(`↵ takes the wheel from ${name}`)).toBeVisible()
  await address.press("Enter")
  await expect(page.getByText("You're driving.")).toBeVisible()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/a")
  await address.fill("https://example.com/b")
  await address.press("Enter")
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/b")
  await expect(address).toHaveValue("example.com/b")

  await page.getByRole("button", { name: "Back", exact: true }).click()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/a")
  await expect(address).toHaveValue("example.com/a")
  // What is typed as the field focuses replaces the address it shows.
  await address.fill("example.com/c")
  await expect(address).toHaveValue("example.com/c")
  await address.blur()
  await expect(page.getByRole("tab", { name: /example\.com/ })).toBeVisible()

  await page.getByRole("button", { name: `Hand back to ${name}` }).click()
  await expect(page.getByText(`${name} isn't using the browser.`)).toBeVisible()
  expect((await api.json<Status>("GET", base))!.takeover).toBeFalsy()
})

// The live view maps a click through the frame to the page: pressing "Sign in" on the canvas
// lands on the button, which goes to /welcome.
test("a click on the live view lands on the right page coordinates", async ({
  page,
  api,
  env,
}) => {
  const { tm, base } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  await expect(page.getByText("You're driving.")).toBeVisible()

  const canvas = page.getByLabel("Live view of the browser")
  await expect(canvas).toBeVisible()
  // Frames are 640x400 CSS pixels; "Sign in" spans (40,286)-(200,326).
  const box = (await canvas.boundingBox())!
  await page.waitForTimeout(300) // the frame of the new page
  await page.mouse.click(
    box.x + (120 / 640) * box.width,
    box.y + (306 / 400) * box.height
  )
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/welcome")
  const state = (await api.json<FakeBrowser>(
    "GET",
    `/api/e2e/teammates/${tm}/browser`
  ))!
  const press = state.browser!.inputs.find(
    (i) => i.eventType === "mousePressed"
  )!
  expect(Math.abs(press.x! - 120)).toBeLessThanOrEqual(2)
  expect(Math.abs(press.y! - 306)).toBeLessThanOrEqual(2)
})

// The teammate's question shows on the page, not also in the approval stack; once allowed, the
// click goes through and its step replays from the activity.
test("an agent's question shows on the page, and its step replays from the activity", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const { name, sb, call } = await browsing(page, api, env)
  await call("browser_open", { url: "https://example.com/login" })
  // The address bar follows the teammate's page.
  await expect(page.getByLabel("Browser address")).toHaveValue(
    "example.com/login"
  )
  await call("browser_snapshot", {})
  const click = (await call("browser_click", { ref: "e4" }, false))!

  const ask = page.getByRole("alertdialog", { name: `${name} is asking` })
  await expect(ask).toBeVisible()
  await expect(ask.getByText(/wants to click/)).toBeVisible()
  await expect(page.getByText(`${name} is waiting for you`)).toBeVisible()
  await expect(page.getByTestId("approval-card")).toHaveCount(0)
  await ask.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(ask).toBeHidden()
  // The held call goes on once allowed, and the click lands.
  await expect
    .poll(
      async () =>
        (await api.json<Call>(
          "GET",
          `/api/e2e/sandboxes/${sb}/calls/${click.id}`
        ))!
    )
    .toMatchObject({ done: true })
  const done = (await api.json<Call>(
    "GET",
    `/api/e2e/sandboxes/${sb}/calls/${click.id}`
  ))!
  expect(JSON.stringify(done.result)).toContain("/welcome")

  // The click that ran, not the one that asked.
  const step = page
    .getByRole("complementary")
    .getByRole("button", { name: /click button "Sign in"$/ })
  await expect(step).toBeVisible()
  await step.click()
  await expect(page.getByText("Replay", { exact: true }).first()).toBeVisible()
  await expect(
    page.getByRole("img", { name: /The page after click/ })
  ).toBeVisible()
  await page.getByRole("button", { name: "Back to live" }).click()
  await expect(page.getByLabel("Live view of the browser")).toBeVisible()
})

// A throwaway session an agent opens is a tab of the window; its X ends it.
test("a throwaway session is a tab, and closing the tab ends it", async ({
  page,
  api,
  env,
}) => {
  const { base, call } = await browsing(page, api, env)
  const opened = (await call("browser_open", {
    url: "https://example.org/",
    ephemeral: true,
  }))!
  expect(JSON.stringify(opened.result)).toContain("eph-")
  const tab = page.getByRole("tab", { name: "example.org" })
  await expect(tab).toBeVisible()
  await tab.click()
  await expect(page.getByText(/uses this throwaway session/)).toBeVisible()
  await page.getByRole("button", { name: "End the throwaway session" }).click()
  await expect(tab).toBeHidden()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.sessions ?? [])
    .toEqual([])
})

// Stealth mode is switched in the teammate's settings, and the browser's next command runs
// with it.
test("stealth mode is switched in the settings and reaches the browser", async ({
  page,
  api,
  env,
}) => {
  const { tm, call } = await browsing(page, api, env)
  const fake = () =>
    api.json<FakeBrowser>("GET", `/api/e2e/teammates/${tm}/browser`)
  await call("browser_snapshot", {})
  expect((await fake())!.browser!.stealth).toBe(false)

  await page.goto(`/e/${env}/teammates/${tm}/settings`)
  const stealth = page.getByRole("switch", { name: "Stealth mode" })
  await expect(stealth).not.toBeChecked()
  await stealth.click()
  await expect(page.getByText("Stealth mode on")).toBeVisible()
  await expect(stealth).toBeChecked()

  await call("browser_snapshot", {})
  expect((await fake())!.browser!.stealth).toBe(true)
})
