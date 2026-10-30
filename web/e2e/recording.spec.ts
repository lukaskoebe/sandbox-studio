import { expect, sandboxName, test } from "./fixtures"

// Recording a computer's intercepted traffic keeps its requests in the Connections panel, with
// their headers and bodies, what Studio changed and the secrets it sent, until cleared.
test("the Connections panel records a computer's intercepted requests", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("rec")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await expect(
    screen.getByRole("tablist", { name: `${name}'s screen` })
  ).toBeVisible()
  await screen.getByRole("button", { name: `More for ${name}` }).click()
  await page.getByRole("menuitem", { name: "Connections" }).click()
  const sheet = page.getByRole("dialog", { name: "Connections" })

  const record = sheet.getByRole("switch", {
    name: "Record intercepted traffic",
  })
  await expect(record).not.toBeChecked()
  await expect(sheet.getByRole("tab", { name: /Requests/ })).toHaveCount(0)
  await record.click()
  await expect(record).toBeChecked()
  await expect(sheet.getByText("Recording", { exact: true })).toBeVisible()
  await sheet.getByRole("tab", { name: /Requests/ }).click()
  await expect(sheet.getByText("Nothing recorded yet")).toBeVisible()

  await api.json("POST", `/api/e2e/sandboxes/${id}/recorded`, {
    method: "POST",
    url: "https://api.example.com/v1/items?page=2",
    requestHeaders: [
      { name: "Authorization", value: "Bearer studio-placeholder" },
      { name: "Content-Type", value: "application/json" },
    ],
    requestBody: { size: 13, text: '{"name":"a"}\n' },
    changed: ["Authorization"],
    secrets: ["API_KEY"],
    status: 422,
    responseHeaders: [{ name: "Content-Type", value: "application/json" }],
    responseBody: { size: 26, text: '{"error":"name too short"}' },
  })
  const requests = sheet.getByRole("list", { name: "Recorded requests" })
  const item = requests.getByRole("listitem")
  await expect(item).toHaveCount(1)
  await expect(item).toContainText("POST")
  await expect(item).toContainText("422")
  await expect(item).toContainText("/v1/items?page=2")
  await expect(item).toContainText("api.example.com")
  await expect(item).toContainText("1 secret")

  await item.getByRole("button").click()
  await expect(item).toContainText(
    "Studio sent the secret API_KEY in place of the placeholders shown here."
  )
  await expect(item).toContainText(
    "Bearer studio-placeholder(changed by Studio)"
  )
  // JSON bodies are shown indented.
  await expect(item.locator("pre").last()).toHaveText(
    '{\n  "error": "name too short"\n}'
  )

  // Stopped, it keeps what it has until cleared.
  await record.click()
  await expect(record).not.toBeChecked()
  await expect(sheet).toContainText("1 request kept until")
  await expect(item).toHaveCount(1)
  await sheet.getByRole("button", { name: "Clear" }).click()
  await expect(sheet.getByRole("tab", { name: /Requests/ })).toHaveCount(0)
  await expect(sheet.getByText("No connections yet")).toBeVisible()
})
