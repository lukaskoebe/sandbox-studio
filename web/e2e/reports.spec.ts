import {
  addTeammate,
  approvals,
  expect,
  openRow,
  sandboxName,
  test,
} from "./fixtures"

// An agent offers a report for Studio's developers: the card shows it with the names Studio
// knows taken out, lets the user edit it, and opens GitHub's new-issue page filled in. Nothing
// is sent from Studio; Don't send drops it.
test("an agent's report is anonymized, editable, and opens on GitHub only from the card", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const who = `Reporter ${sandboxName("")}`
  const teammate = await addTeammate(api, env, who)
  const box = sandboxName("rep")
  const id = await api.createSandbox(box, teammate)
  await api.guestReady(id)
  // GitHub stays out of the test: the page the card opens is a stub.
  await page
    .context()
    .route("https://github.com/**", (r) =>
      r.fulfill({ contentType: "text/html", body: "<title>New issue</title>" })
    )
  const report = (title: string, body: string) =>
    api.json<{ result: { status: string; replaced: number } }>(
      "POST",
      `/api/e2e/sandboxes/${id}/calls`,
      { method: "report_issue", params: { title, body }, wait: true }
    )

  await page.goto(`/e/${env}`)
  const sent = await report(
    `studio pr checks hangs in ${box}`,
    `${who} ran it in /home/someone/src; mail someone@example.org.`
  )
  expect(sent!.result).toMatchObject({ status: "offered" })
  const card = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "offers a report for Studio's developers" })
  await openRow(card)
  const title = card.getByRole("textbox", { name: "Title" })
  const text = card.getByRole("textbox", { name: "Report" })
  await expect(title).toHaveValue("studio pr checks hangs in <computer>")
  await expect(text).toHaveValue(
    "<teammate> ran it in /home/<user>/src; mail <email>."
  )
  await expect(card).toContainText(/took out \d+ names, addresses and paths/)
  await expect(card).toContainText("Reported by an agent in Sandbox Studio")

  // The user's edits are what goes; the footer rides along below a rule.
  await title.fill("studio pr checks --wait never returns")
  await text.fill("It hangs after the checks pass.")
  const open = card.getByRole("link", { name: "Open on GitHub…" })
  const href = new URL((await open.getAttribute("href"))!)
  expect(href.origin + href.pathname).toMatch(
    /^https:\/\/github\.com\/[^/]+\/[^/]+\/issues\/new$/
  )
  expect(href.searchParams.get("title")).toBe(
    "studio pr checks --wait never returns"
  )
  expect(href.searchParams.get("body")).toMatch(
    /^It hangs after the checks pass\.\n\n---\nReported by an agent/
  )
  await text.fill("")
  await expect(open).toHaveAttribute("aria-disabled", "true")
  await expect(open).not.toHaveAttribute("href")
  await text.fill("It hangs after the checks pass.")
  const [tab] = await Promise.all([page.waitForEvent("popup"), open.click()])
  await expect(tab).toHaveURL(/github\.com\/.+\/issues\/new\?title=/)
  await tab.close()
  await expect(card).toHaveCount(0)

  // Don't send drops another without opening anything.
  await report("the Files tab shows a stale tree", "It lists a deleted file.")
  const next = approvals(page)
    .getByTestId("approval-card")
    .filter({ hasText: "the Files tab shows a stale tree" })
  await openRow(next)
  await next.getByRole("button", { name: "Don't send" }).click()
  await expect(next).toHaveCount(0)
})
