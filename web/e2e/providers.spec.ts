import { expect, test } from "./fixtures"

test("a Claude subscription asks for a browser sign-in, not a key", async ({
  page,
  api,
  env,
}) => {
  // An API-key provider first, so the subscription doesn't become the default teammate's.
  const existing = await api.json<{ id: string }[]>(
    "GET",
    `/api/environments/${env}/providers`
  )
  if (!existing?.length)
    await api.json("POST", `/api/environments/${env}/providers`, {
      name: "Anthropic",
      kind: "anthropic_api",
      apiKey: "sk-ant-e2e-fake",
    })

  await page.goto(`/e/${env}/settings/providers`)
  await page.getByRole("button", { name: "Add provider" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add provider" })
  await expect(dialog.getByText(/Paste the key below/)).toBeVisible()
  await expect(dialog.getByLabel("API key")).toBeVisible()

  await dialog.getByLabel("Name", { exact: true }).fill("Claude Max")
  await dialog.getByLabel("Kind").click()
  await page.getByRole("option", { name: "Claude subscription" }).click()
  await expect(dialog.getByText(/there is no key to paste/)).toBeVisible()
  await expect(dialog.getByLabel("API key")).toHaveCount(0)
  await dialog.getByRole("button", { name: "Add provider" }).click()

  // The sign-in follows in the same dialog; the token is a separate, folded-away path.
  await expect(
    dialog.getByRole("button", { name: "Sign in with Claude" })
  ).toBeVisible()
  const fallback = dialog.getByRole("button", {
    name: "Use a token from claude setup-token instead",
  })
  await expect(fallback).toHaveAttribute("aria-expanded", "false")
  await expect(dialog.getByLabel("Claude Code token")).toHaveCount(0)
  await fallback.click()
  await expect(dialog.getByLabel("Claude Code token")).toBeVisible()
  await expect(
    dialog.getByRole("button", { name: "Save token" })
  ).toBeDisabled()
  await dialog.getByRole("button", { name: "Done" }).click()
  await expect(dialog).toBeHidden()

  // The providers table offers the sign-in again.
  const row = page.getByRole("row").filter({ hasText: "Claude Max" })
  await expect(row.getByText("Sign-in needed")).toBeVisible()
  await row.getByRole("button", { name: "Sign in" }).click()
  await expect(
    page.getByRole("dialog", { name: "Sign in to Claude Max" })
  ).toBeVisible()
  await page.keyboard.press("Escape")

  const providers = await api.json<{ id: string; name: string }[]>(
    "GET",
    `/api/environments/${env}/providers`
  )
  const added = providers?.find((p) => p.name === "Claude Max")
  if (added)
    await api.request.delete(`/api/environments/${env}/providers/${added.id}`)
})
