import type { Page } from "@playwright/test"
import { addTeammate, expect, sandboxName, test, type Api } from "./fixtures"

type Status = { vm: string; url?: string }
type Secret = {
  id: string
  name: string
  hosts?: string[]
  teammateId?: string
  browserOnly: boolean
  username?: string
  agentMayUse: boolean
}

/** A teammate with a running browser, driven by the user on a sign-in page of a host of its own. */
async function signInPage(page: Page, api: Api, env: string) {
  const name = `Ada ${Math.random().toString(36).slice(2, 8)}`
  const host = `shop-${Math.random().toString(36).slice(2, 8)}.test`
  const tm = await addTeammate(api, env, name)
  await api.createSandbox(sandboxName("web"), tm)
  const base = `/api/environments/${env}/teammates/${tm}/browser`
  await api.json("POST", `${base}/start`)
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.vm)
    .toBe("running")
  await page.goto(`/e/${env}/teammates/${tm}?tab=browser`)
  const address = page.getByLabel("Browser address")
  await address.fill(`https://${host}/login`)
  await address.press("Enter")
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe(`https://${host}/login`)
  const logins = async () =>
    (await api.json<Secret[]>(
      "GET",
      `/api/environments/${env}/secrets`
    ))!.filter((s) => s.browserOnly && s.teammateId === tm)
  return { name, host, tm, base, logins }
}

// A click on the fake page's frame, which is 640x400 CSS pixels.
async function clickPage(page: Page, x: number, y: number) {
  const canvas = page.getByLabel("Live view of the browser")
  const box = (await canvas.boundingBox())!
  await page.mouse.click(
    box.x + (x / 640) * box.width,
    box.y + (y / 400) * box.height
  )
}

// The fake sign-in form: the password field spans (40,166)-(600,202), Sign in (40,286)-(200,326).
const passwordField = [300, 184] as const
const signInButton = [120, 306] as const

// A password typed into the live view is offered as a login of the teammate's browser once the
// field is left; saved, it is not offered again, and the site info copies it and switches
// whether agents use it without asking.
test("a password typed into the live view is saved as a browser login", async ({
  page,
  context,
  api,
  env,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  const { name, host, logins } = await signInPage(page, api, env)
  await page.waitForTimeout(300) // the frame of the new page
  await clickPage(page, ...passwordField)
  await page.keyboard.type("hunter3")
  await page.keyboard.press("Backspace")
  await page.keyboard.type("2")
  await clickPage(page, ...signInButton)

  const card = page.getByRole("form", { name: "Save password" })
  await expect(card).toContainText(
    `Save this password for ${host} in ${name}'s browser?`
  )
  const loginName = host.toUpperCase().replace(/[^A-Z0-9]+/g, "_")
  await expect(card.getByLabel("Name", { exact: true })).toHaveValue(loginName)
  await card.getByLabel("Username (optional)").fill("ada")
  await card
    .getByRole("switch", { name: `Let ${name}'s agents use it without asking` })
    .click()
  await card.getByRole("button", { name: "Save", exact: true }).click()
  await expect(card).toBeHidden()
  await expect.poll(async () => (await logins()).length).toBe(1)
  const [login] = await logins()
  expect(login).toMatchObject({
    name: loginName,
    hosts: [host],
    username: "ada",
    agentMayUse: true,
  })
  const value = await api.json<{ value: string }>(
    "GET",
    `/api/environments/${env}/secrets/${login.id}/value`
  )
  expect(value!.value).toBe("hunter2")

  // The same password again is not offered.
  const address = page.getByLabel("Browser address")
  await address.fill(`https://${host}/login`)
  await address.press("Enter")
  await page.waitForTimeout(300)
  await clickPage(page, ...passwordField)
  await page.keyboard.type("hunter2")
  await page.keyboard.press("Enter")
  await page.waitForTimeout(1000)
  await expect(card).toHaveCount(0)

  // The site info has it: Copy password reads it back, and the switch takes agents off it.
  await address.fill(`https://${host}/login`)
  await address.press("Enter")
  await page.getByRole("button", { name: `Site info for ${host}` }).click()
  await page
    .getByRole("button", { name: `Copy the password of ${loginName}` })
    .click()
  await expect(
    page.getByText(`Copied the password of ${loginName}`)
  ).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    "hunter2"
  )
  await page
    .getByRole("switch", { name: `${name}'s agents use it without asking` })
    .click()
  await expect.poll(async () => (await logins())[0].agentMayUse).toBe(false)
  await page.keyboard.press("Escape")

  // Settings → Secrets lists it under the teammate's browser logins.
  await page.goto(`/e/${env}/settings/secrets`)
  const section = page.getByRole("region", {
    name: `${name}'s browser logins`,
  })
  await expect(section).toContainText(loginName)
  await expect(section).toContainText("ada")
  await api.json("DELETE", `/api/environments/${env}/secrets/${login.id}`)
})

// Typing that the page can't be followed through, such as moving the caret, is not offered;
// Never for this site keeps the card away on this computer.
test("a password is not offered after an edit it can't follow, or once told never", async ({
  page,
  api,
  env,
}) => {
  const { host } = await signInPage(page, api, env)
  const card = page.getByRole("form", { name: "Save password" })
  await page.waitForTimeout(300)
  await clickPage(page, ...passwordField)
  await page.keyboard.type("abc")
  await page.keyboard.press("ArrowLeft")
  await page.keyboard.type("x")
  await clickPage(page, ...signInButton)
  await page.waitForTimeout(1000)
  await expect(card).toHaveCount(0)

  const address = page.getByLabel("Browser address")
  await address.fill(`https://${host}/login`)
  await address.press("Enter")
  await page.waitForTimeout(300)
  await clickPage(page, ...passwordField)
  await page.keyboard.type("secret")
  await page.keyboard.press("Enter")
  await card.getByRole("button", { name: "Never for this site" }).click()
  await expect(card).toBeHidden()

  await address.fill(`https://${host}/login`)
  await address.press("Enter")
  await page.waitForTimeout(300)
  await clickPage(page, ...passwordField)
  await page.keyboard.type("secret2")
  await page.keyboard.press("Enter")
  await page.waitForTimeout(1000)
  await expect(card).toHaveCount(0)
})

// A page asking for a username and password (HTTP Basic, which the gateway reports) says so
// over the page: Sign in saves a login, a refused login can be changed, and one held back from
// the agents can be given to them.
test("a page asking for a username and password signs in with a browser login", async ({
  page,
  api,
  env,
}) => {
  const { name, host, tm, logins } = await signInPage(page, api, env)
  const ask = (body: object) =>
    api.json("POST", "/api/e2e/signin", {
      environmentId: env,
      teammateId: tm,
      host,
      ...body,
    })
  await ask({ realm: "Staging" })
  const strip = page.getByRole("status", { name: "Sign in" })
  await expect(strip).toContainText(
    `${host} asks for a username and password for “Staging”.`
  )
  await strip.getByRole("button", { name: "Sign in…" }).click()
  await strip.getByLabel("Username").fill("ada")
  await strip.getByLabel("Password").fill("correct-horse")
  await strip.getByRole("button", { name: "Save and sign in" }).click()
  await expect.poll(async () => (await logins()).length).toBe(1)
  const [login] = await logins()
  expect(login).toMatchObject({ username: "ada", agentMayUse: false })

  // The page loaded: the strip goes.
  await ask({ realm: null })
  await expect(strip).toBeHidden()

  await ask({ realm: "Staging", login: login.name, refused: true })
  await expect(strip).toContainText(
    `${host} refused the login ${login.name} for “Staging”.`
  )
  await strip.getByRole("button", { name: "Change the password…" }).click()
  await expect(strip.getByLabel("Username")).toHaveValue("ada")
  await strip.getByLabel("Password").fill("battery-staple")
  await strip.getByRole("button", { name: "Save and retry" }).click()
  await expect
    .poll(
      async () =>
        (await api.json<{ value: string }>(
          "GET",
          `/api/environments/${env}/secrets/${login.id}/value`
        ))!.value
    )
    .toBe("battery-staple")

  await ask({ realm: "Staging", login: login.name, refused: false })
  await expect(strip).toContainText(
    `${name}'s agents may not use ${login.name} without asking`
  )
  await strip.getByRole("button", { name: "Let agents use it" }).click()
  await expect.poll(async () => (await logins())[0].agentMayUse).toBe(true)
  await api.json("DELETE", `/api/environments/${env}/secrets/${login.id}`)
})
