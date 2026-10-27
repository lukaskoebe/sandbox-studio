import { approvals, expect, sandboxName, test, type Api } from "./fixtures"

type Posted = { repo: string; body: Record<string, unknown> }

/**
 * A Forgejo forge on a host of its own. The e2e build's fake forge has pull request 1 on
 * every repository, changing web/coupon.ts, and keeps the reviews posted to it.
 */
async function forgejo(api: Api, env: string) {
  const suffix = Math.random().toString(36).slice(2, 8)
  const name = `fj-${suffix}`
  const host = `forgejo-${suffix}.example.com`
  await api.json("POST", `/api/environments/${env}/forges`, {
    name,
    kind: "forgejo",
    baseUrl: `https://${host}`,
    token: "e2e-forge-token",
  })
  return { name, repo: `${host}/acme/web` }
}

async function draftReview(api: Api, sb: string, forge: string) {
  const call = (await api.json<{ result: Record<string, unknown> }>(
    "POST",
    `/api/e2e/sandboxes/${sb}/calls`,
    {
      method: "git_pull_request_review",
      params: {
        forge,
        repository: "acme/web",
        number: 1,
        verdict: "request_changes",
        summary: "The coupon is checked against the subtotal.",
        comments: [
          {
            path: "web/coupon.ts",
            line: 22,
            body: "Validate against finalTotal(cart).",
          },
        ],
      },
      wait: true,
    }
  ))!
  expect(call.result.status).toBe("awaiting approval")
}

test("a teammate's pull request review waits for approval and posts as edited", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgejo(api, env)
  const name = sandboxName("review")
  const sb = await api.createSandbox(name)
  await draftReview(api, sb, forge.name)

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "acme/web#1" })
  await expect(card).toContainText(
    "Review acme/web#1: request changes, 1 comment"
  )
  await card.getByRole("button", { name: "Review" }).click()

  const sheet = page.getByRole("dialog")
  await expect(sheet).toContainText("Apply coupon codes")
  await expect(sheet).toContainText("feature/coupons → main")
  await expect(sheet.getByRole("note")).toContainText(
    "Validate against finalTotal(cart)."
  )
  await expect(sheet).toContainText("Posts as e2e-user")
  // Edit the draft: a comment instead, a longer summary, the attribution line.
  await sheet.getByRole("button", { name: "Comment", exact: true }).click()
  const summary = sheet.getByRole("textbox", { name: "Summary" })
  await summary.fill(
    "The coupon is checked against the subtotal; see the line."
  )
  await sheet.getByRole("checkbox", { name: /Review by an agent/ }).click()
  await sheet.getByRole("button", { name: "Post review" }).click()
  await expect(sheet).toBeHidden()
  await expect(card).toBeHidden()

  await expect
    .poll(async () =>
      ((await api.json<Posted[]>("GET", "/api/e2e/reviews")) ?? []).find(
        (r) => r.repo === forge.repo
      )
    )
    .toMatchObject({
      body: {
        event: "COMMENT",
        commit_id: "c0ffee",
        comments: [{ path: "web/coupon.ts", new_position: 22 }],
      },
    })
  const posted = ((await api.json<Posted[]>("GET", "/api/e2e/reviews")) ?? [])
    .filter((r) => r.repo === forge.repo)
    .at(-1)!
  expect(posted.body.body).toBe(
    `The coupon is checked against the subtotal; see the line.\n\n— Review by an agent in ${name}`
  )

  // Activity links to the decided review, which says where it was posted.
  await page.goto(`/e/${env}/settings/activity?kind=git`)
  // Other tests in the environment review the same pull request.
  const rows = page.getByRole("row").filter({ hasText: "acme/web#1" })
  await expect(
    rows.getByRole("link", { name: "Review posted" }).first()
  ).toBeVisible()
})

test("discarding a review posts nothing and keeps the note", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgejo(api, env)
  const sb = await api.createSandbox(sandboxName("discard"))
  await draftReview(api, sb, forge.name)

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "acme/web#1" })
  await card.getByRole("button", { name: "Review" }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("button", { name: "Discard…" }).click()
  await page
    .getByRole("textbox", { name: /^Note for/ })
    .fill("Not blocking; leave it to the author.")
  await page.getByRole("button", { name: "Discard the review" }).click()
  await expect(card).toBeHidden()

  await page.goto(`/e/${env}/settings/activity?kind=git`)
  await expect(
    page.getByRole("row").filter({ hasText: "acme/web#1" }).filter({
      hasText: "Discarded · Note: Not blocking; leave it to the author.",
    })
  ).toHaveCount(1)
  const posted = (await api.json<Posted[]>("GET", "/api/e2e/reviews")) ?? []
  expect(posted.filter((r) => r.repo === forge.repo)).toEqual([])
})
