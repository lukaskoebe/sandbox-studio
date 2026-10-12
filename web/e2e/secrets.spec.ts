import type { Response } from "@playwright/test"
import { expect, sandboxName, terminal, test } from "./fixtures"

test("a secret reaches the guest only as its placeholder", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("sec"))
  const suffix = Math.random().toString(36).slice(2, 8)
  const name = `E2E_TOKEN_${suffix.toUpperCase()}`
  const value = `e2e-secret-value-${suffix}`

  // Every API response the page reads, to check none carries the value.
  const responses: Promise<string>[] = []
  const record = (r: Response) => {
    const type = r.headers()["content-type"] ?? ""
    if (r.url().includes("/api/") && type.includes("json"))
      responses.push(r.text().catch(() => ""))
  }
  page.on("response", record)

  await page.goto(`/e/${env}/secrets`)
  await page.getByRole("button", { name: "Add secret" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add secret" })
  await dialog.getByLabel("Name", { exact: true }).fill(name)
  await dialog.getByLabel("Value", { exact: true }).fill(value)
  await dialog.getByLabel("Hosts", { exact: true }).fill("api.example.com")
  await dialog.getByLabel("Hosts", { exact: true }).press("Enter")
  await dialog.getByRole("button", { name: "Add secret" }).click()
  await expect(dialog).toBeHidden()

  const secrets = await api.json<{ name: string; placeholder: string }[]>(
    "GET",
    `/api/environments/${env}/secrets`
  )
  const placeholder = secrets!.find((s) => s.name === name)!.placeholder
  expect(placeholder).toMatch(/^studio-/)
  await expect(page.getByRole("row").filter({ hasText: name })).toContainText(
    placeholder
  )

  // The guest's config carries the placeholder, and a shell sees it.
  await expect
    .poll(async () => (await api.guest(id)).env[name])
    .toBe(placeholder)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const term = terminal(page)
  await term.run(`printenv ${name}`)
  await expect(term.line(placeholder)).toHaveCount(1)

  page.off("response", record)
  const bodies = await Promise.all(responses)
  expect(bodies.length).toBeGreaterThan(0)
  for (const path of [
    `/api/environments/${env}/secrets`,
    `/api/environments/${env}/sandboxes/${id}`,
    `/__e2e/sandboxes/${id}`,
  ])
    bodies.push(await (await api.request.get(path)).text())
  for (const body of bodies) expect(body).not.toContain(value)
})
