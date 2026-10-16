import { expect, test } from "./fixtures"

/** A host name unique to this run, so tests don't see each other's entries. */
function freshName(label: string, suffix: string) {
  return `${label}-${Math.random().toString(36).slice(2, 8)}.${suffix}`
}

test("Settings > Network adds, lists and removes local network hosts", async ({
  page,
  api,
  env,
}) => {
  const host = freshName("git", "company.local")
  const saved = () =>
    api.json<{ host: string; ports: number[] | null }[]>(
      "GET",
      `/api/environments/${env}/private-hosts`
    )

  await page.goto(`/e/${env}/settings/network`)
  await expect(
    page.getByRole("heading", { name: "Local network", exact: true })
  ).toBeVisible()
  await expect(page.getByText(/compan(y|ies) networks?/i)).toHaveCount(0)

  await page.getByLabel("Host", { exact: true }).fill(host)
  await page.getByLabel("Ports").fill("22")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  const list = page.getByRole("list", { name: "Local network" })
  await expect(list).toContainText(host)
  await expect(list).toContainText("Ports 22")
  await expect
    .poll(async () => (await saved())?.map((h) => h.host))
    .toContain(host)

  // An address is listed too, and the row says the name sandboxes connect to.
  const octet = 2 + Math.floor(Math.random() * 250)
  const addr = `192.168.231.${octet}`
  await page.getByLabel("Host", { exact: true }).fill(addr)
  await page.getByLabel("Ports").fill("22")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await expect(list).toContainText(
    `Sandboxes connect to 192-168-231-${octet}.lan.studio.internal`
  )

  // A wildcard over a top-level domain is refused, and the list keeps its entries.
  await page.getByLabel("Host", { exact: true }).fill("*.com")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await expect(page.getByText("is too broad")).toBeVisible()
  await expect(list).toContainText(host)

  await page.getByRole("button", { name: `Remove ${host}` }).click()
  await page.getByRole("button", { name: `Remove ${addr}` }).click()
  // The list goes when its last entry does, so the entry itself is what must be gone.
  await expect(page.getByText(host)).toBeHidden()
  await expect
    .poll(async () => (await saved())?.map((h) => h.host))
    .not.toContain(host)
})

test("Settings > Network opens and closes ports of this computer", async ({
  page,
  api,
  env,
}) => {
  await page.goto(`/e/${env}/settings/network`)
  await expect(
    page.getByRole("heading", { name: "This computer", exact: true })
  ).toBeVisible()
  await expect(page.getByText("No ports open.")).toBeVisible()
  await page.getByLabel("Port", { exact: true }).fill("2222")
  await page.getByRole("button", { name: "Open port" }).click()
  await expect(page.getByRole("list", { name: "Open ports" })).toContainText(
    "host.studio.internal:2222"
  )
  await expect
    .poll(() =>
      api.json<number[]>("GET", `/api/environments/${env}/host-ports`)
    )
    .toEqual([2222])
  await page.getByRole("button", { name: "Close port 2222" }).click()
  await expect(page.getByText("No ports open.")).toBeVisible()
  await expect
    .poll(() =>
      api.json<number[]>("GET", `/api/environments/${env}/host-ports`)
    )
    .toEqual([])
})

test("a rule no sandbox can reach says so, and why", async ({
  page,
  api,
  env,
}) => {
  await page.goto(`/e/${env}/network/rules`)
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Host", { exact: true }).fill("127.0.0.1")
  await dialog.getByLabel("Ports").fill("5432")
  await dialog.getByRole("button", { name: "Add rule", exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText("No sandbox can reach this rule")).toBeVisible()
  const row = page.getByRole("row").filter({ hasText: "5432" })
  await expect(row).toContainText("stay inside the sandbox")
  await expect(
    row.getByRole("link", { name: "Open network settings" })
  ).toBeVisible()

  const rules = await api.json<{ id: string; host: string }[]>(
    "GET",
    `/api/environments/${env}/rules`
  )
  for (const rule of rules?.filter((r) => r.host === "127.0.0.1") ?? [])
    await api.json("DELETE", `/api/environments/${env}/rules/${rule.id}`)
})

test("a proxy rule's Advanced fields are saved with the rule", async ({
  page,
  api,
  env,
}) => {
  const host = freshName("proxied", "example.org")
  await page.goto(`/e/${env}/network/rules`)
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Host", { exact: true }).fill(host)
  await dialog.getByRole("button", { name: "Proxy", exact: true }).click()
  await dialog.getByRole("button", { name: "Advanced" }).click()
  await dialog.getByLabel("Upstream").fill("upstream.example.org:8443")
  await dialog.getByLabel("Host header").fill("api.example.org")
  await dialog.getByLabel("SNI").fill("sni.example.org")
  await dialog.getByRole("button", { name: "Add rule", exact: true }).click()
  await expect(dialog).toBeHidden()

  await expect
    .poll(
      async () =>
        (
          await api.json<{ host: string; config: Record<string, string> }[]>(
            "GET",
            `/api/environments/${env}/rules`
          )
        )?.find((r) => r.host === host)?.config
    )
    .toMatchObject({
      upstream: "upstream.example.org:8443",
      hostHeader: "api.example.org",
      sni: "sni.example.org",
    })
})
