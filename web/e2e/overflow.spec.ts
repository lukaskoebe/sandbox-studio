import type { Locator } from "@playwright/test"
import { addTeammate, approvals, expect, sandboxName, test } from "./fixtures"

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

// Home answers its environment's approvals in a section of its own; every other page shows
// them in the floating stack.
for (const [width, path] of [
  [390, ""],
  [1440, ""],
  [390, "/chats"],
  [1440, "/chats"],
] as const)
  test(`a network approval with a long host fits its card at ${width}px on ${path || "Home"}`, async ({
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

    await page.goto(`/e/${env}${path}`)
    // Below sm (640 px) the stack folds into its pill, which opens the card.
    if (path && width < 640)
      await approvals(page)
        .getByRole("button", { name: "1 approval", exact: true })
        .click()
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

test("a browser request's Options fit its card on a phone", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 390, height: 900 })
  await api.dismissApprovals()
  const suffix = Math.random().toString(36).slice(2, 8)
  const teammate = await addTeammate(api, env, `a-teammate-${suffix}`)
  const id = await api.createSandbox(sandboxName("browser"), teammate)
  const host = `challenges-${suffix}.example.com`
  void api
    .json("POST", "/api/e2e/requests", {
      sandboxId: id,
      host,
      port: 443,
      method: "GET",
      path: "/turnstile/v0/b/d2d3316b729e/api.js",
    })
    .catch(() => {})

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await card.getByRole("button", { name: "Options", exact: true }).click()
  await expect(card.getByRole("group", { name: "Who" })).toBeVisible()
  expect(await overflowing(card)).toEqual([])
  // The session's longer button label and its hint fit too.
  await card.getByRole("button", { name: "This session", exact: true }).click()
  await expect(
    card.getByRole("button", { name: "Allow for session", exact: true })
  ).toBeVisible()
  expect(await overflowing(card)).toEqual([])
  await api.dismissApprovals()
})

test("the setup card on Home and the setup fit a phone", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 390, height: 900 })
  // The card shows until the setup is dismissed; the fixtures dismiss it.
  await api.json("PUT", `/api/setup?env=${env}`, {
    steps: {},
    dismissed: false,
  })
  try {
    await page.goto(`/e/${env}`)
    const card = page.getByRole("region", { name: "Finish setup" })
    await expect(card).toBeVisible()
    expect(await overflowing(card)).toEqual([])

    await page.goto("/setup")
    await expect(
      page.getByRole("heading", { name: "Set up Studio" })
    ).toBeVisible()
    expect(await overflowing(page.getByRole("main"))).toEqual([])
  } finally {
    await api.json("PUT", `/api/setup?env=${env}`, {
      steps: {},
      dismissed: true,
    })
  }
})
