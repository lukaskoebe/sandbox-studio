import type { Page } from "@playwright/test"
import { approvals, expect, sandboxName, test, type Api } from "./fixtures"

/** The group row of a domain: its name, then how many rules sit under it. */
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

test("the network tabs open by URL and by click", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  await page.goto(`/e/${env}/network/activity`)
  await expect(
    page.getByRole("tab", { name: "Activity", exact: true })
  ).toHaveAttribute("aria-selected", "true")
  await expect(
    page.getByRole("heading", { name: "Decisions", exact: true })
  ).toBeVisible()

  await page.getByRole("tab", { name: "Rules", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network/rules$`))
  await expect(
    page.getByRole("heading", { name: "Rules", exact: true })
  ).toBeVisible()

  await page.getByRole("tab", { name: "Requests", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network/requests$`))
  await expect(
    page.getByText("Nothing is waiting for a decision.")
  ).toBeVisible()
})

test("/network opens the requests tab only while a connection waits", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  await page.goto(`/e/${env}/network`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network/rules$`))

  const id = await api.createSandbox(sandboxName("net-tabs"))
  const host = `held-${Math.random().toString(36).slice(2, 8)}.example.org`
  await page.goto(`/e/${env}`)
  await api.connect(id, host)
  await expect(
    approvals(page).getByTestId("approval-card").filter({ hasText: host })
  ).toBeVisible()

  await page.goto(`/e/${env}/network`)
  await expect(page).toHaveURL(new RegExp(`/e/${env}/network/requests$`))
  await expect(
    page.getByRole("heading", { name: "Waiting for you", exact: true })
  ).toBeVisible()
  await expect(
    page.getByRole("listitem").filter({ hasText: host })
  ).toBeVisible()

  await api.dismissApprovals()
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

  await page.goto(`/e/${env}/network/rules`)
  const host = (name: string) => page.getByRole("cell", { name, exact: true })
  await expect(host(rules.api)).toBeVisible()
  await expect(host(rules.cdn)).toBeVisible()
  await expect(host(rules.www)).toBeVisible()
  await expect(groupRow(page, rules.domain, 2)).toBeVisible()
  await expect(groupRow(page, rules.other, 1)).toBeVisible()

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

test("the network tabs fit a 390px screen", async ({ page, api, env }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await api.dismissApprovals()
  await seedRules(api, env, await api.createSandbox(sandboxName("narrow")))

  for (const [tab, heading] of [
    ["rules", "Rules"],
    ["requests", "Waiting for you"],
    ["activity", "Decisions"],
  ] as const) {
    await page.goto(`/e/${env}/network/${tab}`)
    await expect(
      page.getByRole("heading", { name: heading, exact: true })
    ).toBeVisible()
    const width = await page.evaluate(
      () => document.documentElement.scrollWidth
    )
    expect(width, `network/${tab} scrolls sideways`).toBeLessThanOrEqual(390)
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
  await page.goto(`/e/${env}/network/rules`)
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
