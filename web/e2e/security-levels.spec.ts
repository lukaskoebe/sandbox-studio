import { approvals, expect, sandboxName, test } from "./fixtures"

test("the security level decides what no rule covers, and a curated entry can be switched off", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const id = await api.createSandbox(sandboxName("levels"))
  const level = (name: string) =>
    page
      .getByRole("group", { name: "Security level" })
      .getByRole("button", { name, exact: true })
  const held = (host: string) =>
    approvals(page).getByTestId("approval-card").filter({ hasText: host })

  await page.goto(`/e/${env}/network/rules`)
  await expect(
    page.getByRole("heading", { name: "Security level", exact: true })
  ).toBeVisible()
  await expect(level("Guarded")).toHaveAttribute("aria-pressed", "true")

  // Guarded allows a curated entry without asking, and Activity says why.
  await api.connect(id, "deb.debian.org", 80)
  await expect
    .poll(async () =>
      (await api.guest(id)).connections.find((c) => c.host === "deb.debian.org")
    )
    .toMatchObject({ done: true, status: 200 })
  await page.goto(`/e/${env}/network/activity`)
  await expect(
    page
      .getByRole("row")
      .filter({ hasText: "deb.debian.org:80" })
      .filter({ hasText: "Allowed · curated list" })
  ).toBeVisible()

  // Locked asks for every host, curated or not.
  await page.goto(`/e/${env}/network/rules`)
  await level("Locked").click()
  await expect(level("Locked")).toHaveAttribute("aria-pressed", "true")
  await api.connect(id, "security.debian.org", 80)
  await expect(held("security.debian.org")).toBeVisible()
  await api.dismissApprovals()

  // Switching the entry off makes Guarded ask for its hosts too.
  await level("Guarded").click()
  await expect(level("Guarded")).toHaveAttribute("aria-pressed", "true")
  const debian = page.getByRole("switch", { name: "Allow Debian archives" })
  await debian.click()
  await expect(debian).not.toBeChecked()
  await api.connect(id, "deb.debian.org", 80)
  await expect(held("deb.debian.org")).toBeVisible()

  // Back to the defaults for the tests after this one.
  await api.dismissApprovals()
  await debian.click()
  await expect(debian).toBeChecked()
})
