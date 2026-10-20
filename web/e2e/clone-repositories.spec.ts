import { execFileSync } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { expect, test } from "./fixtures"

// The setup's sandbox step lists a local forge's repositories, and the ones ticked with the
// sandbox are cloned into its workspace through the virtual remote once it runs.
test("repositories picked in the setup are cloned into the first sandbox", async ({
  page,
  api,
}) => {
  const folder = mkdtempSync(join(tmpdir(), "studio-clone-"))
  const git = (...args: string[]) =>
    execFileSync("git", args, {
      env: {
        ...process.env,
        GIT_AUTHOR_NAME: "Me",
        GIT_AUTHOR_EMAIL: "me@example.com",
        GIT_COMMITTER_NAME: "Me",
        GIT_COMMITTER_EMAIL: "me@example.com",
      },
    })
  for (const name of ["proj", "other"]) {
    git("init", "-q", "-b", "main", join(folder, name))
    git(
      "-C",
      join(folder, name),
      "commit",
      "-q",
      "--allow-empty",
      "-m",
      "First"
    )
  }
  // An environment of its own, so the step's sandbox is the one this test creates.
  const env = (await api.json<{ id: string }>("POST", "/api/environments", {
    name: `clone-${Date.now()}`,
  }))!.id
  await api.json("POST", `/api/environments/${env}/forges`, {
    name: "here",
    kind: "local",
    baseUrl: folder,
  })
  await page.addInitScript((id) => {
    localStorage.setItem("studio.environment", id)
  }, env)

  await page.goto("/setup")
  await page.getByRole("button", { name: /^(\d+ )?First sandbox / }).click()
  const repos = page.getByRole("group", { name: "Repositories" })
  await expect(repos).toContainText("other")
  await page.getByLabel("Clone repositories").fill("pro")
  await expect(repos.getByText("other", { exact: true })).toBeHidden()
  await repos.getByText("proj", { exact: true }).click()
  await expect(page.getByText("1 selected.")).toBeVisible()
  await page.getByLabel("Name", { exact: true }).fill("cloned")
  await page.getByRole("button", { name: "Create sandbox" }).click()

  const clones = page.getByRole("list", { name: "Clones" })
  await expect(clones).toContainText("Cloned", { timeout: 30_000 })
  await expect(clones).toContainText("proj")
  const sandboxes = await api.json<{ id: string; name: string }[]>(
    "GET",
    `/api/environments/${env}/sandboxes`
  )
  const id = sandboxes!.find((s) => s.name === "cloned")!.id
  expect((await api.guest(id)).clones).toEqual(["/workspace/proj"])
})
