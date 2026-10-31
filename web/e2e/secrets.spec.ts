import type { Response } from "@playwright/test"
import {
  addTeammate,
  approvals,
  expect,
  sandboxName,
  terminal,
  test,
} from "./fixtures"

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

  await page.goto(`/e/${env}/settings/secrets`)
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
    `/api/e2e/sandboxes/${id}`,
  ])
    bodies.push(await (await api.request.get(path)).text())
  for (const body of bodies) expect(body).not.toContain(value)
})

test("secrets are grouped by scope, and a teammate's settings list what its sandboxes see", async ({
  page,
  api,
  env,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const teammate = `Sec${suffix}`
  const teammateId = await addTeammate(api, env, teammate)
  const sbName = sandboxName("scoped")
  const sandboxId = await api.createSandbox(sbName, teammateId)
  const shared = `E2E_SHARED_${suffix.toUpperCase()}`
  const plain = `E2E_PLAIN_${suffix.toUpperCase()}`
  const own = `E2E_SANDBOX_${suffix.toUpperCase()}`
  const add = (name: string, scope: object) =>
    api.json("POST", `/api/environments/${env}/secrets`, {
      name,
      value: "e2e-value",
      hosts: ["api.example.com"],
      ...scope,
    })
  await add(shared, {})
  await add(plain, {})
  await add(shared, { teammateId })
  await add(own, { sandboxId })

  // The Secrets page has a section per scope, named after its teammate or sandbox.
  await page.goto(`/e/${env}/settings/secrets`)
  const section = (name: string) => page.getByRole("region", { name })
  const environment = section("Environment")
  await expect(environment.getByRole("row", { name: shared })).toBeVisible()
  await expect(environment.getByRole("row", { name: own })).toHaveCount(0)
  const teammateSection = section(`Teammate ${teammate}`)
  await expect(teammateSection.getByRole("heading")).toHaveText(
    `Teammate ${teammate}`
  )
  await expect(teammateSection.getByRole("row", { name: shared })).toBeVisible()
  await expect(
    section(`Sandbox ${sbName}`).getByRole("row", { name: own })
  ).toBeVisible()

  // The teammate's settings list its secrets, its sandbox's and the inherited ones.
  await page.goto(
    `/e/${env}/teammates/${teammateId}?profile=settings&open=secrets`
  )
  const secrets = page.getByRole("region", { name: "Secrets" })
  const rows = secrets.getByRole("row")
  await expect(
    rows.filter({ hasText: shared }).filter({ hasText: "Inherited" })
  ).toContainText(`replaced by ${teammate}'s`)
  await expect(
    rows.filter({ hasText: shared }).filter({ hasNotText: "Inherited" })
  ).toContainText("replaces the environment's")
  await expect(rows.filter({ hasText: plain })).toContainText("Inherited")
  await expect(rows.filter({ hasText: own })).toContainText(`Sandbox ${sbName}`)

  // A secret added here is the teammate's.
  const added = `E2E_MINE_${suffix.toUpperCase()}`
  await secrets.getByRole("button", { name: "Add secret" }).click()
  const dialog = page.getByRole("dialog", { name: "Add secret" })
  await expect(dialog.getByLabel("Scope")).toContainText(`Teammate ${teammate}`)
  await dialog.getByLabel("Name", { exact: true }).fill(added)
  await dialog.getByLabel("Value", { exact: true }).fill("e2e-value")
  await dialog.getByLabel("Hosts", { exact: true }).fill("api.example.com")
  await dialog.getByRole("button", { name: "Add secret" }).click()
  await expect(dialog).toBeHidden()
  await expect(rows.filter({ hasText: added })).toContainText(teammate)
  const list = await api.json<
    { name: string; teammateId?: string; sandboxId?: string }[]
  >("GET", `/api/environments/${env}/secrets`)
  const created = list!.find((s) => s.name === added)!
  expect(created.teammateId).toBe(teammateId)
  expect(created.sandboxId).toBeFalsy()
})

// A host that keeps answering 401 to a secret's value raises a card; Update secret opens it.
test("a secret a host keeps refusing asks to be updated", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = sandboxName("rej")
  const id = await api.createSandbox(name)
  const secret = `E2E_REFUSED_${Math.random().toString(36).slice(2, 8).toUpperCase()}`
  const created = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/secrets`,
    { name: secret, value: "expired", hosts: ["registry.example.org"] }
  ))!
  try {
    await page.goto(`/e/${env}`)
    for (let i = 0; i < 3; i++)
      await api.json("POST", "/api/e2e/rejected", {
        sandboxId: id,
        host: "registry.example.org",
        names: [secret],
      })
    const card = approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: `registry.example.org keeps refusing ${secret}` })
    await expect(card.getByText(/probably wrong or expired/)).toBeVisible()
    await card.getByRole("button", { name: "Update secret" }).click()
    await expect(page).toHaveURL(/\/settings\/secrets\?edit=/)
    const dialog = page.getByRole("dialog", { name: "Edit secret" })
    await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(secret)
    await expect(card).toHaveCount(0)
    await page.keyboard.press("Escape")
    await expect(page).toHaveURL(/\/settings\/secrets$/)
  } finally {
    await api.request.delete(`/api/environments/${env}/secrets/${created.id}`)
  }
})

test("an AWS access key gives a sandbox a profile, and its credentials are asked for", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = sandboxName("aws")
  const id = await api.createSandbox(name)
  const secret = `E2E_AWS_${Math.random().toString(36).slice(2, 8).toUpperCase()}`

  await page.goto(`/e/${env}/settings/secrets`)
  await page.getByRole("button", { name: "Add secret" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add secret" })
  await dialog.getByLabel("Kind", { exact: true }).click()
  await page.getByRole("option", { name: "AWS access key" }).click()
  await expect(dialog.getByLabel("Hosts", { exact: true })).toHaveCount(0)
  await dialog.getByLabel("Name", { exact: true }).fill(secret)
  await dialog
    .getByLabel("Access key ID", { exact: true })
    .fill("AKIAE2EEXAMPLE00001")
  await dialog
    .getByLabel("Secret access key", { exact: true })
    .fill("e2e-not-a-real-secret-access-key")
  await dialog.getByRole("button", { name: "Add secret" }).click()
  await expect(dialog).toBeHidden()

  const secrets = await api.json<{ id: string; name: string; kind?: string }[]>(
    "GET",
    `/api/environments/${env}/secrets`
  )
  const created = secrets!.find((s) => s.name === secret)!
  expect(created.kind).toBe("aws")
  try {
    await expect(
      page.getByRole("row").filter({ hasText: secret })
    ).toContainText("Short-lived credentials")

    // No placeholder in the environment: a profile, which AWS_PROFILE picks.
    await expect
      .poll(async () => (await api.guest(id)).env.AWS_PROFILE)
      .toBe(secret.toLowerCase())
    const guest = await api.guest(id)
    expect(guest.env[secret]).toBeUndefined()
    expect(guest.homeFiles).toContain(".aws/config")

    // The tool's helper asks; the user denies, so Studio asks AWS nothing.
    await page.goto(`/e/${env}`)
    const call = api.json<{ id: number }>(
      "POST",
      `/api/e2e/sandboxes/${id}/calls`,
      {
        method: "credential",
        params: { secret, format: "process" },
        wait: false,
      }
    )
    const n = (await call)!.id
    const card = approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: `asks for credentials from ${secret}` })
    await expect(card.getByRole("button", { name: "Always" })).toBeVisible()
    await card.getByRole("button", { name: "Deny" }).click()
    await expect(card).toHaveCount(0)
    await expect
      .poll(
        async () =>
          (
            await api.json<{ done: boolean; error?: string }>(
              "GET",
              `/api/e2e/sandboxes/${id}/calls/${n}`
            )
          )?.error ?? ""
      )
      .toContain("didn't let this sandbox have credentials")
  } finally {
    await api.request.delete(`/api/environments/${env}/secrets/${created.id}`)
  }
})
