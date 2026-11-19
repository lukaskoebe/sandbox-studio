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

  // Review opens the push in its computer's Changes tab, which then shows how it went.
  await expect(page).toHaveURL(
    new RegExp(`/e/${env}/sandboxes/${id}\\?.*tab=changes`)
  )
  const pane = page.getByTestId("git-review")
  await expect(pane).toContainText("Add a greeting")
  await expect(pane).toContainText("hello from the sandbox")
  await expect(card).toBeHidden()
  await pane.getByRole("button", { name: "Approve and push" }).click()
  await expect(pane.getByRole("status")).toContainText("This was approved.")
  await expect(
    pane.getByRole("button", { name: "Approve and push" })
  ).toHaveCount(0)

  await expect
    .poll(async () =>
      (await api.upstream(forge.repo)).find((b) => b.name === "main")
    )
    .toEqual({ name: "main", sha: pushed.commit, subject: "Add a greeting" })

  // Settings → Activity lists the push with its outcome and links to its page, which shows
  // the decided review without the buttons.
  await page.goto(`/e/${env}/activity?kind=git`)
  const row = page.getByRole("row").filter({ hasText: "team/app" })
  await expect(row).toContainText("pushed")
  await row.getByRole("link").click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/git/reviews/`))
  await expect(page.getByRole("status")).toContainText("This was approved.")
  await expect(page.getByText("hello from the sandbox")).toBeVisible()
  // Its one commit is named in the bar.
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
  const pane = page.getByTestId("git-review")
  await pane.getByRole("button", { name: "Approve and push" }).click()
  await expect(pane.getByRole("status")).toContainText("This was approved")

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
  const pane = page.getByTestId("git-review")
  await expect(
    pane.getByText("rewrites history", { exact: true })
  ).toBeVisible()
  const rewrite = pane.getByRole("region", { name: "Rewrites history" })
  await expect(rewrite).toContainText("drops 1 commit now on the forge")
  await expect(rewrite).toContainText(
    `${first.commit!.slice(0, 10)}Add the coupon field→ ${amended.commit!.slice(0, 10)}`
  )
  await expect(rewrite).toContainText("differ in coupon.txt")
  await expect(pane).toContainText("field v2")
  await expect(
    pane.getByRole("button", { name: "Approve and push" })
  ).toHaveCount(0)
  await pane.getByRole("button", { name: "Force-push" }).click()
  await expect(pane.getByRole("status")).toContainText("This was approved")
  await expect
    .poll(async () =>
      (await api.upstream(forge.repo)).find((b) => b.name === "topic")
    )
    .toMatchObject({ sha: amended.commit })
})

test("a pending push review has a page of its own and is decided there", async ({
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

  // Review opens it in the computer's Changes tab; its id is also the address of its page.
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
  await card.getByRole("button", { name: "Review" }).click()
  await expect(page).toHaveURL(/[?&]review=/)
  const review = new URL(page.url()).searchParams.get("review")
  await page.goto(`/e/${env}/git/reviews/${review}`)

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

// Review keeps the thread in view when the push comes from its teammate's computer.
test("Review opens a push in Changes beside its teammate's thread", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const teammate = await addTeammate(
    api,
    env,
    `Beside ${Math.random().toString(36).slice(2, 6)}`
  )
  const id = await api.createSandbox(sandboxName("beside"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  const pushed = await api.push(id, {
    url: forge.url,
    file: "beside.txt",
    content: "beside\n",
    message: "Add beside",
  })
  expect(pushed.error, pushed.output).toBeUndefined()

  await page.goto(`/e/${env}/teammates/${teammate}?thread=${chat.id}`)
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
  await card.getByRole("button", { name: "Review" }).click()
  await expect(page).toHaveURL(/[?&]tab=changes/)
  expect(new URL(page.url()).searchParams.get("thread")).toBe(chat.id)
  await expect(page.getByTestId("git-review")).toContainText("Add beside")

  await api.dismissApprovals()
})

test("a review that doesn't exist says so", async ({ page, env }) => {
  await page.goto(`/e/${env}/git/reviews/no-such-review`)
  await expect(page.getByText("No such review")).toBeVisible()
  await page.getByRole("link", { name: "All pushes" }).click()
  await expect(page).toHaveURL(new RegExp(`/e/${env}/activity\\?kind=git$`))
})

// The note goes once the decision is in, even when the review closed meanwhile, as a push that
// takes a while upstream leaves the list before the answer comes back.
test("a review's note reaches the chat when the review closed before the decision was in", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const teammate = await addTeammate(
    api,
    env,
    `Left ${Math.random().toString(36).slice(2, 6)}`
  )
  const id = await api.createSandbox(sandboxName("left"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  const pushed = await api.push(id, {
    url: forge.url,
    file: "late.txt",
    content: "late\n",
    message: "Add late",
  })
  expect(pushed.error, pushed.output).toBeUndefined()
  await page.goto(`/e/${env}`)
  await approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
    .getByRole("button", { name: "Review" })
    .click()
  const pane = page.getByTestId("git-review")
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await pane
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("Name it later.txt instead")
  await pane.getByRole("combobox", { name: "Send to" }).click()
  await page.getByRole("option", { name: /^Chat/ }).click()

  // The decision takes a moment; the review closes before it's in.
  await page.route("**/approvals/*/decide", async (route) => {
    await new Promise((r) => setTimeout(r, 1500))
    await route.continue()
  })
  await pane.getByRole("button", { name: "Reject" }).click()
  await page.getByRole("tab", { name: "Files" }).click()
  await expect(pane).toBeHidden()
  await expect(page.getByText("Feedback sent to the agent")).toBeVisible()
  await page.unroute("**/approvals/*/decide")

  await page.goto(`/e/${env}/chats/${chat.id}`)
  await expect(page.getByRole("main")).toContainText(
    "Name it later.txt instead"
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
    return page.getByTestId("git-review")
  }

  // A line comment, sent to the chat with the rejection.
  let pane = await review("greeting.txt", "Add a greeting")
  await commentOn(pane, pane.locator("[data-line]").first())
  await pane
    .getByRole("textbox", { name: "Comment on greeting.txt:1" })
    .fill("Say hello to the world, please")
  await pane.getByRole("button", { name: "Save comment" }).click()
  await expect(
    pane.getByRole("note", { name: "Comment on greeting.txt:1" })
  ).toContainText("Say hello to the world")
  // Comments lists it, and jumps back to its line.
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  const listed = pane.getByRole("list", { name: "Line comments" })
  await expect(listed).toContainText("greeting.txt:1")
  await listed.getByRole("button", { name: /Say hello to the world/ }).click()
  await expect(pane.getByRole("tab", { name: "Changes" })).toHaveAttribute(
    "aria-selected",
    "true"
  )
  await expect(
    pane.getByRole("note", { name: "Comment on greeting.txt:1" })
  ).toBeInViewport()
  await pane.getByRole("combobox", { name: "Send to" }).click()
  await page.getByRole("option", { name: /^Chat/ }).click()
  await pane.getByRole("button", { name: "Reject" }).click()
  await expect(page.getByText("Feedback sent to the agent")).toBeVisible()

  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  await expect(main).toContainText("Review of your push")
  await expect(main).toContainText("rejected; nothing went upstream.")
  await expect(main).toContainText("greeting.txt:1")
  await expect(main).toContainText("Say hello to the world, please")

  // A note, pasted into the agent's terminal without deciding.
  pane = await review("notes.txt", "Add notes")
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await pane
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("Use markdown for the notes")
  await pane.getByRole("combobox", { name: "Send to" }).click()
  await page.getByRole("option", { name: /^Terminal/ }).click()
  await pane.getByRole("button", { name: "More decisions" }).click()
  await page.getByRole("menuitem", { name: /^Send without deciding/ }).click()
  await expect(page.getByText("Feedback sent to the agent")).toBeVisible()
  await expect(pane.getByRole("button", { name: "Reject" })).toBeVisible()

  await page.goto(
    `/e/${env}/teammates/${teammate}?computer=${id}&tab=terminal&term=${session.name}`
  )
  const rows = terminal(page).rows
  await expect(rows).toContainText("is still waiting for my review")
  await expect(rows).toContainText("Use markdown for the notes")

  // A note in a new chat, the default for a teammate's sandbox.
  pane = await review("todo.txt", "Add a todo")
  await expect(pane.getByRole("combobox", { name: "Send to" })).toContainText(
    "A new chat"
  )
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await pane
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("Keep the todo list short")
  await pane.getByRole("button", { name: "Reject" }).click()
  await expect(page.getByText("Feedback sent to a new chat")).toBeVisible()
  const chats = await api.json<{ id: string }[]>(
    "GET",
    `/api/environments/${env}/sandboxes/${id}/chats`
  )
  const fresh = chats!.find((c) => c.id !== chat.id)!
  await page.goto(`/e/${env}/chats/${fresh.id}`)
  await expect(page.getByRole("main")).toContainText("Keep the todo list short")
})

// With no chat or terminal to send to, comments stay on the waiting push for its agent to
// read; an agent someone started by hand in a terminal can take them too.
test("comments are left on a waiting push, or typed into an agent started by hand", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName("left"))
  await api.guestReady(id)
  const pushed = await api.push(id, {
    url: forge.url,
    file: "rename.txt",
    content: "rename me\n",
    message: "Add a file to rename",
  })
  expect(pushed.error, pushed.output).toBeUndefined()
  const waiting = await api.json<
    { id: string; sandboxId?: string; kind: string }[]
  >("GET", `/api/environments/${env}/approvals`)
  const approval = waiting!.find(
    (a) => a.sandboxId === id && a.kind === "git.push"
  )!
  const note = async () =>
    (
      await api.json<{ git?: { push: { note?: string; state: string } } }>(
        "GET",
        `/api/environments/${env}/approvals/${approval.id}`
      )
    )?.git?.push

  await page.goto(`/e/${env}`)
  await approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
    .getByRole("button", { name: "Review" })
    .click()
  const pane = page.getByTestId("git-review")
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await expect(pane).toContainText(
    "The agent reads the outcome and your comments when it checks on the push"
  )
  await pane
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("Call it renamed.txt")
  await pane.getByRole("button", { name: "More decisions" }).click()
  await page.getByRole("menuitem", { name: /^Leave without deciding/ }).click()
  await expect(page.getByText("Comments left on the push")).toBeVisible()
  await expect(
    pane.getByRole("region", { name: "Comments left for the agent" })
  ).toContainText("Call it renamed.txt")
  await expect(
    pane.getByRole("textbox", { name: "Note for the agent" })
  ).toHaveValue("")
  await expect(pane.getByRole("button", { name: "Reject" })).toBeVisible()
  expect(await note()).toMatchObject({
    state: "pending",
    note: "Call it renamed.txt",
  })

  // opencode, started by hand in a terminal of the sandbox, is offered and takes the note.
  await api.setTerminal(id, "work", {
    command: "opencode",
    foreground: ["opencode"],
  })
  await page.reload()
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await pane.getByRole("combobox", { name: "Send to" }).click()
  await page.getByRole("option", { name: "Terminal work · OpenCode" }).click()
  await pane
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("And keep its contents")
  await pane.getByRole("button", { name: "More decisions" }).click()
  await page.getByRole("menuitem", { name: /^Send without deciding/ }).click()
  await expect(page.getByText("Feedback sent to the agent")).toBeVisible()
  await page.goto(`/e/${env}/sandboxes/${id}?tab=terminal&term=work`)
  await expect(terminal(page).rows).toContainText("And keep its contents")

  // Approving with no note of its own keeps the comments left on the push.
  await api.json(
    "POST",
    `/api/environments/${env}/approvals/${approval.id}/decide`,
    { action: "allow", scope: "sandbox" }
  )
  await expect.poll(note).toMatchObject({
    state: "pushed",
    note: "Call it renamed.txt",
  })
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
  const pane = page.getByTestId("git-review")

  const line = pane.locator("[data-line]")
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
async function commentOn(pane: Locator, row: Locator) {
  await expect(async () => {
    await row.hover()
    await pane
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
    try {
      const r = await route.fetch()
      const body = (await r.text()).replace(
        /"diff":"(?:[^"\\]|\\.)*"/g,
        () =>
          `"diff":${JSON.stringify(typeof diff === "string" ? diff : diff())}`
      )
      await route.fulfill({ response: r, body })
    } catch {
      // The page moved on while the list polled, and the request went with it.
      await route.abort().catch(() => {})
    }
  })
  await page.goto(`/e/${env}`)
  await approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
    .getByRole("button", { name: "Review" })
    .click()
  return page.getByTestId("git-review")
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
  const pane = await reviewWithDiff(
    { page, api, env },
    "same-length",
    () => diff
  )
  const lines = pane.locator("[data-line]")
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
  const pane = await reviewWithDiff(
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
  await expect(pane).toContainText("big.bin: 900000 bytes")
  await expect(pane).toContainText("new.txt")
  await expect(pane).toContainText("3 more files not shown")
  await expect(
    pane.locator("[data-line]").filter({ hasText: "<img src=x" })
  ).toBeVisible()
  await expect(
    pane.locator("diffs-container img, diffs-container script")
  ).toHaveCount(0)
  expect(await page.evaluate(() => "pwned" in window)).toBe(false)
})

test("a comment lands on its line after a removed --- line, and a draft outlives scrolling away", async ({
  page,
  api,
  env,
}) => {
  const body = Array.from({ length: 3000 }, (_, i) => `+line ${i}`)
  const pane = await reviewWithDiff(
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
  const row = pane.locator("[data-line]").filter({ hasText: /^line 2$/ })
  await commentOn(pane, row)
  const editor = pane.getByRole("textbox", { name: "Comment on q.sql:4" })
  await editor.fill("A draft on the fourth line")

  await page.mouse.wheel(0, 20000)
  await expect(editor).toHaveCount(0)
  await page.mouse.wheel(0, -20000)
  await expect(editor).toHaveValue("A draft on the fourth line")
  await pane.getByRole("button", { name: "Save comment" }).click()
  await expect(
    pane.getByRole("note", { name: "Comment on q.sql:4" })
  ).toBeVisible()

  // The list jumps back to a line that left the page.
  await page.mouse.wheel(0, 20000)
  await expect(
    pane.getByRole("note", { name: "Comment on q.sql:4" })
  ).toHaveCount(0)
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await pane
    .getByRole("list", { name: "Line comments" })
    .getByRole("button", { name: /A draft on the fourth line/ })
    .click()
  await expect(
    pane.getByRole("note", { name: "Comment on q.sql:4" })
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
  const pane = await reviewWithDiff(
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
  await expect(pane.locator("pre[data-diff-type]")).toHaveAttribute(
    "data-diff-type",
    "split"
  )
  // c is line 3 on the old side and line 4 on the new.
  const old = pane
    .locator("[data-deletions] [data-column-number]")
    .filter({ hasText: /^3$/ })
  await old.click()
  await expect(
    pane.getByRole("textbox", { name: "Comment on q.txt:4" })
  ).toBeVisible()
  await pane.getByRole("button", { name: "Cancel" }).click()

  // Dragging from the comment button of a to the old c covers a to c.
  const a = pane
    .locator("[data-deletions] [data-line]")
    .filter({ hasText: /^a$/ })
  await a.hover()
  const button = pane.getByRole("button", { name: "Comment on this line" })
  const from = (await button.boundingBox())!
  const to = (await old.boundingBox())!
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2)
  await page.mouse.down()
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, {
    steps: 5,
  })
  await page.mouse.up()
  await expect(
    pane.getByRole("textbox", { name: "Comment on q.txt:1–4" })
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
    const pane = page.getByTestId("git-review")
    const update = pane.getByRole("checkbox", {
      name: "Update the checked-out files too",
    })
    await expect(update).toBeChecked()
    await expect(pane).toContainText("main is checked out in the repository")
    // The diff is the last part of the pane to load; once it shows, the pane has settled.
    await expect(pane).toContainText("from the sandbox")
    await pane.getByRole("button", { name: "Approve and push" }).click()
    await expect(pane.getByRole("status")).toContainText("This was approved")

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
async function numberOf(pane: Locator, row: Locator) {
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
  const cell = pane
    .locator(`[data-column-number][data-line-index="${index}"]`)
    .first()
  // The diff renders its rows again as it scrolls, so the cell found may be gone by then.
  let box: { x: number; y: number; width: number; height: number } | null = null
  await expect(async () => {
    await cell.scrollIntoViewIfNeeded({ timeout: 2000 })
    box = await cell.boundingBox()
    expect(box).not.toBeNull()
  }).toPass()
  const { x, y, width, height } = box!
  return { x: x + width / 2, y: y + height / 2 }
}

test("a diff lists its files to jump to, and a comment covers the lines dragged over", async ({
  page,
  api,
  env,
}) => {
  const pane = await reviewWithDiff(
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
  const files = pane.getByRole("navigation", { name: "Files in the diff" })
  await expect(files.getByRole("listitem")).toHaveCount(3)
  await expect(files).toContainText("3 · +302 −2")
  const a = files.getByRole("button", { name: /src\/a\.ts/ })
  await expect(a).toContainText("+2")
  await expect(a).toContainText("−1")
  await expect(files.getByRole("button", { name: /long\.txt/ })).toContainText(
    "+300"
  )
  const gone = pane.locator("[data-line]").filter({ hasText: /^gone$/ })
  await expect(gone).not.toBeInViewport()
  await files.getByRole("button", { name: /z\.txt/ }).click()
  await expect(gone).toBeInViewport()
  await a.click()

  // Dragging over the numbers of three lines comments on all of them.
  const row = (text: RegExp) =>
    pane.locator("[data-line]").filter({ hasText: text }).first()
  const one = await numberOf(pane, row(/^one$/))
  const three = await numberOf(pane, row(/^three$/))
  await page.mouse.move(one.x, one.y)
  await page.mouse.down()
  await page.mouse.move(three.x, three.y, { steps: 5 })
  await page.mouse.up()
  await pane
    .getByRole("textbox", { name: "Comment on src/a.ts:1–3" })
    .fill("Keep these together")
  await pane.getByRole("button", { name: "Save comment" }).click()
  const note = pane.getByRole("note", { name: "Comment on src/a.ts:1–3" })
  await expect(note).toContainText("lines 1–3")
  await expect(note).toContainText("Keep these together")

  // A click and a shift-click select a range too.
  const x2 = await numberOf(pane, row(/^x 2$/))
  await page.mouse.click(x2.x, x2.y)
  const x5 = await numberOf(pane, row(/^x 5$/))
  await page.keyboard.down("Shift")
  await page.mouse.click(x5.x, x5.y)
  await page.keyboard.up("Shift")
  await expect(
    pane.getByRole("textbox", { name: "Comment on long.txt:3–6" })
  ).toBeVisible()
  await pane.getByRole("button", { name: "Cancel" }).click()

  // Comments names the range, and the agent's note quotes its lines.
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await expect(pane.getByRole("list", { name: "Line comments" })).toContainText(
    "src/a.ts:1–3"
  )
  const decided = page.waitForRequest(
    (r) => r.method() === "POST" && r.url().endsWith("/decide")
  )
  await pane.getByRole("button", { name: "Reject" }).click()
  const body = (await decided).postDataJSON() as { note?: string }
  expect(body.note).toContain("src/a.ts:1–3, on these lines:")
  expect(body.note).toContain("   one\n  -two\n  +TWO\n  +three")
  expect(body.note).toContain("Keep these together")
})

test("a review keeps its note, comments, tab and place across another tab and a reload, until it is decided", async ({
  page,
  api,
  env,
}) => {
  const pane = await reviewWithDiff(
    { page, api, env },
    "keep-place",
    [
      "diff --git a/src/a.ts b/src/a.ts",
      "--- a/src/a.ts",
      "+++ b/src/a.ts",
      "@@ -1,2 +1,2 @@",
      " one",
      "-two",
      "+TWO",
      "diff --git a/long.txt b/long.txt",
      "new file mode 100644",
      "--- /dev/null",
      "+++ b/long.txt",
      "@@ -0,0 +1,300 @@",
      ...Array.from({ length: 300 }, (_, i) => `+x ${i}`),
      "",
    ].join("\n")
  )
  const row = (text: RegExp) =>
    pane.locator("[data-line]").filter({ hasText: text }).first()
  const one = await numberOf(pane, row(/^one$/))
  await page.mouse.click(one.x, one.y)
  await pane
    .getByRole("textbox", { name: "Comment on src/a.ts:1" })
    .fill("Why one?")
  await pane.getByRole("button", { name: "Save comment" }).click()
  const comment = pane.getByRole("note", { name: "Comment on src/a.ts:1" })
  await expect(comment).toContainText("Why one?")

  // Reading on into long.txt.
  const scroller = pane.locator(".diff-view > *").first()
  const top = () => scroller.evaluate((el) => el.scrollTop)
  // The number of the line of long.txt at the top of the view, under the file's header.
  const topLine = async () => {
    const edge = (await scroller.boundingBox())!.y + 40
    return pane.locator("[data-line]").evaluateAll((rows, below) => {
      const first = rows
        .map((r) => ({ text: r.textContent, y: r.getBoundingClientRect().top }))
        .filter((x) => x.y >= below)
        .sort((a, b) => a.y - b.y)
        .at(0)
      const m = /^x (\d+)$/.exec(first?.text.trim() ?? "")
      return m ? Number(m[1]) : -1
    }, edge)
  }
  await scroller.hover()
  await page.mouse.wheel(0, 2400)
  await expect.poll(top).toBeGreaterThan(1500)
  const at = await topLine()
  expect(at).toBeGreaterThan(50)
  const near = async () => Math.abs((await topLine()) - at)
  // The place is saved a moment after the scrolling stops.
  await page.waitForTimeout(600)
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await pane
    .getByRole("textbox", { name: "Note for the agent" })
    .fill("Mostly fine")

  // Another tab and back: all of it is where it was.
  await page.getByRole("tab", { name: "Terminal", exact: true }).click()
  await expect(pane).toBeHidden()
  await page.getByRole("tab", { name: /^Changes/ }).click()
  await expect(
    pane.getByRole("textbox", { name: "Note for the agent" })
  ).toHaveValue("Mostly fine")
  await expect(pane.getByRole("list", { name: "Line comments" })).toContainText(
    "Why one?"
  )
  await pane.getByRole("tab", { name: "Changes", exact: true }).click()
  await expect.poll(near).toBeLessThanOrEqual(2)

  // A reload finds the same: the tab, the note, the comment and the place.
  await pane.getByRole("tab", { name: /^Comments/ }).click()
  await page.reload()
  await expect(
    pane.getByRole("textbox", { name: "Note for the agent" })
  ).toHaveValue("Mostly fine")
  await expect(pane.getByRole("list", { name: "Line comments" })).toContainText(
    "Why one?"
  )
  await pane.getByRole("tab", { name: "Changes", exact: true }).click()
  await expect.poll(near).toBeLessThanOrEqual(2)

  // Deciding sends it all and forgets it.
  const decided = page.waitForRequest(
    (r) => r.method() === "POST" && r.url().endsWith("/decide")
  )
  await pane.getByRole("button", { name: "Reject" }).click()
  const body = (await decided).postDataJSON() as { note?: string }
  expect(body.note).toContain("Mostly fine")
  expect(body.note).toContain("Why one?")
  await expect
    .poll(() =>
      page.evaluate(() =>
        Object.keys(localStorage).filter((k) => k.startsWith("studio.review."))
      )
    )
    .toEqual([])
})

// The push's diff is one scroll frame with its head above the first file, and a commit picked in
// the bar narrows it to that commit's own diff, keeping the whole one behind it.
test("a push's commits narrow its diff to one of them", async ({
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
    earlier: [
      {
        file: "first.txt",
        content: "from the first commit\n",
        message: "Add the first file",
      },
    ],
    file: "hello.txt",
    content: "hello from the second commit\n",
    message: "Add a greeting",
  })
  expect(pushed.error, pushed.output).toBeUndefined()
  await page.goto(`/e/${env}`)
  await approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "team/app" })
    .getByRole("button", { name: "Review" })
    .click()
  const pane = page.getByTestId("git-review")
  const line = (text: string) =>
    pane.locator("[data-line]:visible").filter({ hasText: text })
  await expect(line("from the first commit")).toBeVisible()
  await expect(line("hello from the second commit")).toBeVisible()
  // The head scrolls with the files, inside the diff's own scroll box.
  await expect(
    pane.locator(".diff-view").getByRole("heading", { level: 2 })
  ).toBeVisible()

  await pane.getByRole("combobox", { name: "Commits" }).click()
  await page.getByRole("option", { name: /Add the first file/ }).click()
  const commit = pane.getByRole("region", { name: /^Commit / })
  await expect(commit).toContainText("Add the first file")
  await expect(commit).toContainText("1 file")
  await expect(line("from the first commit")).toBeVisible()
  await expect(line("hello from the second commit")).toHaveCount(0)

  await commit.getByRole("button", { name: "all its commits" }).click()
  await expect(commit).toHaveCount(0)
  await expect(line("hello from the second commit")).toBeVisible()
  await expect(pane.getByRole("combobox", { name: "Commits" })).toContainText(
    "All 2 commits"
  )
})

// A file marked viewed folds to its name, and the mark is kept for the branch: the next push
// of it finds the files that didn't change still viewed, and says which one changed since.
test("viewed files fold and stay viewed on the branch's next push until they change", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const id = await api.createSandbox(sandboxName("viewed"))
  await api.guestReady(id)
  const push = (bravo: string) =>
    api.push(id, {
      url: forge.url,
      branch: "fix/viewed",
      from: "main",
      earlier: [
        { file: "a.txt", content: "alpha\n", message: "Add a" },
        { file: "b.txt", content: bravo, message: "Add b" },
      ],
      file: "c.txt",
      content: "charlie\n",
      message: "Add c",
    })
  const open = async () => {
    await page.goto(`/e/${env}`)
    await approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: "team/app" })
      .getByRole("button", { name: "Review" })
      .click()
  }
  const pane = page.getByTestId("git-review")
  // The file headers' marks, and the list of files beside the diff or above its first file.
  const diff = pane.locator(".diff-view").getByTestId("viewed-mark")
  const files = pane.getByRole("navigation", { name: "Files in the diff" })
  const line = (text: string) =>
    pane.locator("[data-line]:visible").filter({ hasText: text })
  const count = (text: string) =>
    expect(pane.getByText(text, { exact: true })).toBeVisible()

  const first = await push("bravo\n")
  expect(first.error, first.output).toBeUndefined()
  await open()
  await expect(line("alpha")).toBeVisible()
  await count("0 / 3 viewed")

  // From the file's header: it folds to its name, and opens again without losing the mark.
  await diff.getByRole("checkbox", { name: "Viewed a.txt" }).click()
  await expect(line("alpha")).toHaveCount(0)
  await count("1 / 3 viewed")
  await diff.getByRole("button", { name: "Show a.txt" }).click()
  await expect(line("alpha")).toBeVisible()
  await expect(
    diff.getByRole("checkbox", { name: "Viewed a.txt" })
  ).toBeChecked()
  await diff.getByRole("button", { name: "Fold a.txt" }).click()
  await expect(line("alpha")).toHaveCount(0)

  // From the list of files, and all of them: Next unviewed goes once none is left.
  await files.getByRole("checkbox", { name: "Viewed b.txt" }).click()
  await count("2 / 3 viewed")
  await expect(
    pane.getByRole("button", { name: "Next unviewed" })
  ).toBeVisible()
  await files.getByRole("checkbox", { name: "Viewed c.txt" }).click()
  await count("3 / 3 viewed")
  await expect(pane.getByRole("button", { name: "Next unviewed" })).toHaveCount(
    0
  )
  await files.getByRole("checkbox", { name: "Viewed c.txt" }).click()
  await count("2 / 3 viewed")
  await expect(line("charlie")).toBeVisible()

  // Kept in this browser: a reload finds them.
  await page.reload()
  await open()
  await count("2 / 3 viewed")
  await expect(line("charlie")).toBeVisible()
  await expect(line("alpha")).toHaveCount(0)
  await expect(line("bravo")).toHaveCount(0)

  await pane.getByRole("button", { name: "Reject" }).click()
  await expect(pane).toBeHidden()

  // The branch again, with b.txt changed: a.txt stays viewed, b.txt says it changed.
  // The sandbox's next fetch after the rejection fails once, with its reason.
  expect((await push("bravo, changed\n")).error).toContain("409")
  const again = await push("bravo, changed\n")
  expect(again.error, again.output).toBeUndefined()
  await open()
  await count("1 / 3 viewed")
  await expect(line("alpha")).toHaveCount(0)
  await expect(line("bravo, changed")).toBeVisible()
  await expect(diff.getByText("Changed since you viewed it")).toBeVisible()
  await expect(
    diff.getByRole("checkbox", { name: "Viewed b.txt" })
  ).not.toBeChecked()
  await diff.getByRole("checkbox", { name: "Viewed b.txt" }).click()
  await count("2 / 3 viewed")
  await expect(diff.getByText("Changed since you viewed it")).toHaveCount(0)
})

// Changes holds its parts in sections that fold, per computer. A review link opens its section
// where it was folded and leaves the others; an allowed push moves to Pushed for a day.
test("a review link opens Changes on its section, and the allowed push moves to Pushed", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const forge = await forgeWithRepo(api, env)
  const name = sandboxName("sections")
  const id = await api.createSandbox(name)
  await api.guestReady(id)
  const pushed = await api.push(id, {
    url: forge.url,
    file: "hello.txt",
    content: "hello from the sandbox\n",
    message: "Add a greeting",
  })
  expect(pushed.error, pushed.output).toBeUndefined()
  const waiting = await api.json<
    { id: string; sandboxId?: string; kind: string }[]
  >("GET", `/api/environments/${env}/approvals`)
  const review = waiting!.find(
    (a) => a.sandboxId === id && a.kind === "git.push"
  )!

  const screen = page.getByRole("region", { name: `${name}'s screen` })
  const rail = screen.getByLabel("Changes", { exact: true })
  const header = (label: string) =>
    rail
      .getByRole("region", { name: label, exact: true })
      .getByRole("button", { name: new RegExp(`^${label}`) })
  // Folded before the link, its section opens; the others stay as they were.
  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes`)
  await header("Waiting for you").click()
  await expect(header("Waiting for you")).toHaveAttribute(
    "aria-expanded",
    "false"
  )
  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes&review=${review.id}`)
  await expect(header("Waiting for you")).toHaveAttribute(
    "aria-expanded",
    "true"
  )
  await expect(header("Repositories")).toHaveAttribute("aria-expanded", "true")
  await expect(rail.getByRole("status")).toHaveCount(0)
  // Away to Details and back, the folds are the user's.
  await header("Repositories").click()
  await screen.getByRole("tab", { name: "Details" }).click()
  await screen.getByRole("tab", { name: /^Changes/ }).click()
  await expect(header("Waiting for you")).toHaveAttribute(
    "aria-expanded",
    "true"
  )
  await expect(header("Repositories")).toHaveAttribute("aria-expanded", "false")
  await header("Repositories").click()
  await header("Repositories").blur()

  // [ and ] move between the sections' headers.
  await page.locator("body").press("]")
  await expect(header("Waiting for you")).toBeFocused()
  await page.keyboard.press("]")
  await expect(header("Repositories")).toBeFocused()
  await page.keyboard.press("[")
  await expect(header("Waiting for you")).toBeFocused()

  await page.getByRole("button", { name: "Approve and push" }).click()
  await expect(header("Pushed")).toBeVisible()
  await expect(
    rail.getByRole("region", { name: "Waiting for you" })
  ).toHaveCount(0)
  await expect(rail.getByRole("region", { name: "Pushed" })).toContainText(
    "Push to main"
  )

  // A fold is kept for the computer.
  await header("Repositories").click()
  await expect(header("Repositories")).toHaveAttribute("aria-expanded", "false")
  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes`)
  await expect(header("Repositories")).toHaveAttribute("aria-expanded", "false")
  await expect(header("Pushed")).toHaveAttribute("aria-expanded", "true")
  await header("Repositories").click()
})

test("a pushed branch shows its pull request's state, checks and comments in Pushed", async ({
  page,
  api,
  env,
}) => {
  const suffix = Math.random().toString(36).slice(2, 8)
  const forge = `fj-${suffix}`
  const host = `forgejo-${suffix}.example.com`
  await api.createUpstream(`${host}/acme/web`)
  await api.json("POST", `/api/environments/${env}/forges`, {
    name: forge,
    kind: "forgejo",
    baseUrl: `https://${host}`,
    token: "e2e-forge-token",
  })
  const name = sandboxName("pushed-pr")
  const id = await api.createSandbox(name)
  await api.guestReady(id)
  // The fake forge's pull request 1 is from feature/coupons, and its CI passed.
  const pushed = await api.push(id, {
    url: `https://git.studio.internal/${forge}/acme/web.git`,
    branch: "feature/coupons",
    from: "main",
    file: "coupon.ts",
    content: "export const coupon = true\n",
    message: "Apply coupons",
  })
  expect(pushed.error, pushed.output).toBeUndefined()
  const approval = /approval (\S+)\)/.exec(pushed.output)![1]
  await api.json(
    "POST",
    `/api/environments/${env}/approvals/${approval}/decide`,
    { action: "allow" }
  )

  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes`)
  const row = page
    .getByRole("region", { name: "Pushed", exact: true })
    .getByRole("button", { name: /Push to feature\/coupons/ })
  await expect(row).toContainText("#1")
  await expect(row).toContainText("open")
  await expect(row).toContainText("checks passed")
})

// The pull request goes up under the user's name: the line naming the teammate is theirs to add.
test("a teammate's pull request names who pushed it only when that is ticked", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const suffix = Math.random().toString(36).slice(2, 8)
  const forge = `fj-${suffix}`
  const host = `forgejo-${suffix}.example.com`
  await api.createUpstream(`${host}/acme/web`)
  await api.json("POST", `/api/environments/${env}/forges`, {
    name: forge,
    kind: "forgejo",
    baseUrl: `https://${host}`,
    token: "e2e-forge-token",
  })
  const who = `Credited ${suffix.slice(0, 4)}`
  const teammate = await addTeammate(api, env, who)
  const name = sandboxName("credit")
  const id = await api.createSandbox(name, teammate)
  await api.guestReady(id)
  const line = `Pushed by ${who} from sandbox ${name} through Sandbox Studio.`
  type Opened = { repo: string; body: { head: string; body: string } }
  const opened = async (head: string) =>
    ((await api.json<Opened[]>("GET", "/api/e2e/pulls")) ?? []).find(
      (p) => p.repo === `${host}/acme/web` && p.body.head === head
    )
  const review = async (branch: string) => {
    const pushed = await api.push(id, {
      url: `https://git.studio.internal/${forge}/acme/web.git`,
      branch,
      from: "main",
      file: `${branch.replace("/", "-")}.txt`,
      content: `${branch}\n`,
      message: `Add ${branch}`,
    })
    expect(pushed.error, pushed.output).toBeUndefined()
    await page.goto(`/e/${env}`)
    await approvals(page)
      .getByTestId("approval-card")
      .filter({ hasText: branch })
      .getByRole("button", { name: "Review" })
      .click()
    const pane = page.getByTestId("git-review")
    // The decision row sits below the diff until it renders, then sticks to the bottom.
    await expect(
      pane.getByText(`${branch.replace("/", "-")}.txt`).first()
    ).toBeVisible()
    return pane
  }

  // Off by default.
  let pane = await review("feature/plain")
  await expect(
    pane.getByRole("checkbox", { name: /Also open a pull request/ })
  ).toBeChecked()
  let credit = pane.getByRole("checkbox", { name: /End the description with/ })
  await expect(credit).not.toBeChecked()
  await expect(pane.getByText(line, { exact: false })).toBeVisible()
  await pane.getByRole("button", { name: "Push and open pull request" }).click()
  await expect.poll(() => opened("feature/plain")).toBeTruthy()
  expect((await opened("feature/plain"))!.body.body).not.toContain("Pushed by")

  // Ticked, the description ends with it.
  pane = await review("feature/credited")
  credit = pane.getByRole("checkbox", { name: /End the description with/ })
  // A click the moving row lost is clicked again; one that toggled twice still fails.
  await expect(async () => {
    if (!(await credit.isChecked())) await credit.click()
    await expect(credit).toBeChecked({ timeout: 1000 })
  }).toPass()
  await pane.getByRole("button", { name: "Push and open pull request" }).click()
  await expect.poll(() => opened("feature/credited")).toBeTruthy()
  expect((await opened("feature/credited"))!.body.body).toBe(line)

  // And the choice is remembered for the teammate's next push.
  pane = await review("feature/again")
  await expect(
    pane.getByRole("checkbox", { name: /End the description with/ })
  ).toBeChecked()
  await pane.getByRole("button", { name: "Reject" }).click()
})
