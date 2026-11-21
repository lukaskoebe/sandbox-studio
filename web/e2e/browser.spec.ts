import type { Page } from "@playwright/test"
import { addTeammate, expect, sandboxName, test, type Api } from "./fixtures"

type Pattern = { id: string; origin: string; action: string; verdict: string }

// Without a browser VM the window says so and offers to open a page; the rail it opens has the
// activity and the sites, whose rules are set in plain words.
test("the screen's browser tab shows the sleeping window, its activity and its sites", async ({
  page,
  api,
  env,
}) => {
  const name = `Browse ${Math.random().toString(36).slice(2, 8)}`
  const id = await addTeammate(api, env, name)
  await api.createSandbox(sandboxName("sites"), id)
  const patterns = `/api/environments/${env}/teammates/${id}/browser/patterns`
  await api.json("POST", patterns, {
    origin: "https://shop.example.com",
    action: "*",
    verdict: "allow",
  })

  await page.goto(`/e/${env}/teammates/${id}?tab=browser`)
  await expect(page.getByText(`${name} has no browser yet`)).toBeVisible()
  await expect(page.getByLabel("Open a page")).toBeVisible()
  await page.getByRole("button", { name: "Activity and sites" }).click()
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

// A teammate's computer, at its own address or its old workspace's, is the screen on the
// teammate's page; the workspace's Browser panel is the screen's Browser tab.
test("a teammate's computer opens on the teammate's page, on the tab its link names", async ({
  page,
  api,
  env,
}) => {
  const name = `Panel ${Math.random().toString(36).slice(2, 8)}`
  const tm = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("browse"), tm)
  await page.goto(`/e/${env}/sandboxes/${id}/work?panel=browser`)
  await expect(page).toHaveURL(new RegExp(`/teammates/${tm}\\?`))
  expect(new URL(page.url()).searchParams.get("computer")).toBe(id)
  expect(new URL(page.url()).searchParams.get("tab")).toBe("browser")
  await expect(page.getByRole("tab", { name: /^Browser/ })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  await expect(page.getByText(`${name} has no browser yet`)).toBeVisible()
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
    sessions: {
      slot: number
      url: string
      title: string
      viewport: [number, number]
      scale: number
      files?: string[]
      dropped?: string[]
      dropAt?: [number, number]
    }[]
    inputs: {
      type: string
      eventType?: string
      x?: number
      y?: number
      key?: string
      text?: string
    }[]
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
  await page.goto(`/e/${env}/teammates/${tm}?tab=browser`)
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
  await expect(page.getByTitle(`${name} isn't using the browser`)).toBeVisible()

  const address = page.getByLabel("Browser address")
  await address.fill("example.com/a")
  await expect(page.getByText(`↵ takes the wheel from ${name}`)).toBeVisible()
  await address.press("Enter")
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()
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
  await expect(address).toHaveValue("example.com/a")

  await page.getByRole("button", { name: `Hand back to ${name}` }).click()
  await expect(page.getByTitle(`${name} isn't using the browser`)).toBeVisible()
  expect((await api.json<Status>("GET", base))!.takeover).toBeFalsy()
})

// localhost typed into the address bar opens over http, on the computer whose screen shows
// the browser, with the ports it listens on now: no agent has to have used the browser.
test("localhost in the address bar opens the computer's dev server", async ({
  page,
  api,
  env,
}) => {
  const { tm, sb, base } = await browsing(page, api, env)
  await api.json("PUT", `/api/e2e/sandboxes/${sb}/ports`, { ports: [3000] })
  const address = page.getByLabel("Browser address")
  await address.fill("localhost:3000/app")
  await address.press("Enter")
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("http://localhost:3000/app")
  const title = async () =>
    (await api.json<FakeBrowser>(
      "GET",
      `/api/e2e/teammates/${tm}/browser`
    ))!.browser!.sessions.find((s) => s.slot === 0)!.title
  await expect
    .poll(title)
    .toBe(`Preview of sandbox ${sb} on port 3000, path /app`)
  await expect(address).toHaveValue("http://localhost:3000/app")
})

// A sleeping browser picks up where it left off: its last pages, newest first, each wake it
// on that page, and the logins it keeps show as chips.
test("the sleeping browser lists its last pages and wakes on one", async ({
  page,
  api,
  env,
}) => {
  const { name, tm, base } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  // A long address truncates in its row rather than widening the card.
  const long = `b/${"z-long-segment-with-no-break".repeat(8)}?q=1`
  for (const path of ["a", long]) {
    await address.fill(`example.com/${path}`)
    await address.press("Enter")
    await expect
      .poll(async () => (await api.json<Status>("GET", base))!.url)
      .toBe(`https://example.com/${path}`)
  }
  await api.json("POST", `/api/environments/${env}/secrets`, {
    name: `SHOP_${Math.random().toString(36).slice(2, 8).toUpperCase()}`,
    value: "hunter2",
    hosts: ["shop.example.com"],
    teammateId: tm,
    browserOnly: true,
    username: "ada",
  })
  await api.json("POST", `${base}/stop`)

  const card = page.getByRole("region", { name: `${name}'s browser` })
  await expect(card.getByText(/^Asleep since /)).toBeVisible()
  const pages = card.getByRole("list", { name: "Last pages" })
  await expect(pages.getByRole("button")).toHaveText([
    /example\.com\/b/,
    /example\.com\/a/,
  ])
  expect(
    await card.evaluate((el) => el.scrollWidth - el.clientWidth)
  ).toBeLessThanOrEqual(0)
  await expect(
    card.getByRole("button", { name: "Wake on example.com" })
  ).toBeVisible()
  await expect(card.getByLabel("Logins it keeps")).toContainText(
    "shop.example.com"
  )

  await pages.getByRole("button", { name: /\/a/ }).click()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.vm)
    .toBe("running")
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/a")
  await expect(card).toBeHidden()

  // Asleep, the address bar still takes an address, and wakes the browser on it.
  await api.json("POST", `${base}/stop`)
  await expect(card).toBeVisible()
  await expect(address).toHaveAttribute("placeholder", "Asleep")
  await address.fill("example.com/c")
  await address.press("Enter")
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.url)
    .toBe("https://example.com/c")
  await expect(card).toBeHidden()
})

// A page whose host a deny rule refuses says so over the live view, and links to the rule,
// which the network settings open.
test("a page a deny rule blocks says so, with a link to the rule", async ({
  page,
  api,
  env,
}) => {
  const host = `shop-${Math.random().toString(36).slice(2, 8)}.test`
  const rule = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/rules`,
    { host: `*.${host}`, action: "deny" }
  ))!
  try {
    const { base } = await browsing(page, api, env)
    const address = page.getByLabel("Browser address")
    await address.fill(`https://www.${host}/cart`)
    await address.press("Enter")
    await expect
      .poll(async () => (await api.json<Status>("GET", base))!.url)
      .toBe(`https://www.${host}/cart`)
    const notice = page
      .getByRole("status")
      .filter({ hasText: "A network rule blocks" })
    await expect(notice).toContainText(`www.${host}: deny *.${host}`)

    // Another page leaves the notice behind.
    await address.fill("https://example.com/a")
    await address.press("Enter")
    await expect(notice).toBeHidden()
    await page.getByRole("button", { name: "Back", exact: true }).click()
    await expect(notice).toBeVisible()

    await notice.getByRole("link", { name: "Open the rule" }).click()
    const dialog = page.getByRole("dialog", { name: "Edit rule" })
    await expect(dialog).toBeVisible()
    await expect(
      dialog.getByRole("textbox", { name: "Host", exact: true })
    ).toHaveValue(`*.${host}`)
    await expect(page).toHaveURL(/\/e\/[^/]+\/network$/)
  } finally {
    await api.json("DELETE", `/api/environments/${env}/rules/${rule.id}`)
  }
})

// While the user drives, an agent's call is paused and says so, and browser_request_control
// asks on the page: Keep driving keeps the wheel, Hand back returns it and answers the call.
test("an agent asks for the browser back while the user drives", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const { name, sb, base, call } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("example.com/a")
  await address.press("Enter")
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()

  const state = (id: number) =>
    api.json<Call>("GET", `/api/e2e/sandboxes/${sb}/calls/${id}`)
  const ask = page.getByRole("alertdialog", { name: `${name} is asking` })
  const request = () =>
    call("browser_request_control", { reason: "to finish the order" }, false)

  let req = (await request())!
  await expect(ask.getByText(/to finish the order/)).toBeVisible()
  await ask.getByRole("button", { name: "Keep driving" }).click()
  await expect(ask).toBeHidden()
  await expect.poll(async () => (await state(req.id))!.done).toBe(true)
  expect(JSON.stringify((await state(req.id))!.result)).toContain(
    "keeps control"
  )
  expect((await api.json<Status>("GET", base))!.takeover).toBe(true)

  req = (await request())!
  await ask.getByRole("button", { name: "Hand back" }).click()
  await expect(page.getByTitle(`${name} isn't using the browser`)).toBeVisible()
  await expect.poll(async () => (await state(req.id))!.done).toBe(true)
  expect(JSON.stringify((await state(req.id))!.result)).toContain(
    "handed the browser back"
  )
})

// browser_show posts a card and returns at once: Watch opens the teammate's Browser tab and
// clears the card, Take the wheel also hands the user control.
test("an agent shows the user a page, to watch or to take the wheel", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const { name, tm, base, call } = await browsing(page, api, env)
  await page.goto(`/e/${env}`)
  const shown = (await call("browser_show", {
    url: "https://example.com/login",
    title: "Login page",
    note: "Try a wrong password",
  }))!
  expect(JSON.stringify(shown.result)).toContain(
    "Showing https://example.com/login"
  )
  const card = page
    .getByTestId("approval-card")
    .filter({ hasText: `${name} wants to show you: Login page` })
  await expect(card.getByText("Try a wrong password")).toBeVisible()
  await card.getByRole("button", { name: "Watch" }).click()
  await expect(page).toHaveURL(new RegExp(`/teammates/${tm}\\?tab=browser$`))
  await expect(card).toHaveCount(0)
  await expect(page.getByLabel("Browser address")).toHaveValue(
    "example.com/login"
  )
  expect((await api.json<Status>("GET", base))!.takeover).toBeFalsy()

  // On the teammate's page the request waits on its home, and Answer opens its card there.
  await call("browser_show", { url: "https://example.com/a" })
  await page
    .getByRole("region", { name: "Waiting for you" })
    .getByRole("button", { name: "Answer" })
    .click()
  await page
    .getByTestId("approval-card")
    .filter({ hasText: `${name} wants to show you: https://example.com/a` })
    .getByRole("button", { name: "Take the wheel" })
    .click()
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()
  expect((await api.json<Status>("GET", base))!.takeover).toBe(true)
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
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()

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

// A page that asks for files gets the ones picked on this computer, from the card over the
// live view, as paths in the browser's VM; files dropped on the frame reach the page where
// they land; and neither happens while the teammate drives.
test("the page asks for files, and files dropped on it reach it", async ({
  page,
  api,
  env,
}) => {
  const { name, tm } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/upload")
  await address.press("Enter")
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()
  const canvas = page.getByLabel("Live view of the browser")
  await expect(canvas).toBeVisible()
  await page.waitForTimeout(300) // the frame of the new page
  // Frames are 640x400 CSS pixels; the file input spans (40,110)-(240,146), and the drop
  // zone (40,170)-(600,330).
  const box = (await canvas.boundingBox())!
  const at = (x: number, y: number) => ({
    x: box.x + (x / 640) * box.width,
    y: box.y + (y / 400) * box.height,
  })
  const session = async () =>
    (await api.json<FakeBrowser>(
      "GET",
      `/api/e2e/teammates/${tm}/browser`
    ))!.browser!.sessions.find((s) => s.slot === 0)!

  const card = page.getByRole("dialog", { name: "The page asks for files" })
  // Studio watches the page from when the view opens; a click before that goes unheard.
  await expect(async () => {
    const input = at(140, 128)
    await page.mouse.click(input.x, input.y)
    await expect(card).toBeVisible({ timeout: 1000 })
  }).toPass()
  await expect(card).toContainText(`to ${name}'s browser`)
  const chooser = page.waitForEvent("filechooser")
  await card.getByRole("button", { name: "Choose files…" }).click()
  const picker = await chooser
  expect(picker.isMultiple()).toBe(true)
  await picker.setFiles([
    { name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("hi") },
    { name: "photo.png", mimeType: "image/png", buffer: Buffer.from("png") },
  ])
  await expect(page.getByText("Gave the page 2 files")).toBeVisible()
  await expect(card).toHaveCount(0)
  await expect
    .poll(async () => (await session()).files)
    .toEqual(["/tmp/studio-pastes/notes.txt", "/tmp/studio-pastes/photo.png"])

  // A click and Cancel: the page gets nothing, and the card goes.
  const input = at(140, 128)
  await page.mouse.click(input.x, input.y)
  await expect(card).toBeVisible()
  await card.getByRole("button", { name: "Cancel" }).click()
  await expect(card).toHaveCount(0)

  // Files dragged over the frame; dropped, they reach the page at that point.
  const drag = async (types: string[]) => {
    const point = at(300, 250)
    await canvas.evaluate(
      (el, ev) => {
        const data = new DataTransfer()
        data.items.add(
          new File(["report"], "report.pdf", { type: "application/pdf" })
        )
        for (const type of ev.types)
          el.dispatchEvent(
            new DragEvent(type, {
              bubbles: true,
              cancelable: true,
              clientX: ev.point.x,
              clientY: ev.point.y,
              dataTransfer: data,
            })
          )
      },
      { point, types }
    )
  }
  await drag(["dragenter", "dragover"])
  await expect(
    page.getByText(`Drop to give the page these files, in ${name}'s browser`)
  ).toBeVisible()
  await drag(["drop"])
  await expect(page.getByText("Dropped report.pdf")).toBeVisible()
  const dropped = await expect
    .poll(async () => (await session()).dropped)
    .toEqual(["/tmp/studio-pastes/report.pdf"])
    .then(session)
  expect(Math.abs(dropped.dropAt![0] - 300)).toBeLessThanOrEqual(2)
  expect(Math.abs(dropped.dropAt![1] - 250)).toBeLessThanOrEqual(2)

  // Handed back, the frame takes no files.
  await page.getByRole("button", { name: `Hand back to ${name}` }).click()
  await expect(page.getByTitle(`${name} isn't using the browser`)).toBeVisible()
  await drag(["dragenter", "dragover"])
  await expect(
    page.getByText("Take the wheel to drop files on the page")
  ).toBeVisible()
  await drag(["drop"])
  await expect(page.getByText("Dropped report.pdf")).toHaveCount(0)
})

test("the live view shows the page's cursor under the mouse while you drive", async ({
  page,
  api,
  env,
}) => {
  await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()

  const canvas = page.getByLabel("Live view of the browser")
  await expect(canvas).toBeVisible()
  await page.waitForTimeout(300) // the frame of the new page
  const box = (await canvas.boundingBox())!
  const hover = (x: number, y: number) =>
    page.mouse.move(
      box.x + (x / 640) * box.width,
      box.y + (y / 400) * box.height,
      { steps: 3 }
    )
  const cursor = () => canvas.evaluate((el) => getComputedStyle(el).cursor)
  // "Email" spans (40,110)-(600,146) and "Sign in" (40,286)-(200,326).
  await hover(120, 128)
  await expect.poll(cursor).toBe("text")
  await hover(120, 306)
  await expect.poll(cursor).toBe("pointer")
  await hover(620, 30)
  await expect.poll(cursor).toBe("default")

  // A wheel over the page scrolls the page in the VM, and nothing else: on a Mac the view
  // around it would bounce, as it can't scroll.
  const kept = await canvas.evaluate(
    (el) =>
      !el.dispatchEvent(
        new WheelEvent("wheel", { deltaY: 40, bubbles: true, cancelable: true })
      )
  )
  expect(kept).toBe(true)
  expect(
    await page.evaluate(
      () => getComputedStyle(document.documentElement).overscrollBehaviorY
    )
  ).toBe("none")
})

// DevTools open under the page, on a host of their own that reaches the page's socket and
// nothing else of the browser; the header breaks them out into a window, and they end when the
// browser goes to sleep.
test("DevTools open under the page and break out into a window", async ({
  page,
  api,
  env,
}) => {
  const { base } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  await expect(page.getByLabel("Live view of the browser")).toBeVisible()

  const options = page.getByRole("button", { name: "Browser options" })
  await options.click()
  await page.getByRole("menuitemcheckbox", { name: "DevTools" }).click()
  const panel = page.getByRole("region", { name: "DevTools" })
  await expect(panel).toBeVisible()
  const iframe = panel.locator("iframe")
  const src = (await iframe.getAttribute("src"))!
  expect(src).toMatch(/^http:\/\/devtools-[0-9a-f]{32}\.localhost:\d+\/$/)
  // The fake frontend asks the page's socket which page it is, as DevTools do.
  const devtools = iframe.contentFrame()
  await expect(devtools.locator("#state")).toHaveText(
    /^Inspecting .+ at https:\/\/example\.com\/login/
  )
  const frame = (await (await iframe.elementHandle()).contentFrame())!
  const status = (path: string) =>
    frame.evaluate((p) => fetch(p).then((r) => r.status), path)
  expect(await status("/json/list")).toBe(404)
  expect(await status("/devtools/browser/x")).toBe(404)

  // Opening again, as after a reload, is the same DevTools.
  const again = await api.json<{ url: string }>("POST", `${base}/devtools`)
  expect(again!.url).toBe(src)

  // Out into a window, which the panel leaves to.
  const popup = page.waitForEvent("popup")
  await panel.getByRole("button", { name: "Open DevTools in a window" }).click()
  const win = await popup
  await expect(win.locator("#state")).toHaveText(/^Inspecting /)
  expect(new URL(win.url()).host).toBe(new URL(src).host)
  await expect(panel).toBeHidden()

  // The browser's sleep ends them.
  await api.json("POST", `${base}/stop`)
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.vm)
    .not.toBe("running")
  await win.reload()
  await expect(win.getByRole("heading")).toHaveText("These DevTools ended")
})

// Fit to this view gives the page the size of the view's body, one pixel to one, and follows
// the window; Standard puts the page back to agent-browser's own size.
test("the page can be fitted to the live view, and follows its size", async ({
  page,
  api,
  env,
}) => {
  const { tm } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  const canvas = page.getByLabel("Live view of the browser")
  await expect(canvas).toHaveAttribute("width", "640")
  const viewport = async () =>
    (await api.json<FakeBrowser>(
      "GET",
      `/api/e2e/teammates/${tm}/browser`
    ))!.browser!.sessions.find((s) => s.slot === 0)!.viewport
  const room = async () => {
    const box = (await page.getByTestId("browser-view").boundingBox())!
    return [Math.floor(box.width), Math.floor(box.height)]
  }
  const sizes = page.getByRole("button", { name: /^Page size/ })
  const fit = page.getByRole("menuitemradio", { name: "Fit to this view" })

  await expect(sizes).toHaveAccessibleName("Page size: Standard size, 1280×720")
  await sizes.click()
  await expect(fit).toHaveAttribute("aria-checked", "false")
  await fit.click()
  await expect(sizes).toHaveAccessibleName("Page size: Fits this view")
  await expect.poll(viewport).toEqual(await room())
  const [width] = await room()
  await expect(canvas).toHaveAttribute("width", String(width))
  // At one pixel to one, the frame is as wide as the room.
  expect(Math.round((await canvas.boundingBox())!.width)).toBe(width)

  await page.setViewportSize({ width: 1100, height: 700 })
  await expect.poll(viewport).toEqual(await room())
  expect((await room())[0]).toBeLessThan(width)

  // The choice is this computer's: it holds after a reload.
  await page.reload()
  await sizes.click()
  await expect(fit).toHaveAttribute("aria-checked", "true")
  await page.getByRole("menuitemradio", { name: /^Standard/ }).click()
  await expect.poll(viewport).toEqual([1280, 720])
  await expect(canvas).toHaveAttribute("width", "1280")
})

// A device's size is the session's, not this view's: the page keeps it while the window changes,
// and the view scales it to fit. An agent's `studio browser viewport` shows the same way, with
// its name, and choosing a size here replaces it.
test("a phone's or a laptop's size stays for the session, and so does an agent's", async ({
  page,
  api,
  env,
}) => {
  const { tm, base, call } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  const canvas = page.getByLabel("Live view of the browser")
  await expect(canvas).toHaveAttribute("width", "640")
  const viewport = async () =>
    (await api.json<FakeBrowser>(
      "GET",
      `/api/e2e/teammates/${tm}/browser`
    ))!.browser!.sessions.find((s) => s.slot === 0)!.viewport
  const kept = async () =>
    (await api.json<Status & { viewport?: object }>("GET", base))!.viewport
  const sizes = page.getByRole("button", { name: /^Page size/ })

  await sizes.click()
  await page.getByRole("menuitemradio", { name: /^Phone/ }).click()
  await expect(sizes).toHaveAccessibleName("Page size: Phone size, 390×844")
  await expect.poll(viewport).toEqual([390, 844])
  await expect.poll(kept).toEqual({ width: 390, height: 844 })
  await expect(canvas).toHaveAttribute("width", "390")
  // The page is taller than the view, which scales it down to show it whole.
  const room = (await page.getByTestId("browser-view").boundingBox())!
  const frame = (await canvas.boundingBox())!
  expect(frame.height).toBeLessThanOrEqual(room.height + 1)
  await page.setViewportSize({ width: 1100, height: 700 })
  await page.waitForTimeout(800)
  expect(await viewport()).toEqual([390, 844])

  // Fit waits while a size is kept, and takes over once it goes.
  await sizes.click()
  await page.getByRole("menuitemradio", { name: "Fit to this view" }).click()
  await expect.poll(kept).toBeUndefined()
  await expect.poll(viewport).not.toEqual([1280, 720])
  await expect.poll(viewport).not.toEqual([390, 844])

  await page.getByRole("button", { name: "Hand back" }).click()
  const set = await call("browser_viewport", { size: "1024x700" })
  expect(set!.error).toBeUndefined()
  expect(String(set!.result)).toContain("1024×700")
  await expect.poll(viewport).toEqual([1024, 700])
  await expect(sizes).toHaveAccessibleName(
    /Page size: Page size, 1024×700, chosen by /
  )
  await sizes.click()
  await expect(
    page.getByRole("menuitemradio", { name: /'s size 1024×700$/ })
  ).toHaveAttribute("aria-checked", "true")
  await page.getByRole("menuitemradio", { name: /^Laptop/ }).click()
  await expect.poll(viewport).toEqual([1440, 900])
  await expect.poll(kept).toEqual({ width: 1440, height: 900 })
  await expect(sizes).toHaveAccessibleName("Page size: Laptop size, 1440×900")
})

// A paste on the live view types this computer's clipboard into the page, once the user has
// read what the teammate sees of it; Ctrl+V doesn't reach the remote browser as a key.
// On a screen of two device pixels to a CSS pixel, the fitted page is at 2x, so its frames have
// a pixel for each of the screen's and the view isn't blurred; clicks still land in CSS pixels.
test.describe("at 2x", () => {
  test.use({ deviceScaleFactor: 2 })

  test("a fitted page has the screen's scale, and frames as sharp as it", async ({
    page,
    api,
    env,
  }) => {
    const { tm } = await browsing(page, api, env)
    const address = page.getByLabel("Browser address")
    await address.fill("https://example.com/login")
    await address.press("Enter")
    const canvas = page.getByLabel("Live view of the browser")
    await expect(canvas).toHaveAttribute("width", "640")
    const session = async () =>
      (await api.json<FakeBrowser>(
        "GET",
        `/api/e2e/teammates/${tm}/browser`
      ))!.browser!.sessions.find((s) => s.slot === 0)!
    const box = (await page.getByTestId("browser-view").boundingBox())!
    const room = [Math.floor(box.width), Math.floor(box.height)]

    await page.getByRole("button", { name: /^Page size/ }).click()
    await page.getByRole("menuitemradio", { name: "Fit to this view" }).click()
    await expect
      .poll(async () => {
        const s = await session()
        return [...s.viewport, s.scale]
      })
      .toEqual([...room, 2])
    // The canvas holds twice the page's pixels, shown at the page's size in CSS pixels.
    await expect(canvas).toHaveAttribute("width", String(room[0] * 2))
    expect(Math.round((await canvas.boundingBox())!.width)).toBe(room[0])
  })
})

test("a paste into the live view types the clipboard, after a warning", async ({
  page,
  api,
  env,
}) => {
  const { name, tm } = await browsing(page, api, env)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/login")
  await address.press("Enter")
  await expect(page.getByTitle(/^You're driving/)).toBeVisible()
  const canvas = page.getByLabel("Live view of the browser")
  await canvas.focus()
  await page.keyboard.press("ControlOrMeta+v")

  const paste = (text: string) =>
    canvas.evaluate((el, text) => {
      const data = new DataTransfer()
      data.setData("text/plain", text)
      el.dispatchEvent(
        new ClipboardEvent("paste", {
          clipboardData: data,
          bubbles: true,
          cancelable: true,
        })
      )
    }, text)
  const typed = async () => {
    const state = (await api.json<FakeBrowser>(
      "GET",
      `/api/e2e/teammates/${tm}/browser`
    ))!
    return state
      .browser!.inputs.filter(
        (i) => i.type === "input_keyboard" && i.eventType === "char"
      )
      .map((i) => i.text)
      .join("")
  }
  await paste("hunter2")
  const warning = page.getByRole("alertdialog", {
    name: `Paste into ${name}'s browser?`,
  })
  await expect(warning).toContainText("The 7 characters on your clipboard")
  await expect(warning).toContainText(
    `In a password field, ${name} can't read it`
  )
  await warning
    .getByRole("checkbox", { name: "Don't ask again in this browser" })
    .check()
  await warning.getByRole("button", { name: "Paste" }).click()
  await expect.poll(typed).toBe("hunter2")

  // Told not to ask, the next paste goes straight in.
  await paste("!")
  await expect.poll(typed).toBe("hunter2!")
  await expect(warning).toHaveCount(0)
  const state = (await api.json<FakeBrowser>(
    "GET",
    `/api/e2e/teammates/${tm}/browser`
  ))!
  expect(
    state.browser!.inputs.filter((i) => i.key?.toLowerCase() === "v")
  ).toEqual([])
})

// The teammate's question shows on the page, not also in the top bar's list; once allowed, the
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
  await expect(page.getByTitle(`${name} is waiting for you`)).toBeVisible()
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
  await page.getByRole("button", { name: "Activity and sites" }).click()
  const step = page
    .getByRole("complementary", { name: "Activity and sites" })
    .getByRole("button", { name: /clicked “Sign in”$/ })
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
  // The toolbar says whose it is in short; the whole of it is the title.
  await expect(page.getByTitle(/uses this throwaway session/)).toBeVisible()
  await page.getByRole("button", { name: "End the throwaway session" }).click()
  await expect(tab).toBeHidden()
  await expect
    .poll(async () => (await api.json<Status>("GET", base))!.sessions ?? [])
    .toEqual([])
})

test("a tab with a long host keeps its label inside the tab", async ({
  page,
  api,
  env,
}) => {
  const { call } = await browsing(page, api, env)
  const host = `${"subdomain".repeat(5)}.${"label".repeat(10)}.example.org`
  await call("browser_open", { url: `https://${host}/`, ephemeral: true })
  const tab = page.getByRole("tab", { name: host })
  await expect(tab).toBeVisible()
  const sticking = await tab.evaluate((button) => {
    const box = button.parentElement!.getBoundingClientRect()
    return [...button.querySelectorAll("span")]
      .map((s) => s.getBoundingClientRect())
      .filter((r) => r.right > box.right + 1).length
  })
  expect(sticking).toBe(0)
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

  await page.goto(`/e/${env}/teammates/${tm}?profile=settings&open=browser`)
  const stealth = page.getByRole("switch", { name: "Stealth mode" })
  await expect(stealth).not.toBeChecked()
  await stealth.click()
  await expect(
    page.getByRole("button", { name: "Browser Stealth mode on" })
  ).toBeVisible()
  await expect(stealth).toBeChecked()

  await call("browser_snapshot", {})
  expect((await fake())!.browser!.stealth).toBe(true)
})
