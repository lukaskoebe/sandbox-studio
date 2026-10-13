import { readFile } from "node:fs/promises"
import { expect, test } from "./fixtures"

test("a memory scope exports to a markdown file, and the file imports into another environment", async ({
  page,
  api,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const source = await api.json<{ id: string }>("POST", "/api/environments", {
    name: `Memory ${suffix}`,
  })
  const id = source!.id
  const fact = `Deploy the api with make ship ${suffix}`
  await api.json("POST", `/api/environments/${id}/memory/facts`, {
    scope: "shared",
    kind: "procedure",
    text: fact,
    attribute: "deploy.command",
  })

  await page.goto(`/e/${id}/memory`)
  const downloaded = page.waitForEvent("download")
  await page.getByRole("button", { name: "Export", exact: true }).click()
  const file = await downloaded
  expect(file.suggestedFilename()).toMatch(
    /^memory-shared-\d{4}-\d{2}-\d{2}\.md$/
  )
  const path = await file.path()
  const text = await readFile(path, "utf8")
  expect(text).toContain("studio-memory: 1")
  expect(text).toContain(fact)

  const copy = await api.json<{ id: string }>("POST", "/api/environments", {
    name: `Memory ${suffix} copy`,
  })
  await page.goto(`/e/${copy!.id}/memory`)
  const chooser = page.waitForEvent("filechooser")
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await (await chooser).setFiles(path)
  await expect(page.getByText("Memory imported")).toBeVisible()
  const facts = await api.json<{ text: string; tier: string }[]>(
    "GET",
    `/api/environments/${copy!.id}/memory/facts?scope=shared`
  )
  expect(facts?.some((f) => f.text === fact && f.tier === "document")).toBe(
    true
  )
})
