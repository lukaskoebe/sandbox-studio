import { approvals, expect, sandboxName, test } from "./fixtures"

test("a held connection asks; Allow adds a rule and clears the card in every tab", async ({
  page,
  context,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("net"))
  const host = `api-${Math.random().toString(36).slice(2, 8)}.example.org`

  await page.goto(`/e/${env}`)
  const other = await context.newPage()
  await other.goto(`/e/${env}`)
  await api.connect(id, host)

  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  const otherCard = approvals(other)
    .getByTestId("approval-card")
    .filter({ hasText: host })
  await expect(card).toBeVisible()
  await expect(otherCard).toBeVisible()

  await card.getByRole("button", { name: "Allow" }).click()
  await expect(card).toBeHidden()
  await expect(otherCard).toBeHidden()

  // The held connection went through once allowed.
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.find((c) => c.host === host)
    )
    .toMatchObject({ done: true, status: 200, body: `hello from ${host}\n` })

  await page.goto(`/e/${env}/network`)
  await expect(
    page.getByRole("cell", { name: host, exact: true })
  ).toBeVisible()
})
