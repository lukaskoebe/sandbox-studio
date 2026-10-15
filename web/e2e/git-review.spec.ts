import { approvals, expect, sandboxName, test, type Api } from "./fixtures"

/**
 * A forge of kind https on a host of its own, and its upstream repository: a bare repository
 * the e2e build's fake forge serves. Returns the sandbox's URL for it on Studio's git remote.
 */
async function forgeWithRepo(api: Api, env: string) {
  const suffix = Math.random().toString(36).slice(2, 8)
  const name = `up-${suffix}`
  const host = `forge-${suffix}.example.com`
  const initial = await api.createUpstream(`${host}/team/app`)
  await api.json("POST", `/api/environments/${env}/forges`, {
    name,
    kind: "https",
    baseUrl: `https://${host}`,
    token: "e2e-forge-token",
  })
  return {
    repo: `${host}/team/app`,
    url: `https://git.studio.internal/${name}/team/app.git`,
    initial: initial.find((b) => b.name === "main")!,
  }
}

test("a push to the virtual remote waits for review, and approving it pushes upstream", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName("push"))
  await api.guestReady(id)

  const pushed = await api.push(id, {
    url: forge.url,
    file: "hello.txt",
    content: "hello from the sandbox\n",
    message: "Add a greeting",
  })
  expect(pushed.error, pushed.output).toBeUndefined()
  expect(pushed.output).toContain("queued for review")
  // Nothing reached the forge yet.
  expect(await api.upstream(forge.repo)).toEqual([forge.initial])

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
  await expect(card).toBeVisible()
  await card.getByRole("button", { name: "Review" }).click()

  const sheet = page.getByRole("dialog")
  await expect(sheet).toContainText("Add a greeting")
  await expect(sheet).toContainText("+hello from the sandbox")
  await expect(sheet.getByRole("link", { name: "Open as page" })).toBeVisible()
  await sheet.getByRole("button", { name: "Approve and push" }).click()
  await expect(sheet).toBeHidden()
  await expect(card).toBeHidden()

  await expect
    .poll(async () =>
      (await api.upstream(forge.repo)).find((b) => b.name === "main")
    )
    .toEqual({ name: "main", sha: pushed.commit, subject: "Add a greeting" })

  // The Git page, from the sidebar, lists the push with its outcome and links to its page,
  // which shows the decided review without the buttons.
  await page.getByRole("link", { name: "Git", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/git$`))
  const row = page.getByRole("row").filter({ hasText: "team/app" })
  await expect(row).toContainText("pushed")
  await row.getByRole("link").click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/git/reviews/`))
  await expect(page.getByRole("status")).toContainText("This was approved.")
  await expect(page.getByText("Add a greeting")).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Approve and push" })
  ).toHaveCount(0)
})

test("a pending push review opens as a page and is decided there", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName("page"))
  await api.guestReady(id)
  const pushed = await api.push(id, {
    url: forge.url,
    file: "notes.md",
    content: "# Notes\n",
    message: "Start the notes",
  })
  expect(pushed.error, pushed.output).toBeUndefined()

  // The sidebar's Git link counts the push while it waits.
  await page.goto(`/e/${env}`)
  await expect(
    page.getByRole("link", { name: /^Git/ }).locator('[data-status="waiting"]')
  ).toBeVisible()

  // The sheet's "Open as page" leads to the review's own address.
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
  await card.getByRole("button", { name: "Review" }).click()
  await page.getByRole("link", { name: "Open as page" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/git/reviews/[^/]+$`))
  await expect(page.getByRole("dialog")).toHaveCount(0)

  const main = page.getByRole("main")
  await expect(main).toContainText("Start the notes")
  await expect(main).toContainText("+# Notes")
  await page.getByLabel("Note for the agent").fill("Not this one")
  await page.getByRole("button", { name: "Reject" }).click()
  await expect(page.getByRole("status")).toContainText("This was rejected.")
  await expect(main).toContainText("Note: Not this one")
  expect(await api.upstream(forge.repo)).toEqual([forge.initial])
})

test("a review that doesn't exist says so", async ({ page, env }) => {
  await page.goto(`/e/${env}/git/reviews/no-such-review`)
  await expect(page.getByText("No such review")).toBeVisible()
  await page.getByRole("link", { name: "All pushes" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/git$`))
})
