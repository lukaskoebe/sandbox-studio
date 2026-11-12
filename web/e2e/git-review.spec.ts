import {
  addTeammate,
  approvals,
  expect,
  sandboxName,
  terminal,
  test,
  type Api,
} from "./fixtures"
import type { Locator, Page } from "@playwright/test"
import { execFileSync } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

/**
 * A forge of kind https on a host of its own, and its upstream repository: a bare repository
 * the e2e build's fake forge serves. Returns the sandbox's URL for it on Studio's git remote.
 */
async function forgeWithRepo(api: Api, env: string, token = "e2e-forge-token") {
  const suffix = Math.random().toString(36).slice(2, 8)
  const name = `up-${suffix}`
  const host = `forge-${suffix}.example.com`
  const initial = await api.createUpstream(`${host}/team/app`)
  const forge = await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/forges`,
    {
      name,
      kind: "https",
      baseUrl: `https://${host}`,
      token,
    }
  )
  return {
    id: forge!.id,
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
  await expect(sheet).toContainText("hello from the sandbox")
  await expect(sheet.getByRole("link", { name: "Open as page" })).toBeVisible()
  await sheet.getByRole("button", { name: "Approve and push" }).click()
  await expect(sheet).toBeHidden()
  await expect(card).toBeHidden()

  await expect
    .poll(async () =>
      (await api.upstream(forge.repo)).find((b) => b.name === "main")
    )
    .toEqual({ name: "main", sha: pushed.commit, subject: "Add a greeting" })

  // Settings → Activity lists the push with its outcome and links to its page, which shows
  // the decided review without the buttons.
  await page.goto(`/e/${env}/settings/activity?kind=git`)
  const row = page.getByRole("row").filter({ hasText: "team/app" })
  await expect(row).toContainText("pushed")
  await row.getByRole("link").click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/git/reviews/`))
  await expect(page.getByRole("status")).toContainText("This was approved.")
  await expect(page.getByText("hello from the sandbox")).toBeVisible()
  await page.getByRole("tab", { name: /^Commits/ }).click()
  await expect(page.getByText("Add a greeting")).toBeVisible()
  // A decided push has no comments to write.
  await expect(page.getByRole("tab", { name: /^Comments/ })).toHaveCount(0)
  await expect(
    page.getByRole("button", { name: "Approve and push" })
  ).toHaveCount(0)
})

test("a push the forge refuses says why, and goes up when pushed again with a token that writes", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  // The e2e upstream lets this token read but refuses it for pushes.
  const forge = await forgeWithRepo(api, env, "e2e-read-only-token")
  const tested = await api.json<{ ok: boolean; pushRefused?: string }>(
    "POST",
    `/api/environments/${env}/forges/${forge.id}/test`,
    { repo: "team/app" }
  )
  expect(tested!.ok).toBe(true)
  expect(tested!.pushRefused).toContain("write access")

  const id = await api.createSandbox(sandboxName("refused"))
  await api.guestReady(id)
  const pushed = await api.push(id, {
    url: forge.url,
    file: "hello.txt",
    content: "hello\n",
    message: "Add a greeting",
  })
  expect(pushed.error, pushed.output).toBeUndefined()

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
  await card.getByRole("button", { name: "Review" }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("button", { name: "Approve and push" }).click()
  await expect(sheet).toBeHidden()

  // The approval is gone, but a toast says the push failed and why, and stays.
  const toast = page
    .getByRole("listitem")
    .filter({ hasText: "The push of main to team/app failed" })
  await expect(toast).toBeVisible()
  await expect(toast).toContainText("write access")
  expect(await api.upstream(forge.repo)).toEqual([forge.initial])
  await toast.getByRole("button", { name: "Open" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/git/reviews/`))
  await expect(page.getByRole("status")).toContainText(
    "This was approved, but the push failed"
  )
  const alert = page
    .getByRole("alert")
    .filter({ hasText: "didn’t go upstream" })
  await expect(alert).toContainText("Write access to repository not granted.")

  // With a token that can write, pushing again puts the commit upstream.
  await api.json("PUT", `/api/environments/${env}/forges/${forge.id}/token`, {
    token: "e2e-forge-token",
  })
  await alert.getByRole("button", { name: "Push again" }).click()
  await expect(
    page.getByRole("listitem").filter({ hasText: "Pushed main to team/app" })
  ).toBeVisible()
  await expect(alert).toBeHidden()
  await expect
    .poll(async () =>
      (await api.upstream(forge.repo)).find((b) => b.name === "main")
    )
    .toEqual({ name: "main", sha: pushed.commit, subject: "Add a greeting" })
})

test("a rewritten branch is reviewed with what it drops and takes a force-push", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName("force"))
  await api.guestReady(id)
  const first = await api.push(id, {
    url: forge.url,
    branch: "topic",
    from: "main",
    file: "coupon.txt",
    content: "field\n",
    message: "Add the coupon field",
  })
  expect(first.error, first.output).toBeUndefined()
  const approval = /approval (\S+)\)/.exec(first.output)![1]
  await api.json(
    "POST",
    `/api/environments/${env}/approvals/${approval}/decide`,
    { action: "allow" }
  )
  await expect
    .poll(async () =>
      (await api.upstream(forge.repo)).find((b) => b.name === "topic")
    )
    .toMatchObject({ sha: first.commit })

  // The agent amends the commit and force-pushes the branch.
  const amended = await api.push(id, {
    url: forge.url,
    branch: "topic",
    amend: true,
    file: "coupon.txt",
    content: "field v2\n",
    message: "Add the coupon field",
  })
  expect(amended.error, amended.output).toBeUndefined()

  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "Force-push 1 commit to topic" })
  await card.getByRole("button", { name: "Review" }).click()
  const sheet = page.getByRole("dialog")
  await expect(
    sheet.getByText("rewrites history", { exact: true })
  ).toBeVisible()
  const rewrite = sheet.getByRole("region", { name: "Rewrites history" })
  await expect(rewrite).toContainText("drops 1 commit now on the forge")
  await expect(rewrite).toContainText(
    `${first.commit!.slice(0, 10)}Add the coupon field→ ${amended.commit!.slice(0, 10)}`
  )
  await expect(rewrite).toContainText("differ in coupon.txt")
  await expect(sheet).toContainText("field v2")
  await expect(
    sheet.getByRole("button", { name: "Approve and push" })
  ).toHaveCount(0)
  await sheet.getByRole("button", { name: "Force-push" }).click()
  await expect(sheet).toBeHidden()
  await expect
    .poll(async () =>
      (await api.upstream(forge.repo)).find((b) => b.name === "topic")
    )
    .toMatchObject({ sha: amended.commit })
})

test("a pending push review opens as a page and is decided there", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const name = sandboxName("page")
  const id = await api.createSandbox(name)
  await api.guestReady(id)
  const pushed = await api.push(id, {
    url: forge.url,
    file: "notes.md",
    content: "# Notes\n",
    message: "Start the notes",
  })
  expect(pushed.error, pushed.output).toBeUndefined()

  // The sidebar counts the push on its computer's row while it waits.
  await page.goto(`/e/${env}`)
  await expect(
    page
      .locator('[data-sidebar="sidebar"]')
      .getByRole("link", { name: new RegExp(`^${name}`) })
      .getByTitle("1 waiting for you")
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
  await expect(main).toContainText("# Notes")
  await page.getByRole("tab", { name: /^Comments/ }).click()
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
  await expect(page).toHaveURL(
    new RegExp(`/e/${env}/settings/activity\\?kind=git$`)
  )
})

// Review feedback goes straight to the agent: line comments and the outcome as a prompt of
// its chat, or pasted into its terminal.
test("review comments are sent to the agent's chat, terminal or a new chat", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const teammate = await addTeammate(
    api,
    env,
    `Reviewed ${Math.random().toString(36).slice(2, 6)}`
  )
  const id = await api.createSandbox(sandboxName("fb"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  const session = (await api.json<{ name: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/sessions`
  ))!

  const review = async (file: string, message: string) => {
    const pushed = await api.push(id, {
      url: forge.url,
      file,
      content: `${message}\n`,
      message,
    })
    expect(pushed.error, pushed.output).toBeUndefined()
    await page.goto(`/e/${env}`)
    await approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: "team/app" })
      .getByRole("button", { name: "Review" })
      .click()
    return page.getByRole("dialog")
  }

  // A line comment, sent to the chat with the rejection.
  let sheet = await review("greeting.txt", "Add a greeting")
  await commentOn(sheet, sheet.locator("[data-line]").first())
  await sheet
    .getByRole("textbox", { name: "Comment on greeting.txt:1" })
    .fill("Say hello to the world, please")
  await sheet.getByRole("button", { name: "Save comment" }).click()
  await expect(
    sheet.getByRole("note", { name: "Comment on greeting.txt:1" })
  ).toContainText("Say hello to the world")
  // Comments lists it, and jumps back to its line.
  await sheet.getByRole("tab", { name: /^Comments/ }).click()
  const listed = sheet.getByRole("list", { name: "Line comments" })
  await expect(listed).toContainText("greeting.txt:1")
  await listed.getByRole("button", { name: /Say hello to the world/ }).click()
  await expect(sheet.getByRole("tab", { name: "Changes" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  await expect(
    sheet.getByRole("note", { name: "Comment on greeting.txt:1" })
  ).toBeInViewport()
  await sheet.getByRole("combobox", { name: "Send to" }).click()
  await page.getByRole("option", { name: /^Chat/ }).click()
  await sheet.getByRole("button", { name: "Reject" }).click()
  await expect(page.getByText("Feedback sent to the agent")).toBeVisible()

  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  await expect(main).toContainText("Review of your push")
  await expect(main).toContainText("rejected; nothing went upstream.")
  await expect(main).toContainText("greeting.txt:1")
  await expect(main).toContainText("Say hello to the world, please")

  // A note, pasted into the agent's terminal without deciding.
  sheet = await review("notes.txt", "Add notes")
  await sheet.getByRole("tab", { name: /^Comments/ }).click()
  await sheet
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("Use markdown for the notes")
  await sheet.getByRole("combobox", { name: "Send to" }).click()
  await page.getByRole("option", { name: /^Terminal/ }).click()
  await sheet.getByRole("button", { name: "More decisions" }).click()
  await page.getByRole("menuitem", { name: /^Send without deciding/ }).click()
  await expect(page.getByText("Feedback sent to the agent")).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Reject" })).toBeVisible()

  await page.goto(
    `/e/${env}/teammates/${teammate}?computer=${id}&tab=terminal&term=${session.name}`
  )
  const rows = terminal(page).rows
  await expect(rows).toContainText("is still waiting for my review")
  await expect(rows).toContainText("Use markdown for the notes")

  // A note in a new chat, the default for a teammate's sandbox.
  sheet = await review("todo.txt", "Add a todo")
  await expect(sheet.getByRole("combobox", { name: "Send to" })).toContainText(
    "A new chat"
  )
  await sheet.getByRole("tab", { name: /^Comments/ }).click()
  await sheet
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("Keep the todo list short")
  await sheet.getByRole("button", { name: "Reject" }).click()
  await expect(page.getByText("Feedback sent to a new chat")).toBeVisible()
  const chats = await api.json<{ id: string }[]>(
    "GET",
    `/api/environments/${env}/sandboxes/${id}/chats`
  )
  const fresh = chats!.find((c) => c.id !== chat.id)!
  await page.goto(`/e/${env}/chats/${fresh.id}`)
  await expect(page.getByRole("main")).toContainText("Keep the todo list short")
})

test("a long diff is highlighted off the page's thread, and a very long line stays plain", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName("long-diff"))
  await api.guestReady(id)
  await api.push(id, {
    url: forge.url,
    file: "a.txt",
    content: "a\n",
    message: "A long change",
  })
  const code = Array.from(
    { length: 1000 },
    (_, i) => `+func f${i}() string { return "s${i}" }`
  )
  const long = `+var long = "${"x".repeat(3000)}"`
  const diff = [
    "diff --git a/main.go b/main.go",
    "--- a/main.go",
    "+++ b/main.go",
    "@@ -0,0 +1,1002 @@",
    long,
    ...code,
    "",
  ].join("\n")
  // The diff comes as if the push had changed that much.
  await page.route(/\/api\/.*approvals/, async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    const r = await route.fetch()
    const body = (await r.text()).replace(
      /"diff":"(?:[^"\\]|\\.)*"/g,
      () => `"diff":${JSON.stringify(diff)}`
    )
    await route.fulfill({ response: r, body })
  })
  await page.goto(`/e/${env}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
  await card.getByRole("button", { name: "Review" }).click()
  const sheet = page.getByRole("dialog")

  const line = sheet.locator("[data-line]")
  const first = line.filter({ hasText: 'func f0() string { return "s0" }' })
  await expect(first).toBeVisible()
  await expect(
    first.locator('span[style*="--code-keyword"]').first()
  ).toHaveText("func")
  await expect(first.locator('span[style*="--code-string"]')).toContainText(
    "s0"
  )
  const longLine = line.filter({ hasText: "var long" })
  await expect(longLine).toBeVisible()
  await expect(longLine.locator('span[style*="--code-"]')).toHaveCount(0)
  await expect(longLine).toHaveText(long.slice(1))
  // Only the lines near the view are on the page.
  expect(await line.count()).toBeLessThan(300)
})

/** Opens the comment editor of a row. Highlighting redraws a row, which drops the hover's button. */
async function commentOn(sheet: Locator, row: Locator) {
  await expect(async () => {
    await row.hover()
    await sheet
      .getByRole("button", { name: "Comment on this line" })
      .click({ timeout: 1000 })
  }).toPass()
}

/** Opens the review of a push whose diff comes as given. */
async function reviewWithDiff(
  { page, api, env }: { page: Page; api: Api; env: string },
  name: string,
  diff: string | (() => string)
) {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName(name))
  await api.guestReady(id)
  await api.push(id, {
    url: forge.url,
    file: "a.txt",
    content: "a\n",
    message: "A change",
  })
  await page.route(/\/api\/.*approvals/, async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    const r = await route.fetch()
    const body = (await r.text()).replace(
      /"diff":"(?:[^"\\]|\\.)*"/g,
      () => `"diff":${JSON.stringify(typeof diff === "string" ? diff : diff())}`
    )
    await route.fulfill({ response: r, body })
  })
  await page.goto(`/e/${env}`)
  await approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
    .getByRole("button", { name: "Review" })
    .click()
  return page.getByRole("dialog")
}

test("a diff that changes to one of the same length shows the new one", async ({
  page,
  api,
  env,
}) => {
  const change = (value: number) =>
    [
      "diff --git a/a.txt b/a.txt",
      "--- a/a.txt",
      "+++ b/a.txt",
      "@@ -1 +1 @@",
      "-v = 0",
      `+v = ${value}`,
      "",
    ].join("\n")
  let diff = change(1)
  const sheet = await reviewWithDiff(
    { page, api, env },
    "same-length",
    () => diff
  )
  const lines = sheet.locator("[data-line]")
  await expect(lines.filter({ hasText: "v = 1" })).toBeVisible()

  diff = change(2)
  // Another request makes the page fetch the approvals again.
  const other = await api.createSandbox(sandboxName("same-length-other"))
  await api.connect(other, `refetch-${Date.now()}.example.org`)
  await expect(lines.filter({ hasText: "v = 2" })).toBeVisible()
  await expect(lines.filter({ hasText: "v = 1" })).toHaveCount(0)
})

test("a diff shows Studio's notes and renames, and what it holds stays text", async ({
  page,
  api,
  env,
}) => {
  const sheet = await reviewWithDiff(
    { page, api, env },
    "diff-notes",
    [
      "diff --git a/big.bin b/big.bin",
      "[big.bin: 900000 bytes, larger than 512 KiB; not shown]",
      "diff --git a/old.txt b/new.txt",
      "similarity index 100%",
      "rename from old.txt",
      "rename to new.txt",
      "diff --git a/<img src=x onerror=window.pwned=1>.html b/<img src=x onerror=window.pwned=1>.html",
      "--- a/<img src=x onerror=window.pwned=1>.html",
      "+++ b/<img src=x onerror=window.pwned=1>.html",
      "@@ -1,2 +1,2 @@",
      "-<b>old</b>",
      "+<img src=x onerror=window.pwned=1>",
      " <script>window.pwned=1</script>",
      "",
      "[... diff truncated by Sandbox Studio: 3 more files not shown ...]",
      "",
    ].join("\n")
  )
  await expect(sheet).toContainText("big.bin: 900000 bytes")
  await expect(sheet).toContainText("new.txt")
  await expect(sheet).toContainText("3 more files not shown")
  await expect(
    sheet.locator("[data-line]").filter({ hasText: "<img src=x" })
  ).toBeVisible()
  await expect(
    sheet.locator("diffs-container img, diffs-container script")
  ).toHaveCount(0)
  expect(await page.evaluate(() => "pwned" in window)).toBe(false)
})

test("a comment lands on its line after a removed --- line, and a draft outlives scrolling away", async ({
  page,
  api,
  env,
}) => {
  const body = Array.from({ length: 3000 }, (_, i) => `+line ${i}`)
  const sheet = await reviewWithDiff(
    { page, api, env },
    "diff-comment",
    [
      "diff --git a/q.sql b/q.sql",
      "--- a/q.sql",
      "+++ b/q.sql",
      "@@ -1,2 +1,3001 @@",
      "--- an old comment",
      " keep",
      ...body,
      "",
    ].join("\n")
  )
  const row = sheet.locator("[data-line]").filter({ hasText: /^line 2$/ })
  await commentOn(sheet, row)
  const editor = sheet.getByRole("textbox", { name: "Comment on q.sql:4" })
  await editor.fill("A draft on the fourth line")

  await page.mouse.wheel(0, 20000)
  await expect(editor).toHaveCount(0)
  await page.mouse.wheel(0, -20000)
  await expect(editor).toHaveValue("A draft on the fourth line")
  await sheet.getByRole("button", { name: "Save comment" }).click()
  await expect(
    sheet.getByRole("note", { name: "Comment on q.sql:4" })
  ).toBeVisible()

  // The list jumps back to a line that left the page.
  await page.mouse.wheel(0, 20000)
  await expect(
    sheet.getByRole("note", { name: "Comment on q.sql:4" })
  ).toHaveCount(0)
  await sheet.getByRole("tab", { name: /^Comments/ }).click()
  await sheet
    .getByRole("list", { name: "Line comments" })
    .getByRole("button", { name: /A draft on the fourth line/ })
    .click()
  await expect(
    sheet.getByRole("note", { name: "Comment on q.sql:4" })
  ).toBeInViewport()
})

// Split shows old beside new; an unchanged line's old number takes a comment on its new line.
test("a split diff comments on an unchanged line from either side", async ({
  page,
  api,
  env,
}) => {
  await page.addInitScript(() =>
    localStorage.setItem("studio.diff.style", "split")
  )
  const sheet = await reviewWithDiff(
    { page, api, env },
    "diff-split",
    [
      "diff --git a/q.txt b/q.txt",
      "--- a/q.txt",
      "+++ b/q.txt",
      "@@ -1,3 +1,4 @@",
      " a",
      "-b",
      "+B",
      "+B2",
      " c",
      "",
    ].join("\n")
  )
  await expect(sheet.locator("pre[data-diff-type]")).toHaveAttribute(
    "data-diff-type",
    "split"
  )
  // c is line 3 on the old side and line 4 on the new.
  const old = sheet
    .locator("[data-deletions] [data-column-number]")
    .filter({ hasText: /^3$/ })
  await old.click()
  await expect(
    sheet.getByRole("textbox", { name: "Comment on q.txt:4" })
  ).toBeVisible()
  await sheet.getByRole("button", { name: "Cancel" }).click()

  // Dragging from the comment button of a to the old c covers a to c.
  const a = sheet
    .locator("[data-deletions] [data-line]")
    .filter({ hasText: /^a$/ })
  await a.hover()
  const button = sheet.getByRole("button", { name: "Comment on this line" })
  const from = (await button.boundingBox())!
  const to = (await old.boundingBox())!
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2)
  await page.mouse.down()
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, {
    steps: 5,
  })
  await page.mouse.up()
  await expect(
    sheet.getByRole("textbox", { name: "Comment on q.txt:1–4" })
  ).toBeVisible()
})

// A push to the branch a repository on this computer has checked out: the card offers to
// update the checked-out files too, ticked, and approving it moves the working copy along.
test("a push to a checked-out branch of a local repository updates its files", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const folder = mkdtempSync(join(tmpdir(), "studio-checked-out-"))
  const repo = join(folder, "proj")
  const git = (...args: string[]) =>
    execFileSync("git", args, {
      encoding: "utf8",
      env: {
        ...process.env,
        GIT_AUTHOR_NAME: "Me",
        GIT_AUTHOR_EMAIL: "me@example.com",
        GIT_COMMITTER_NAME: "Me",
        GIT_COMMITTER_EMAIL: "me@example.com",
      },
    })
  git("init", "-q", "-b", "main", repo)
  writeFileSync(join(repo, "README"), "hello\n")
  git("-C", repo, "add", "README")
  git("-C", repo, "commit", "-q", "-m", "First")
  const name = `here-${Math.random().toString(36).slice(2, 8)}`
  const forge = await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/forges`,
    { name, kind: "local", baseUrl: folder }
  )
  try {
    const id = await api.createSandbox(sandboxName("checked-out"))
    await api.guestReady(id)
    const pushed = await api.push(id, {
      url: `https://git.studio.internal/${name}/proj.git`,
      file: "agent.txt",
      content: "from the sandbox\n",
      message: "Add a file",
    })
    expect(pushed.error, pushed.output).toBeUndefined()

    await page.goto(`/e/${env}`)
    const card = approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: "proj" })
    await card.getByRole("button", { name: "Review" }).click()
    const sheet = page.getByRole("dialog")
    const update = sheet.getByRole("checkbox", {
      name: "Update the checked-out files too",
    })
    await expect(update).toBeChecked()
    await expect(sheet).toContainText("main is checked out in the repository")
    // The diff is the last part of the sheet to load; once it shows, the sheet has settled.
    await expect(sheet).toContainText("from the sandbox")
    await sheet.getByRole("button", { name: "Approve and push" }).click()
    await expect(sheet).toBeHidden()

    await expect
      .poll(() => git("-C", repo, "rev-parse", "HEAD").trim())
      .toBe(pushed.commit)
    expect(readFileSync(join(repo, "agent.txt"), "utf8")).toBe(
      "from the sandbox\n"
    )
    expect(git("-C", repo, "status", "--porcelain")).toBe("")
  } finally {
    await api.json("DELETE", `/api/environments/${env}/forges/${forge!.id}`)
    rmSync(folder, { recursive: true, force: true })
  }
})

/**
 * The middle of a diff row's number: a click there selects the line, a drag or shift-click
 * several. The mouse moves by itself, as hover() waits out the comment button over a number.
 */
async function numberOf(sheet: Locator, row: Locator) {
  // A jump scrolls smoothly: the row settles first.
  let was = -1
  await expect
    .poll(async () => {
      const y = (await row.boundingBox())?.y ?? -2
      const settled = y === was
      was = y
      return settled
    })
    .toBe(true)
  const index = await row.getAttribute("data-line-index")
  const cell = sheet
    .locator(`[data-column-number][data-line-index="${index}"]`)
    .first()
  await cell.scrollIntoViewIfNeeded()
  const box = (await cell.boundingBox())!
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 }
}

test("a diff lists its files to jump to, and a comment covers the lines dragged over", async ({
  page,
  api,
  env,
}) => {
  const sheet = await reviewWithDiff(
    { page, api, env },
    "diff-files",
    [
      "diff --git a/src/a.ts b/src/a.ts",
      "--- a/src/a.ts",
      "+++ b/src/a.ts",
      "@@ -1,3 +1,4 @@",
      " one",
      "-two",
      "+TWO",
      "+three",
      " four",
      "diff --git a/long.txt b/long.txt",
      "new file mode 100644",
      "--- /dev/null",
      "+++ b/long.txt",
      "@@ -0,0 +1,300 @@",
      ...Array.from({ length: 300 }, (_, i) => `+x ${i}`),
      "diff --git a/z.txt b/z.txt",
      "deleted file mode 100644",
      "--- a/z.txt",
      "+++ /dev/null",
      "@@ -1 +0,0 @@",
      "-gone",
      "",
    ].join("\n")
  )

  // The overview lists each file with its lines, and jumps to one.
  const files = sheet.getByRole("navigation", { name: "Files in the diff" })
  await expect(files.getByRole("listitem")).toHaveCount(3)
  await expect(files).toContainText("3 · +302 −2")
  const a = files.getByRole("button", { name: /src\/a\.ts/ })
  await expect(a).toContainText("+2")
  await expect(a).toContainText("−1")
  await expect(files.getByRole("button", { name: /long\.txt/ })).toContainText(
    "+300"
  )
  const gone = sheet.locator("[data-line]").filter({ hasText: /^gone$/ })
  await expect(gone).not.toBeInViewport()
  await files.getByRole("button", { name: /z\.txt/ }).click()
  await expect(gone).toBeInViewport()
  await a.click()

  // Dragging over the numbers of three lines comments on all of them.
  const row = (text: RegExp) =>
    sheet.locator("[data-line]").filter({ hasText: text }).first()
  const one = await numberOf(sheet, row(/^one$/))
  const three = await numberOf(sheet, row(/^three$/))
  await page.mouse.move(one.x, one.y)
  await page.mouse.down()
  await page.mouse.move(three.x, three.y, { steps: 5 })
  await page.mouse.up()
  await sheet
    .getByRole("textbox", { name: "Comment on src/a.ts:1–3" })
    .fill("Keep these together")
  await sheet.getByRole("button", { name: "Save comment" }).click()
  const note = sheet.getByRole("note", { name: "Comment on src/a.ts:1–3" })
  await expect(note).toContainText("lines 1–3")
  await expect(note).toContainText("Keep these together")

  // A click and a shift-click select a range too.
  const x2 = await numberOf(sheet, row(/^x 2$/))
  await page.mouse.click(x2.x, x2.y)
  const x5 = await numberOf(sheet, row(/^x 5$/))
  await page.keyboard.down("Shift")
  await page.mouse.click(x5.x, x5.y)
  await page.keyboard.up("Shift")
  await expect(
    sheet.getByRole("textbox", { name: "Comment on long.txt:3–6" })
  ).toBeVisible()
  await sheet.getByRole("button", { name: "Cancel" }).click()

  // Comments names the range, and the agent's note quotes its lines.
  await sheet.getByRole("tab", { name: /^Comments/ }).click()
  await expect(
    sheet.getByRole("list", { name: "Line comments" })
  ).toContainText("src/a.ts:1–3")
  const decided = page.waitForRequest(
    (r) => r.method() === "POST" && r.url().endsWith("/decide")
  )
  await sheet.getByRole("button", { name: "Reject" }).click()
  const body = (await decided).postDataJSON() as { note?: string }
  expect(body.note).toContain("src/a.ts:1–3, on these lines:")
  expect(body.note).toContain("   one\n  -two\n  +TWO\n  +three")
  expect(body.note).toContain("Keep these together")
})

test("a push's commits open to their own changes", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName("commits"))
  await api.guestReady(id)
  const pushed = await api.push(id, {
    url: forge.url,
    file: "hello.txt",
    content: "hello from one commit\n",
    message: "Add a greeting",
  })
  expect(pushed.error, pushed.output).toBeUndefined()
  await page.goto(`/e/${env}`)
  await approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
    .getByRole("button", { name: "Review" })
    .click()
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("tab", { name: /^Commits/ }).click()
  const panel = sheet.getByRole("tabpanel")
  await panel
    .getByRole("button", { name: /^Changes of .*: Add a greeting$/ })
    .click()
  await expect(panel).toContainText("1 file")
  await expect(
    panel.locator("[data-line]").filter({ hasText: "hello from one commit" })
  ).toBeVisible()
})
