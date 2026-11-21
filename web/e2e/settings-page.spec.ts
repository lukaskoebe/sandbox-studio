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

  // The environment's pages left Settings for the sidebar's Environment group.
  await expect(nav.getByRole("link", { name: "Network" })).toHaveCount(0)
  await nav.getByRole("link", { name: "Git forges", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/settings/forges$`))
  const sidebar = page.locator('[data-sidebar="sidebar"]')
  await sidebar.getByRole("link", { name: "Network", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network$`))
  await expect(
    page.getByRole("heading", { name: "Rules", exact: true })
  ).toBeVisible()

  await sidebar.getByRole("link", { name: "Activity", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/activity$`))
  await page.getByRole("link", { name: "Git", exact: true }).click()
  await expect(page).toHaveURL(/[?&]kind=git/)
  await expect(
    page.getByRole("link", { name: "Git", exact: true })
  ).toHaveAttribute("aria-pressed", "true")

  // The search finds a section by what it holds, not only by its name.
  await page.goto(`/e/${env}/settings`)
  await page.getByRole("searchbox", { name: "Search settings" }).fill("gitlab")
  await expect(nav.getByRole("link")).toHaveText(["Git forges"])
  await page.getByRole("searchbox", { name: "Search settings" }).fill("zzz")
  await expect(nav.getByText("No setting matches.")).toBeVisible()
})

test("the old network and settings addresses lead to the new pages", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/network?q=example`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network\\?q=example$`))
  await page.goto(`/e/${env}/settings/secrets`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/secrets$`))
  await page.goto(`/e/${env}/settings/memory`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/memory$`))
  await page.goto(`/e/${env}/settings/activity?kind=git`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/activity\\?kind=git$`))
  await page.goto(`/e/${env}/network/rules?q=example`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network\\?q=example$`))
  await page.goto(`/e/${env}/network/requests`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}$`))
  await page.goto(`/e/${env}/network/activity`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/activity\\?kind=network$`))
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

  await page.goto(`/e/${env}/network`)
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

test("every settings section and environment page fits a 390px screen", async ({
  page,
  api,
  env,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await seedRules(api, env, await api.createSandbox(sandboxName("narrow")))

  for (const [path, heading] of [
    ["network", "Network"],
    ["secrets", "Secrets"],
    ["memory", "Shared memory"],
    ["activity", "Activity"],
  ] as const) {
    await page.goto(`/e/${env}/${path}`)
    await expect(
      page.getByRole("heading", { name: heading, exact: true }).first()
    ).toBeVisible()
    const width = await page.evaluate(
      () => document.documentElement.scrollWidth
    )
    expect(width, `${path} scrolls sideways`).toBeLessThanOrEqual(390)
  }
  for (const [section, heading] of [
    ["general", "General"],
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
  await page.goto(`/e/${env}/network`)
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

test("this browser's text size scales the whole page", async ({
  page,
  env,
}) => {
  const rootSize = () =>
    page.evaluate(() =>
      parseFloat(getComputedStyle(document.documentElement).fontSize)
    )
  await page.goto(`/e/${env}/settings/general`)
  const size = page.getByRole("group", { name: "Text size" })
  await expect(size.getByRole("button", { name: "100%" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  expect(await rootSize()).toBe(16)
  const button = size.getByRole("button", { name: "150%" })
  const before = (await button.boundingBox())!.height

  await size.getByRole("button", { name: "125%" }).click()
  await expect.poll(rootSize).toBe(20)
  expect((await button.boundingBox())!.height).toBeGreaterThan(before)
  await page.reload()
  await expect.poll(rootSize).toBe(20)

  // 100% is the default, so picking it again stores nothing.
  await size.getByRole("button", { name: "100%" }).click()
  await expect.poll(rootSize).toBe(16)
  expect(
    await page.evaluate(() => localStorage.getItem("studio.text-size"))
  ).toBeNull()
})

test("the terminal shell is bash until zsh is picked, for every browser", async ({
  page,
  env,
  api,
}) => {
  await page.goto(`/e/${env}/settings/general`)
  const shell = page.getByRole("group", { name: "Terminal shell" })
  await expect(shell.getByRole("button", { name: "bash" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  await shell.getByRole("button", { name: "zsh" }).click()
  await expect(shell.getByRole("button", { name: "zsh" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  expect(
    await api.json<{ shell: string }>("GET", "/api/system/terminal")
  ).toMatchObject({ shell: "zsh" })
  await page.reload()
  await expect(shell.getByRole("button", { name: "zsh" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
  await shell.getByRole("button", { name: "bash" }).click()
  await expect(shell.getByRole("button", { name: "bash" })).toHaveAttribute(
    "aria-pressed",
    "true"
  )
})

test("the Studio menu checks for updates, restarts and quits Studio, asking first", async ({
  page,
  env,
}) => {
  // The routes are the Go tests'; here they would stop the Studio all the tests share.
  const asked = { restart: 0, shutdown: 0 }
  for (const path of ["restart", "shutdown"] as const)
    await page.route(`**/api/system/${path}`, async (route) => {
      asked[path]++
      await route.fulfill({ status: 202 })
    })
  await page.route("**/api/system/updates/check", (route) =>
    route.fulfill({
      json: {
        mode: "tell",
        enabled: true,
        current: "v1.0.0",
        latest: "v1.1.0",
        available: true,
        canInstall: false,
      },
    })
  )
  await page.goto(`/e/${env}`)
  const menu = page.getByRole("button", { name: /^Sandbox Studio/ })
  const item = (name: string) => page.getByRole("menuitem", { name })

  await menu.click()
  await expect(item("What's new")).toHaveAttribute(
    "href",
    /github\.com\/lukaskoebe\/sandbox-studio\/releases/
  )
  await item("Check for updates").click()
  await expect(page.getByText("Studio v1.1.0 is out")).toBeVisible()
  await page.getByRole("button", { name: "Update…" }).click()
  await expect(page).toHaveURL(/\/settings\/updates$/)

  // Restarting asks first, then waits for Studio to go away and come back.
  let down = 0
  await page.route("**/api/health", (route) =>
    down++ < 2 ? route.abort() : route.continue()
  )
  await menu.click()
  await item("Restart Studio…").click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toBeHidden()
  expect(asked.restart).toBe(0)
  await menu.click()
  await item("Restart Studio…").click()
  await dialog.getByRole("button", { name: "Restart" }).click()
  await expect(page.getByText("Studio restarted")).toBeVisible({
    timeout: 15_000,
  })
  expect(asked.restart).toBe(1)

  // Settings no longer has a Shut down row: quitting is the menu's.
  await page.goto(`/e/${env}/settings/general`)
  await expect(page.getByRole("button", { name: /Shut down/ })).toHaveCount(0)
  await menu.click()
  await item("Quit Studio…").click()
  await dialog.getByRole("button", { name: "Cancel" }).click()
  expect(asked.shutdown).toBe(0)
  await menu.click()
  await item("Quit Studio…").click()
  await dialog.getByRole("button", { name: "Quit" }).click()
  await expect(page.getByText("Studio has stopped")).toBeVisible()
  expect(asked.shutdown).toBe(1)
})

test("Check for updates asks now and says what it found", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/updates`)
  const check = page.getByRole("button", { name: "Check for updates" })
  const state = (await (
    await page.request.get("/api/system/updates")
  ).json()) as Record<string, unknown>
  // The e2e Studio is a development build, which never asks GitHub, except on a tag: the
  // Makefile stamps the tag as its version (v1.2.3 or v1.2.3-rc.1), and a release build asks.
  // git describe's builds (v1.2.3-4-gabcdef, -dirty) are development builds, as in updates.go.
  const current = String(state.current)
  const release =
    /^v?\d+\.\d+\.\d+([-+].*)?$/.test(current) &&
    !current.includes("-g") &&
    !current.endsWith("-dirty")
  if (!release) {
    await check.click()
    await expect(
      page.getByText("a development build doesn't check for releases")
    ).toBeVisible()
  }

  // What a release build hears back, without reaching GitHub. Its own check at the start may
  // have failed (GitHub refusing a runner), and a check that answers clears that.
  let found: Record<string, unknown> = {
    ...state,
    latest: state.current,
    checkedAt: new Date(),
    error: undefined,
  }
  await page.route("**/api/system/updates/check", (route) =>
    route.fulfill({ json: found })
  )
  await check.click()
  await expect(page.getByText("Studio is up to date")).toBeVisible()
  found = { ...found, latest: "v99.0.0", available: true }
  await check.click()
  const updates = page.getByRole("group", { name: "Updates" })
  await expect(updates.getByText("Update available")).toBeVisible()
  await expect(updates.getByText("v99.0.0")).toBeVisible()
})
