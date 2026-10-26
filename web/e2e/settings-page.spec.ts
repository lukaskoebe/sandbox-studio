import type { Page } from "@playwright/test"
import { expect, sandboxName, test, type Api } from "./fixtures"

/** The heading row of a domain with several rules: its name, then how many sit under it. */
function groupRow(page: Page, domain: string, count: number) {
  const name = new RegExp(`^${domain.replace(/\./g, "\\.")}\\s*${count}$`)
  return page.getByRole("cell", { name })
}

/** Rules under two domains, one scoped to a sandbox, created through the API. */
async function seedRules(api: Api, env: string, sandboxId: string) {
  const suffix = Math.random().toString(36).slice(2, 8)
  const domain = `e2e-${suffix}.org`
  const other = `other-${suffix}.net`
  const rules = {
    domain,
    other,
    api: `api.${domain}`,
    cdn: `cdn.${domain}`,
    www: `www.${other}`,
  }
  const post = (body: object) =>
    api.json("POST", `/api/environments/${env}/rules`, body)
  await post({ host: rules.api, action: "allow", ports: [443] })
  await post({ host: rules.cdn, action: "deny", sandboxId })
  await post({ host: rules.www, action: "allow" })
  return rules
}

test("the settings nav opens sections by URL, by click and by search", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/settings/general$`))
  const nav = page.getByRole("navigation", { name: "Settings" })
  await expect(nav.getByRole("link", { name: "General" })).toHaveAttribute(
    "aria-current",
    "page"
  )

  await nav.getByRole("link", { name: "Network", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/settings/network$`))
  await expect(
    page.getByRole("heading", { name: "Rules", exact: true })
  ).toBeVisible()

  await nav.getByRole("link", { name: "Activity", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/settings/activity$`))
  await page.getByRole("link", { name: "Git", exact: true }).click()
  await expect(page).toHaveURL(/[?&]kind=git/)
  await expect(
    page.getByRole("link", { name: "Git", exact: true })
  ).toHaveAttribute("aria-pressed", "true")

  // The search finds a section by what it holds, not only by its name.
  await page.getByRole("searchbox", { name: "Search settings" }).fill("gitlab")
  await expect(nav.getByRole("link")).toHaveText(["Git forges"])
  await page.getByRole("searchbox", { name: "Search settings" }).fill("zzz")
  await expect(nav.getByText("No setting matches.")).toBeVisible()
})

test("the old network addresses lead to Settings and Home", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/network`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/settings/network$`))
  await page.goto(`/e/${env}/network/rules?q=example`)
  await expect(page).toHaveURL(
    new RegExp(`/e/${env}/settings/network\\?q=example$`)
  )
  await page.goto(`/e/${env}/network/requests`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}$`))
  await page.goto(`/e/${env}/network/activity`)
  await expect(page).toHaveURL(
    new RegExp(`/e/${env}/settings/activity\\?kind=network$`)
  )
})

test("rules group by registrable domain, and the filters live in the URL", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const rules = await seedRules(
    api,
    env,
    await api.createSandbox(sandboxName("rules"))
  )

  await page.goto(`/e/${env}/settings/network`)
  const host = (name: string) => page.getByRole("cell", { name, exact: true })
  await expect(host(rules.api)).toBeVisible()
  await expect(host(rules.cdn)).toBeVisible()
  await expect(host(rules.www)).toBeVisible()
  await expect(groupRow(page, rules.domain, 2)).toBeVisible()
  // A domain with one rule needs no heading: the host names it.
  await expect(groupRow(page, rules.other, 1)).toHaveCount(0)

  const search = page.getByRole("searchbox", { name: "Search hosts" })
  await search.fill("cdn.")
  await expect(page).toHaveURL(/[?&]q=cdn\./)
  await expect(host(rules.cdn)).toBeVisible()
  await expect(host(rules.api)).toBeHidden()
  await search.fill("")
  await expect(page).not.toHaveURL(/[?&]q=/)

  await page.getByRole("combobox", { name: "Scope" }).click()
  await page.getByRole("option", { name: "Sandbox", exact: true }).click()
  await expect(page).toHaveURL(/[?&]scope=sandbox/)
  await expect(host(rules.cdn)).toBeVisible()
  await expect(host(rules.api)).toBeHidden()
  await expect(host(rules.www)).toBeHidden()

  await page.getByRole("combobox", { name: "Action" }).click()
  await page.getByRole("option", { name: "Deny", exact: true }).click()
  await expect(page).toHaveURL(/[?&]action=deny/)
  await expect(host(rules.cdn)).toBeVisible()

  // A host no rule has leaves nothing to show, so the empty state offers to clear.
  await search.fill(`no-such-${rules.domain}`)
  await expect(page.getByText("No rules match these filters.")).toBeVisible()
  await page.getByRole("button", { name: "Clear filters" }).click()
  await expect(search).toHaveValue("")
  await expect(page).not.toHaveURL(/[?&](q|scope|action)=/)
  await expect(host(rules.api)).toBeVisible()
  await expect(host(rules.www)).toBeVisible()
})

test("every settings section fits a 390px screen", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await seedRules(api, env, await api.createSandbox(sandboxName("narrow")))

  for (const [section, heading] of [
    ["general", "General"],
    ["network", "Network"],
    ["secrets", "Secrets"],
    ["memory", "Shared memory"],
    ["activity", "Activity"],
    ["providers", "AI providers"],
    ["forges", "Git forges"],
    ["templates", "Templates"],
    ["runtime", "Runtime"],
    ["ports", "Ports and LAN"],
    ["updates", "Updates"],
  ] as const) {
    await page.goto(`/e/${env}/settings/${section}`)
    await expect(
      page.getByRole("heading", { name: heading, exact: true }).first()
    ).toBeVisible()
    // The open section is in view in the nav row, which scrolls sideways.
    await expect(
      page
        .getByRole("navigation", { name: "Settings" })
        .getByRole("link", { name: heading, exact: true })
    ).toBeInViewport()
    const width = await page.evaluate(
      () => document.documentElement.scrollWidth
    )
    expect(width, `settings/${section} scrolls sideways`).toBeLessThanOrEqual(
      390
    )
  }
})

test("the origin and unused filters live in the URL", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const rules = await seedRules(
    api,
    env,
    await api.createSandbox(sandboxName("origin"))
  )
  const host = (name: string) => page.getByRole("cell", { name, exact: true })

  // Rules made through the API are manual, so the approval origin leaves them out.
  await page.goto(`/e/${env}/settings/network`)
  await expect(host(rules.api)).toBeVisible()
  await page.getByRole("combobox", { name: "Origin" }).click()
  await page.getByRole("option", { name: "Approval", exact: true }).click()
  await expect(page).toHaveURL(/[?&]origin=approval/)
  await expect(host(rules.api)).toBeHidden()
  await page.getByRole("combobox", { name: "Origin" }).click()
  await page.getByRole("option", { name: "Manual", exact: true }).click()
  await expect(page).toHaveURL(/[?&]origin=manual/)
  await expect(host(rules.api)).toBeVisible()

  // The rules were made just now, so none is unused.
  await page.getByRole("checkbox", { name: "Unused" }).click()
  await expect(page).toHaveURL(/[?&]unused=1/)
  await expect(page.getByText("No rules match these filters.")).toBeVisible()
  await page.getByRole("button", { name: "Clear filters" }).click()
  await expect(page).not.toHaveURL(/[?&](origin|unused)=/)
  await expect(host(rules.api)).toBeVisible()
})

test("Studio is Pixel unless this browser picks Quiet", async ({
  page,
  env,
}) => {
  const html = page.locator("html")
  // In rem; the built CSS can drop the leading zero.
  const radius = () =>
    page.evaluate(() =>
      parseFloat(
        getComputedStyle(document.documentElement).getPropertyValue("--radius")
      )
    )
  await page.goto(`/e/${env}/settings/general`)
  const style = page.getByRole("group", { name: "Style" })
  await expect(html).toHaveAttribute("data-style", "pixel")
  await expect(style.getByRole("button", { name: "Pixel" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  expect(await radius()).toBe(0)

  await style.getByRole("button", { name: "Quiet" }).click()
  await expect(html).toHaveAttribute("data-style", "quiet")
  expect(await radius()).toBe(0.375)
  await page.reload()
  await expect(html).toHaveAttribute("data-style", "quiet")

  // Pixel is the default, so picking it again stores nothing.
  await style.getByRole("button", { name: "Pixel" }).click()
  await expect(html).toHaveAttribute("data-style", "pixel")
  expect(
    await page.evaluate(() => localStorage.getItem("studio.style"))
  ).toBeNull()
})
