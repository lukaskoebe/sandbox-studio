import type { Page } from "@playwright/test"
import { expect, test } from "./fixtures"

/** A host name unique to this run, so tests don't see each other's entries. */
function freshName(label: string, suffix: string) {
  return `${label}-${Math.random().toString(36).slice(2, 8)}.${suffix}`
}

/** Answers the dialog that asks for the host a change opens up. */
async function confirmHost(page: Page, host: string) {
  const dialog = page.getByRole("alertdialog")
  await expect(dialog).toContainText("Let computers reach this host?")
  const allow = dialog.getByRole("button", { name: "Allow" })
  await expect(allow).toBeDisabled()
  await dialog.getByLabel("Type the host to confirm").fill(host)
  await allow.click()
  await expect(dialog).toBeHidden()
}

test("Settings > Ports and LAN adds, edits, lists and removes local network hosts", async ({
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

  await page.goto(`/e/${env}/settings/ports`)
  await expect(
    page.getByRole("heading", { name: "Local network", exact: true })
  ).toBeVisible()
  await expect(page.getByText(/compan(y|ies) networks?/i)).toHaveCount(0)

  // A host is listed once the user typed it again; Cancel lists nothing.
  await page.getByLabel("Host", { exact: true }).fill(host)
  await page.getByRole("textbox", { name: "Ports" }).fill("22")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: "Cancel" })
    .click()
  await expect(page.getByRole("alertdialog")).toBeHidden()
  expect((await saved())?.map((h) => h.host) ?? []).not.toContain(host)
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await confirmHost(page, host)
  const list = page.getByRole("list", { name: "Local network" })
  await expect(list).toContainText(host)
  await expect(list).toContainText("Ports 22")
  await expect
    .poll(async () => (await saved())?.map((h) => h.host))
    .toContain(host)
  await expect(page.getByLabel("Host", { exact: true })).toHaveValue("")

  // Another port asks again; dropping one doesn't.
  await page.getByRole("button", { name: `Edit ${host}` }).click()
  const edit = list.getByRole("listitem", { name: `Edit ${host}` })
  await edit.getByRole("textbox", { name: "Ports" }).fill("22, 2222")
  await edit.getByRole("button", { name: "Save" }).click()
  await confirmHost(page, host)
  await expect(list).toContainText("Ports 22, 2222")
  await page.getByRole("button", { name: `Edit ${host}` }).click()
  await edit.getByRole("textbox", { name: "Ports" }).fill("2222")
  await edit.getByRole("button", { name: "Save" }).click()
  await expect(list).toContainText("Ports 2222")
  await expect(page.getByRole("alertdialog")).toBeHidden()
  await expect
    .poll(async () => (await saved())?.find((h) => h.host === host)?.ports)
    .toEqual([2222])

  // An address is listed too, and the row says the name sandboxes connect to.
  const octet = 2 + Math.floor(Math.random() * 250)
  const addr = `192.168.231.${octet}`
  await page.getByLabel("Host", { exact: true }).fill(addr)
  await page.getByRole("textbox", { name: "Ports" }).fill("22")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await confirmHost(page, addr)
  await expect(list).toContainText(
    `Computers connect to 192-168-231-${octet}.lan.studio.internal`
  )

  // A wildcard over a top-level domain is refused, and the list keeps its entries.
  await page.getByLabel("Host", { exact: true }).fill("*.com")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await confirmHost(page, "*.com")
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

test("Settings > Ports and LAN opens and closes ports of this computer", async ({
  page,
  api,
  env,
}) => {
  await page.goto(`/e/${env}/settings/ports`)
  await expect(
    page.getByRole("heading", { name: "This machine", exact: true })
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
  await page.goto(`/e/${env}/network`)
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Host", { exact: true }).fill("127.0.0.1")
  await dialog.getByLabel("Ports").fill("5432")
  await dialog.getByRole("button", { name: "Add rule", exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText("No computer can reach this rule")).toBeVisible()
  const row = page.getByRole("row").filter({ hasText: "5432" })
  await expect(row).toContainText("stay inside the sandbox")
  await expect(
    row.getByRole("link", { name: "Open Ports and LAN" })
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
  await page.goto(`/e/${env}/network`)
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
