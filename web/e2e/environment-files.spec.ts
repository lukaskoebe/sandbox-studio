import { readFile } from "node:fs/promises"
import { expect, test } from "./fixtures"

test("an environment exports to a file without its keys, and the file imports as a new environment", async ({
  page,
  api,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const name = `Files ${suffix}`
  const source = await api.json<{ id: string }>("POST", "/api/environments", {
    name,
  })
  const id = source!.id
  await api.json("POST", `/api/environments/${id}/providers`, {
    name: "Anthropic",
    kind: "anthropic_api",
    apiKey: "sk-ant-e2e-fake",
  })
  await api.json("POST", `/api/environments/${id}/rules`, {
    host: `files-${suffix}.example.org`,
    action: "allow",
  })

  await page.goto(`/e/${id}/settings/general`)
  const downloaded = page.waitForEvent("download")
  await page.getByRole("link", { name: "Export environment" }).click()
  const file = await downloaded
  expect(file.suggestedFilename()).toBe(`${name}.studio-environment`)
  const path = await file.path()
  const text = await readFile(path, "utf8")
  expect(text).toContain('"format": "studio-environment"')
  expect(text).toContain(`files-${suffix}.example.org`)
  expect(text).not.toContain("sk-ant-e2e-fake")

  const chooser = page.waitForEvent("filechooser")
  await page.getByRole("button", { name: "Import environment" }).click()
  await (await chooser).setFiles(path)
  const report = page.getByRole("dialog")
  await expect(report).toContainText(`${name} 2 is ready`)
  await expect(
    report.getByRole("link", { name: "Add the API key of Anthropic" })
  ).toBeVisible()
  await report.getByRole("button", { name: `Open ${name} 2` }).click()
  await expect(page).toHaveURL(/\/e\/[^/]+$/)
  const envs = await api.json<{ name: string }[]>("GET", "/api/environments")
  expect(envs?.some((e) => e.name === `${name} 2`)).toBe(true)
})
