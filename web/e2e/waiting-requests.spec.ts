import type { Page } from "@playwright/test"
import { approvals, expect, openApprovals, sandboxName, test } from "./fixtures"

function freshHost(label: string) {
  return `${label}-${Math.random().toString(36).slice(2, 8)}.example.org`
}

/** Pages see nothing held any more, as they would once the client's connection gave up. */
async function holdsEnded(page: Page) {
  await page.route(/\/api\/approvals\?/, async (route) => {
    const response = await route.fetch()
    const list = (await response.json()) as { heldUntil?: string }[]
    for (const a of list) delete a.heldUntil
    await route.fulfill({ response, json: list })
  })
}

test("a sandbox's held connections show as a count, answered there and in Connections", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("wait"))
  const hosts = [freshHost("one"), freshHost("two"), freshHost("three")]

  await page.goto(`/e/${env}/sandboxes/${id}`)
  for (const host of hosts) await api.connect(id, host)

  // A count on the sandbox, and nothing that opens by itself or floats over the page.
  const chip = page.getByRole("button", { name: "3 requests need you" })
  await expect(chip).toBeVisible()
  await expect(page.getByTestId("waiting-popover")).toBeHidden()
  await expect(approvals(page).getByTestId("approval-group")).toHaveCount(0)
  await expect(approvals(page).getByTestId("approval-card")).toHaveCount(0)

  await chip.click()
  const popover = page.getByTestId("waiting-popover")
  await expect(popover).toContainText("wants to connect")
  for (const host of hosts) await expect(popover).toContainText(`${host}:80`)
  await expect(popover).toContainText(/\d+ s left/)

  // Reviewing them is the list in Connections, with its own answers.
  await popover.getByRole("button", { name: "Review individually" }).click()
  const held = page.getByTestId("waiting-held")
  await expect(held).toContainText("Needs you · 3")
  const row = (host: string) =>
    held.getByTestId("waiting-request").filter({ hasText: host })
  await expect(row(hosts[0])).toContainText(/\d+ s left/)

  await row(hosts[0]).getByRole("button", { name: "Allow" }).click()
  await expect(held).toContainText("Needs you · 2")
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.find((c) => c.host === hosts[0])
    )
    .toMatchObject({ done: true, status: 200 })

  // What it allowed moved on to the log beneath them.
  await expect(page.getByRole("heading", { name: "Earlier" })).toBeVisible()

  // Options apply to what's answered next: the rule then covers this sandbox only.
  await held
    .getByRole("button", { name: /^Options: every computer, always/ })
    .click()
  await held
    .getByRole("group", { name: "Scope" })
    .locator("button")
    .first()
    .click()
  await held.getByRole("button", { name: "Allow all" }).click()
  await expect(held).toBeHidden()
  await expect
    .poll(
      async () => (await api.guest(id)).connections.filter((c) => c.done).length
    )
    .toBe(3)
  await expect(
    page.getByRole("button", { name: /requests? needs? you/ })
  ).toHaveCount(0)
})

test("the count's Allow all answers them, and the next request finds it closed", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("wait"))
  const hosts = [freshHost("first"), freshHost("second")]

  await page.goto(`/e/${env}/sandboxes/${id}`)
  for (const host of hosts) await api.connect(id, host)
  await page.getByRole("button", { name: "2 requests need you" }).click()
  const popover = page.getByTestId("waiting-popover")
  await expect(popover).toContainText("Each host gets a lasting rule")
  await popover.getByRole("button", { name: "Allow all" }).click()
  await expect(
    page.getByRole("button", { name: /requests? needs? you/ })
  ).toHaveCount(0)

  // Nothing opens by itself: the next request is a count again, closed.
  await api.connect(id, freshHost("later"))
  await expect(
    page.getByRole("button", { name: "1 request needs you" })
  ).toBeVisible()
  await expect(popover).toBeHidden()
  await api.dismissApprovals()
})

test("another page lists a sandbox's request in the top bar, with how long it is held", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("wait"))
  const host = freshHost("float")
  await page.goto(`/e/${env}/secrets`)
  await api.connect(id, host)

  const card = (await openApprovals(page))
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card.getByTestId("hold-status")).toContainText(
    /Held, \d+ s left/
  )
  await card.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(card).toBeHidden()
})

test("a connection that gave up is answered for the next attempt", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("gone"))
  const later = freshHost("later")
  const dismissed = freshHost("dismissed")
  await holdsEnded(page)
  await api.connect(id, later)
  await api.connect(id, dismissed)

  await page.goto(`/e/${env}/sandboxes/${id}`)
  const chip = page.getByRole("button", { name: "2 gave up waiting" })
  await chip.click()
  const popover = page.getByTestId("waiting-popover")
  await expect(popover).toContainText("Nothing holds these open any more")
  await popover.getByRole("button", { name: "Review individually" }).click()

  const gone = page.getByTestId("waiting-gone")
  await expect(gone).toContainText("Gave up waiting · 2")
  await expect(gone).toContainText("have the client try again")
  await expect(page.getByTestId("waiting-held")).toBeHidden()

  await gone
    .getByTestId("waiting-request")
    .filter({ hasText: dismissed })
    .getByRole("button", { name: "Ignore" })
    .click()
  await expect(gone).toContainText("Gave up waiting · 1")
  await gone
    .getByTestId("waiting-request")
    .filter({ hasText: later })
    .getByRole("button", { name: "Always allow" })
    .click()
  await expect(gone).toBeHidden()

  // The answer was a rule, and only the one it was meant for.
  await page.goto(`/e/${env}/network?q=later-`)
  await expect(
    page.getByRole("cell", { name: later, exact: true })
  ).toBeVisible()
})

test("a row in the top bar's list says when its connection gave up", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("gone"))
  const host = freshHost("card")
  await holdsEnded(page)
  await api.connect(id, host)
  await page.goto(`/e/${env}/secrets`)

  const card = (await openApprovals(page))
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card.getByTestId("hold-status")).toContainText("No longer held")
  await card.getByRole("button", { name: "Allow", exact: true }).click()
  await expect(card).toBeHidden()
})

/** A page that isn't in front, recording what it notifies. */
async function inTheBackground(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as { notices: unknown[] }
    w.notices = []
    class Recorded {
      static permission = "granted"
      static requestPermission = () => Promise.resolve("granted")
      onclick = null
      constructor(title: string, options: { body: string; tag: string }) {
        w.notices.push({ title, ...options })
      }
      close() {}
    }
    Object.defineProperty(window, "Notification", { value: Recorded })
    document.hasFocus = () => false
  })
}

const notices = (page: Page) =>
  page.evaluate(
    () => (window as unknown as { notices: Record<string, string>[] }).notices
  )

test("connections held while Studio isn't in front notify once per sandbox", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = sandboxName("away")
  const id = await api.createSandbox(name)
  await inTheBackground(page)
  await page.goto(`/e/${env}/secrets`)
  await expect(page.getByRole("heading").first()).toBeVisible()

  const hosts = [freshHost("a"), freshHost("b"), freshHost("c"), freshHost("d")]
  for (const host of hosts) await api.connect(id, host)
  await expect.poll(() => notices(page), { timeout: 10_000 }).toHaveLength(1)
  const [notice] = await notices(page)
  expect(notice.title).toBe(`${name} is waiting on a connection`)
  expect(notice.body).toMatch(/ and 1 more$/)
  expect(notice.tag).toBe(`connections-${id}`)
  await api.dismissApprovals()
})

test("Settings turns off the notification about held connections", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("quiet"))
  await inTheBackground(page)
  await page.goto(`/e/${env}/settings/notifications`)
  await page
    .getByRole("switch", { name: /Tell me when a connection is held/ })
    .click()

  await api.connect(id, freshHost("quiet"))
  await page.waitForTimeout(4000)
  expect(await notices(page)).toHaveLength(0)
  await api.dismissApprovals()
})

test("app: a notification's Allow all answers what it named, and nothing after", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = sandboxName("app")
  const id = await api.createSandbox(name)
  await page.addInitScript(() => {
    const posted: string[] = []
    Object.assign(window, {
      posted,
      webkit: {
        messageHandlers: {
          external: { postMessage: (m: string) => posted.push(m) },
        },
      },
    })
    document.hasFocus = () => false
  })
  const notified = async () => {
    const posted = await page.evaluate(
      () => (window as unknown as { posted: string[] }).posted
    )
    return posted
      .filter((m) => m.startsWith("studio:notify "))
      .map(
        (m) =>
          JSON.parse(m.slice("studio:notify ".length)) as {
            id: string
            title: string
            body: string
            allowAll?: boolean
          }
      )
  }
  await page.goto(`/e/${env}/secrets?studio-app=notify`)
  await expect(page.getByRole("heading").first()).toBeVisible()

  const hosts = [freshHost("named"), freshHost("named")]
  for (const host of hosts) await api.connect(id, host)
  await expect.poll(notified, { timeout: 10_000 }).toHaveLength(1)
  const [notice] = await notified()
  expect(notice).toMatchObject({
    id: `connections-${id}`,
    title: `${name} is waiting on a connection`,
    allowAll: true,
  })

  const after = freshHost("after")
  await api.connect(id, after)
  await expect(
    (await openApprovals(page)).getByText(`${after}:80`)
  ).toBeAttached()

  await page.evaluate(
    (detail) =>
      window.dispatchEvent(new CustomEvent("studio:allow-all", { detail })),
    notice.id
  )
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.filter((c) => c.done).map((c) => c.host)
    )
    .toEqual(expect.arrayContaining(hosts))
  const guest = await api.guest(id)
  expect(guest.connections.find((c) => c.host === after)?.done).toBeFalsy()
  await api.dismissApprovals()
})
