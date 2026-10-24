import { mkdir, rm, writeFile } from "node:fs/promises"
import path from "node:path"
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

test("an OpenAI-compatible endpoint suggests the models it lists", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/providers`)
  await page.getByRole("button", { name: "Add provider" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add provider" })
  await dialog.getByLabel("Kind").click()
  await page.getByRole("option", { name: "OpenAI-compatible endpoint" }).click()
  await dialog.getByLabel("Base URL").fill("https://llm.example.com/v1")
  await expect(dialog.getByText(/The endpoint lists/)).toHaveCount(0)

  // Studio asks the endpoint once there is a key; the e2e build's fake lists two models.
  await dialog.getByLabel("API key", { exact: true }).fill("sk-e2e-fake")
  await expect(dialog.getByText("The endpoint lists 2 models")).toBeVisible()
  await expect(dialog.locator("datalist#provider-models option")).toHaveCount(2)
  await expect(
    dialog.locator('datalist#provider-models option[value="e2e-coder"]')
  ).toHaveCount(1)
  // Any other model can still be typed.
  const model = dialog.getByLabel("Model", { exact: true })
  await model.fill("my-own-model")
  await expect(model).toHaveValue("my-own-model")
  // An endpoint's model is taken as text only until the user says it sees images.
  await expect(
    dialog.getByRole("checkbox", { name: "The model can see images" })
  ).not.toBeChecked()
  await page.keyboard.press("Escape")
})

test("logins on this computer become providers without copying a sign-in", async ({
  page,
  api,
  env,
  studio,
}) => {
  // The e2e Studio looks for the CLIs' files in a home of its own.
  const hostHome = path.join(studio.home, "e2e-host-home")
  const claims = Buffer.from(
    JSON.stringify({
      email: "ada@example.com",
      "https://api.openai.com/auth": { chatgpt_plan_type: "plus" },
    })
  ).toString("base64url")
  const files: Record<string, unknown> = {
    ".codex/auth.json": {
      tokens: {
        id_token: `aGVhZA.${claims}.c2ln`,
        access_token: "a.b.c",
        refresh_token: "rt-host-e2e",
      },
    },
    ".local/share/opencode/auth.json": {
      anthropic: { type: "api", key: "sk-ant-opencode-e2e" },
      openai: { type: "oauth", refresh: "rt-host-e2e", access: "at" },
    },
  }
  for (const [rel, body] of Object.entries(files)) {
    await mkdir(path.dirname(path.join(hostHome, rel)), { recursive: true })
    await writeFile(path.join(hostHome, rel), JSON.stringify(body))
  }
  const before = new Set(
    (
      (await api.json<{ id: string }[]>(
        "GET",
        `/api/environments/${env}/providers`
      )) ?? []
    ).map((p) => p.id)
  )
  try {
    await page.goto(`/e/${env}/settings/providers`)
    const offers = page.getByRole("region", { name: "On this computer" })
    await expect(offers.getByText("Codex", { exact: true })).toBeVisible()

    // ChatGPT: the dialog names the computer's account, and Studio signs in on its own.
    await offers
      .getByRole("button", { name: "Use ChatGPT subscription" })
      .click()
    const dialog = page.getByRole("dialog", { name: "Add provider" })
    await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(
      "ChatGPT"
    )
    await expect(dialog.getByText("ada@example.com")).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(dialog).toBeHidden()

    // opencode: its plain key is imported, its OAuth sign-in is not.
    await offers.getByRole("button", { name: "Import keys…" }).click()
    const importer = page.getByRole("dialog", {
      name: "Import keys from opencode",
    })
    const anthropic = importer
      .getByRole("listitem")
      .filter({ hasText: "API key" })
    await expect(importer.getByText(/A subscription sign-in/)).toBeVisible()
    await anthropic.getByRole("button", { name: "Import" }).click()
    await expect(anthropic.getByText("Imported")).toBeVisible()
    await importer.getByRole("button", { name: "Done" }).click()

    const providers =
      (await api.json<{ id: string; kind: string; state: string }[]>(
        "GET",
        `/api/environments/${env}/providers`
      )) ?? []
    const imported = providers.filter((p) => !before.has(p.id))
    expect(imported.map((p) => [p.kind, p.state])).toEqual([
      ["anthropic_api", "ready"],
    ])
  } finally {
    for (const rel of Object.keys(files))
      await rm(path.join(hostHome, rel), { force: true })
    const providers =
      (await api.json<{ id: string }[]>(
        "GET",
        `/api/environments/${env}/providers`
      )) ?? []
    for (const added of providers.filter((p) => !before.has(p.id)))
      await api.request.delete(`/api/environments/${env}/providers/${added.id}`)
  }
})
