import { execFileSync } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { expect, test } from "./fixtures"

// GitLab is a forge kind of its own: the Add forge dialog fills in its address and says which
// token scopes it needs.
test("the forge dialog offers GitLab with its own address and token scopes", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/git`)
  await page.getByRole("button", { name: "Add forge" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add forge" })
  await dialog.getByRole("combobox").filter({ hasText: "GitHub" }).click()
  await page.getByRole("option", { name: "GitLab", exact: true }).click()

  await expect(dialog.locator("#forge-url")).toHaveValue("https://gitlab.com")
  await expect(
    dialog.getByLabel("Short name", { exact: true })
  ).toHaveAttribute("placeholder", "gitlab")
  await expect(dialog).toContainText("api and write_repository scopes")
  await expect(dialog.getByLabel("Username", { exact: true })).toHaveCount(0)
})

// A clone URL pasted into an SSH forge's address fills in the form: the user and host from its
// address, the name from its host.
test("an SSH forge's address takes a clone URL and keeps its user", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/git`)
  await page.getByRole("button", { name: "Add forge" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add forge" })
  await dialog.getByRole("combobox").filter({ hasText: "GitHub" }).click()
  await page.getByRole("option", { name: "Other SSH", exact: true }).click()

  await dialog
    .getByLabel("SSH address", { exact: true })
    .fill("forgejo@code.example.org:team/app.git")
  await expect(dialog.locator("#forge-url")).toHaveValue(
    "ssh://forgejo@code.example.org"
  )
  await expect(dialog.getByLabel("Short name", { exact: true })).toHaveValue(
    "example"
  )
  await expect(dialog.locator("#forge-repo-url")).toHaveValue(
    "forgejo@code.example.org:team/app.git"
  )
})

// A repository's folder pasted into the dialog makes a local forge for the folder it is in:
// no token, and the test lists the repository's branches with the host's git.
test("a repository folder on this computer becomes a local forge", async ({
  page,
  env,
}) => {
  const folder = mkdtempSync(join(tmpdir(), "studio-local-forge-"))
  const repo = join(folder, "proj")
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
  git("init", "-q", "-b", "main", repo)
  git("-C", repo, "commit", "-q", "--allow-empty", "-m", "First")

  await page.goto(`/e/${env}/settings/git`)
  await page.getByRole("button", { name: "Add forge" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add forge" })
  await dialog.getByLabel("Repository URL", { exact: true }).fill(repo)
  await expect(dialog.getByLabel("Folder", { exact: true })).toHaveValue(folder)
  await expect(dialog.getByLabel("Short name", { exact: true })).toHaveValue(
    "local"
  )
  await expect(dialog).toContainText(
    "https://git.studio.internal/local/proj.git"
  )
  await expect(dialog.getByLabel("Access token")).toHaveCount(0)
  await dialog.getByRole("button", { name: "Add forge" }).click()
  await expect(dialog).toBeHidden()

  const row = page.getByRole("row").filter({ hasText: folder })
  await expect(row).toContainText("Folder on this computer")
  await expect(row.getByRole("button", { name: "Replace token" })).toHaveCount(
    0
  )
  await row.getByRole("button", { name: "Test forge" }).click()
  const test = page.getByRole("dialog", { name: "Test local" })
  await test.getByLabel("Repository").fill("proj")
  await test.getByRole("button", { name: "Test" }).click()
  await expect(test).toContainText("proj has 1 branch.")
})
