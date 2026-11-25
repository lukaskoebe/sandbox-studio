import {
  openRow,
  approvals,
  expect,
  sandboxName,
  test,
  type Api,
} from "./fixtures"

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

async function call<T>(
  api: Api,
  sb: string,
  method: string,
  params: Record<string, unknown>
) {
  return (await api.json<{ result: T }>(
    "POST",
    `/api/e2e/sandboxes/${sb}/calls`,
    { method, params, wait: true }
  ))!.result
}

test("an agent finds the repositories and their pull requests to review", async ({
  api,
  env,
}) => {
  const forge = await forgejo(api, env)
  const sb = await api.createSandbox(sandboxName("prlist"))

  const repos = await call<{
    forges: {
      name: string
      pullRequests: boolean
      remote: string
      repositories?: { path: string }[]
    }[]
  }>(api, sb, "git_repositories", { forge: forge.name })
  expect(repos.forges).toEqual([
    expect.objectContaining({
      name: forge.name,
      pullRequests: true,
      remote: `https://git.studio.internal/${forge.name}/`,
      repositories: [
        { path: "acme/web", description: "The shop" },
        { path: "acme/api", private: true },
      ],
    }),
  ])

  const list = await call<{ pullRequests: Record<string, unknown>[] }>(
    api,
    sb,
    "git_pull_request_list",
    { forge: forge.name, repository: "acme/web" }
  )
  expect(list.pullRequests).toEqual([
    expect.objectContaining({
      number: 1,
      title: "Apply coupon codes",
      author: "someone",
      head: "feature/coupons",
      base: "main",
    }),
  ])
  const closed = await call<{ pullRequests: unknown[] }>(
    api,
    sb,
    "git_pull_request_list",
    { forge: forge.name, repository: "acme/web", state: "closed" }
  )
  expect(closed.pullRequests).toEqual([])

  const pr = await call<{ title: string; files: { path: string }[] }>(
    api,
    sb,
    "git_pull_request_read",
    { forge: forge.name, repository: "acme/web", number: 1 }
  )
  expect(pr.title).toBe("Apply coupon codes")
  expect(pr.files).toEqual([expect.objectContaining({ path: "web/coupon.ts" })])
})

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
  await card.getByRole("button", { name: "Review", exact: true }).click()

  const pane = page.getByTestId("git-review")
  await expect(pane).toContainText("Apply coupon codes")
  await expect(pane).toContainText("feature/coupons → main")
  await expect(pane.getByRole("note")).toContainText(
    "Validate against finalTotal(cart)."
  )
  await expect(pane).toContainText("Posts as e2e-user")
  // The diff is highlighted as TypeScript.
  await expect(
    pane.locator(`span[style*="--code-keyword"]`, { hasText: "export" }).first()
  ).toBeVisible()
  // Edit the draft: a comment instead, a longer summary, the attribution line.
  await pane.getByRole("button", { name: "Comment", exact: true }).click()
  const summary = pane.getByRole("textbox", { name: "Summary" })
  await summary.fill(
    "The coupon is checked against the subtotal; see the line."
  )
  // The edits outlive a reload, and go once the review is posted.
  await page.reload()
  await expect(summary).toHaveValue(
    "The coupon is checked against the subtotal; see the line."
  )
  await expect(
    pane.getByRole("button", { name: "Comment", exact: true })
  ).toHaveAttribute("aria-pressed", "true")
  await pane.getByRole("checkbox", { name: /Review by an agent/ }).click()
  await pane.getByRole("button", { name: "Post review" }).click()
  await expect(pane).toBeHidden()
  await expect(card).toBeHidden()
  await expect
    .poll(() =>
      page.evaluate(() =>
        Object.keys(localStorage).filter((k) => k.startsWith("studio.review."))
      )
    )
    .toEqual([])

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
  await page.goto(`/e/${env}/activity?kind=git`)
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
  await card.getByRole("button", { name: "Review", exact: true }).click()
  const pane = page.getByTestId("git-review")
  await pane.getByRole("button", { name: "Discard…" }).click()
  await page
    .getByRole("textbox", { name: /^Note for/ })
    .fill("Not blocking; leave it to the author.")
  await page.getByRole("button", { name: "Discard the review" }).click()
  await expect(card).toBeHidden()

  await page.goto(`/e/${env}/activity?kind=git`)
  await expect(
    page.getByRole("row").filter({ hasText: "acme/web#1" }).filter({
      hasText: "Discarded · Note: Not blocking; leave it to the author.",
    })
  ).toHaveCount(1)
  const posted = (await api.json<Posted[]>("GET", "/api/e2e/reviews")) ?? []
  expect(posted.filter((r) => r.repo === forge.repo)).toEqual([])
})

test("a teammate's ask to resolve a review thread waits for approval", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const suffix = Math.random().toString(36).slice(2, 8)
  const host = `github-${suffix}.example.com`
  const forge = `gh-${suffix}`
  await api.json("POST", `/api/environments/${env}/forges`, {
    name: forge,
    kind: "github",
    baseUrl: `https://${host}`,
    token: "e2e-forge-token",
  })
  const sb = await api.createSandbox(sandboxName("resolve"))
  const pr = { forge, repository: "acme/web", number: 1 }
  type Read = { lineComments: { thread?: string; resolved?: boolean }[] }
  const read = await call<Read>(api, sb, "git_pull_request_read", pr)
  const thread = read.lineComments[0].thread!
  expect(thread).toMatch(/^PRRT_/)
  expect(read.lineComments[0].resolved).toBeFalsy()
  const asked = await call<{ status: string; approval: string }>(
    api,
    sb,
    "git_pull_request_resolve",
    { ...pr, threads: [thread] }
  )
  expect(asked.status).toBe("awaiting approval")

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "acme/web#1" })
  await expect(card).toContainText(
    "Resolve 1 review thread on acme/web#1: Apply coupon codes"
  )
  await openRow(card)
  await expect(card).toContainText("web/coupon.ts:22")
  await card.getByRole("button", { name: "Review", exact: true }).click()
  const pane = page.getByTestId("git-review")
  await expect(pane).toContainText("Resolves as e2e-user")
  await expect(pane).toContainText("Validate against finalTotal(cart).")
  await pane.getByRole("button", { name: "Resolve thread" }).click()
  await expect(pane).toBeHidden()
  await expect(card).toBeHidden()

  const posted = ((await api.json<Posted[]>("GET", "/api/e2e/reviews")) ?? [])
    .filter((r) => r.repo === `${host}/acme/web`)
    .at(-1)!
  expect(posted.body.resolved).toBe(thread)
  const status = await call<{ settled: boolean; result: { status: string } }>(
    api,
    sb,
    "approval_status",
    { approval: asked.approval }
  )
  expect(status.settled).toBe(true)
  expect(status.result.status).toBe("resolved")
  const again = await call<Read>(api, sb, "git_pull_request_read", pr)
  expect(again.lineComments[0].resolved).toBe(true)
})

test("a teammate's comment on a pull request waits for approval and posts as edited", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgejo(api, env)
  const name = sandboxName("comment")
  const sb = await api.createSandbox(name)
  const asked = await call<{ status: string; approval: string }>(
    api,
    sb,
    "git_pull_request_comment",
    {
      forge: forge.name,
      repository: "acme/web",
      number: 1,
      body: "The coupon tests fail on main too.",
    }
  )
  expect(asked.status).toBe("awaiting approval")

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "acme/web#1" })
  await expect(card).toContainText("Comment on acme/web#1: Apply coupon codes")
  await openRow(card)
  await expect(card).toContainText("The coupon tests fail on main too.")
  await card.getByRole("button", { name: "Review", exact: true }).click()
  const pane = page.getByTestId("git-review")
  await expect(pane).toContainText("Posts as e2e-user")
  await pane
    .getByRole("textbox", { name: "Comment" })
    .fill("The coupon tests fail on main too; see #40.")
  await pane.getByRole("checkbox", { name: /Comment by an agent/ }).click()
  await pane.getByRole("button", { name: "Post comment" }).click()
  await expect(pane).toBeHidden()
  await expect(card).toBeHidden()

  const posted = ((await api.json<Posted[]>("GET", "/api/e2e/reviews")) ?? [])
    .filter((r) => r.repo === forge.repo)
    .at(-1)!
  expect(posted.body.comment).toBe(
    `The coupon tests fail on main too; see #40.\n\n— Comment by an agent in ${name}`
  )
  const status = await call<{ settled: boolean; result: { url: string } }>(
    api,
    sb,
    "approval_status",
    { approval: asked.approval }
  )
  expect(status.settled).toBe(true)
  expect(status.result.url).toContain("#issuecomment-")

  await page.goto(`/e/${env}/activity?kind=git`)
  await expect(
    page
      .getByRole("row")
      .filter({ hasText: "Comment on acme/web#1" })
      .getByRole("link", { name: "Comment posted" })
      .first()
  ).toBeVisible()
})

test("a teammate's comment shows its pictures and uploads them once posted", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgejo(api, env)
  const sb = await api.createSandbox(sandboxName("picture"))
  const asked = await call<{ status: string; approval: string }>(
    api,
    sb,
    "git_pull_request_comment",
    {
      forge: forge.name,
      repository: "acme/web",
      number: 1,
      body: "The total is off:",
      attach: ["/home/agent/shots/checkout.png"],
    }
  )
  expect(asked.status).toBe("awaiting approval")

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "acme/web#1" })
  await openRow(card)
  await card.getByRole("button", { name: "Review", exact: true }).click()
  const pane = page.getByTestId("git-review")
  await expect(pane.getByRole("textbox", { name: "Comment" })).toHaveValue(
    "The total is off:\n\n{{attach:/home/agent/shots/checkout.png}}"
  )
  const pictures = pane.getByRole("region", { name: "Pictures" })
  await expect(pictures).toContainText("checkout.png")
  const thumb = pictures.getByRole("img", {
    name: "/home/agent/shots/checkout.png",
  })
  await expect
    .poll(() => thumb.evaluate((i: HTMLImageElement) => i.naturalWidth))
    .toBeGreaterThan(0)
  await pane.getByRole("button", { name: "Post comment" }).click()
  await expect(pane).toBeHidden()

  const posted = (
    (await api.json<Posted[]>("GET", "/api/e2e/reviews")) ?? []
  ).filter((r) => r.repo === forge.repo)
  expect(posted.at(-2)!.body).toMatchObject({
    asset: "checkout.png",
    type: "image/png",
  })
  expect(posted.at(-1)!.body.comment).toBe(
    `The total is off:\n\n![checkout](https://${forge.repo}/attachments/checkout.png)`
  )
})
