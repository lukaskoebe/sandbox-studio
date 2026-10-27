import type { Page } from "@playwright/test"
import { addTeammate, expect, sandboxName, test } from "./fixtures"

/** The type scale of styles.css, in px; each style's section label size is in it too. */
const scale = new Set([10, 10.5, 11, 12, 13, 14])

/**
 * The visible text on the page whose size is off the scale. Avatars set their initials to
 * fit, and the terminal has its own font size.
 */
async function offScale(page: Page) {
  await page.waitForLoadState("networkidle")
  return page.evaluate(
    (allowed) => {
      const off: string[] = []
      const walk = document.createTreeWalker(
        document.body,
        NodeFilter.SHOW_TEXT
      )
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const el = n.parentElement
        const text = n.textContent?.trim()
        if (!el || !text) continue
        if (el.closest('[data-slot="avatar"], .xterm')) continue
        const r = el.getBoundingClientRect()
        if (r.width <= 1 || r.height <= 1) continue
        const size = parseFloat(getComputedStyle(el).fontSize)
        if (!allowed.includes(size))
          off.push(`${size}px “${text.slice(0, 40)}” (${el.className})`)
      }
      return [...new Set(off)]
    },
    [...scale]
  )
}

test("every page's text is on the type scale, in both styles", async ({
  page,
  api,
  env,
}) => {
  test.setTimeout(120_000)
  await api.dismissApprovals()
  const teammate = await addTeammate(
    api,
    env,
    `Type ${Math.random().toString(36).slice(2, 6)}`
  )
  const id = await api.createSandbox(sandboxName("type"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  // A turn that waits for a permission, and a message queued behind it.
  await page.goto(`/e/${env}/chats/${chat.id}`)
  const box = page.getByRole("main").getByRole("textbox", { name: "Message" })
  await box.fill("Please run the tests")
  await box.press("Enter")
  await expect(page.getByText("asks before going on").first()).toBeVisible()
  await box.fill("Then fix the lint")
  await box.press("Enter")
  // It reads at the size of the messages, not bigger.
  const queued = page
    .getByRole("region", { name: "Queued messages" })
    .getByText("Then fix the lint")
  await expect(queued).toHaveCSS("font-size", "12px")
  // A size of the scale beside a text color: class merging keeps both.
  await expect(page.locator('[data-status="waiting"]').first()).toHaveCSS(
    "font-size",
    "10px"
  )

  for (const style of ["pixel", "quiet"]) {
    await page.evaluate((s) => {
      if (s === "quiet") localStorage.setItem("studio.style", "quiet")
      else localStorage.removeItem("studio.style")
    }, style)
    for (const path of [
      "",
      `/chats/${chat.id}`,
      `/teammates/${teammate}`,
      `/sandboxes/${id}`,
      "/chats",
      "/memory",
      "/settings/general",
      "/settings/network",
      "/settings/git",
    ]) {
      await page.goto(`/e/${env}${path}`)
      expect(await offScale(page), `${style} ${path || "Home"}`).toEqual([])
    }
  }
})

test("Home's notices share one look", async ({ page, env }) => {
  // The notifications notice shows while the browser hasn't been asked; Chrome's headless
  // shell, which CI runs, says "denied" instead. The previews notice shows until another
  // test answers it.
  await page.addInitScript(() =>
    Object.defineProperty(Notification, "permission", {
      get: () => "default",
      configurable: true,
    })
  )
  await page.goto(`/e/${env}`)
  const notices = page.locator('[data-slot="notice"]')
  await expect(notices.first()).toBeVisible()
  const looks = await notices.evaluateAll((els) =>
    els.map((el) => {
      const cs = getComputedStyle(el)
      const text = getComputedStyle(el.querySelector("p")!)
      return `${cs.paddingTop} ${cs.paddingLeft} ${text.fontSize}/${text.lineHeight}`
    })
  )
  expect(new Set(looks).size, looks.join(", ")).toBe(1)
  expect(looks[0]).toBe("12px 16px 12px/19.5px")
})
