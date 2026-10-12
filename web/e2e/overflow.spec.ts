import type { Locator } from "@playwright/test"
import { approvals, expect, test } from "./fixtures"

/** Descendants of el that stick out of it, and el itself if it scrolls sideways or leaves the viewport. */
function overflowing(el: Locator) {
  return el.evaluate((root) => {
    const box = root.getBoundingClientRect()
    const out: string[] = []
    if (root.scrollWidth > root.clientWidth + 1)
      out.push(`scrollWidth ${root.scrollWidth} > ${root.clientWidth}`)
    if (box.left < 0 || box.right > window.innerWidth)
      out.push(`outside the viewport: ${box.left}–${box.right}`)
    for (const child of root.querySelectorAll("*")) {
      const r = child.getBoundingClientRect()
      // Visually hidden controls, such as the native input behind a checkbox, are 1px wide.
      if (r.width <= 1 && r.height <= 1) continue
      if (r.left < box.left - 1 || r.right > box.right + 1)
        out.push(
          `<${child.tagName.toLowerCase()} class="${child.getAttribute("class")}"> at ${r.left}–${r.right}, card ${box.left}–${box.right}`
        )
    }
    return out
  })
}

for (const width of [390, 1440]) {
  test(`a network approval with a long host fits its card at ${width}px`, async ({
    page,
    api,
    env,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await api.dismissApprovals()
    const suffix = Math.random().toString(36).slice(2, 8)
    // Names near their limits: a 40-character sandbox, labels of up to 63 characters.
    const sandbox = `a-sandbox-with-a-name-of-forty-ch-${suffix}`
    const host = `${"subdomain".repeat(6)}-${suffix}.${"label".repeat(12)}.example.com`
    const id = await api.createSandbox(sandbox)
    await api.connect(id, host)

    await page.goto(`/e/${env}`)
    const card = approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: host })
    await expect(card).toBeVisible()
    expect(await overflowing(card)).toEqual([])

    await card.getByRole("button", { name: "Options" }).click()
    await expect(card.getByRole("group", { name: "Scope" })).toBeVisible()
    expect(await overflowing(card)).toEqual([])

    await api.dismissApprovals()
  })
}
