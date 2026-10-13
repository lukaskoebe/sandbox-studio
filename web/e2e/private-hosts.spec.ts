import { expect, test } from "./fixtures"

/** A host name unique to this run, so tests don't see each other's entries. */
function freshName(label: string, suffix: string) {
  return `${label}-${Math.random().toString(36).slice(2, 8)}.${suffix}`
}

test("Settings > Network adds, lists and removes private hosts", async ({
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
    page.getByRole("heading", { name: "Private hosts", exact: true })
  ).toBeVisible()
  await expect(
    page.getByText("Private hosts let sandboxes reach company hosts")
  ).toBeVisible()

  await page.getByLabel("Host", { exact: true }).fill(host)
  await page.getByLabel("Ports").fill("22")
  await page.getByRole("button", { name: "Add private host" }).click()
  const list = page.getByRole("list", { name: "Private hosts" })
  await expect(list).toContainText(host)
  await expect(list).toContainText("Ports 22")
  await expect
    .poll(async () => (await saved())?.map((h) => h.host))
    .toContain(host)

  // A wildcard over a top-level domain is refused, and the list keeps its entries.
  await page.getByLabel("Host", { exact: true }).fill("*.com")
  await page.getByRole("button", { name: "Add private host" }).click()
  await expect(page.getByText("is too broad")).toBeVisible()
  await expect(list).toContainText(host)

  await page.getByRole("button", { name: `Remove ${host}` }).click()
  // The list goes when its last entry does, so the entry itself is what must be gone.
  await expect(page.getByText(host)).toBeHidden()
  await expect
    .poll(async () => (await saved())?.map((h) => h.host))
    .not.toContain(host)
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
