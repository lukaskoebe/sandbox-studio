import { execFileSync } from "node:child_process"
import { mkdirSync, mkdtempSync } from "node:fs"
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
  // The forge is named after its host, unless the user names it.
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue("gitlab")
  await dialog.locator("#forge-url").fill("https://git.example.co.uk")
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue("example")
  await dialog.getByLabel("Name in Studio", { exact: true }).fill("work")
  await dialog.locator("#forge-url").fill("https://gitlab.com")
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue("work")
  await expect(dialog).toContainText("https://git.studio.internal/work/")
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
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue("example")
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
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue("local")
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
  const tester = page.getByRole("dialog", { name: "Test local" })
  await tester.getByLabel("Repository").fill("proj")
  await tester.getByRole("button", { name: "Test" }).click()
  await expect(tester).toContainText("proj has 1 branch.")
})

// The folder of a local forge can be picked from the folders on this computer, which the
// browser can't name: Browse… lists them from where the field points, repositories marked.
test("a local forge's folder can be picked by browsing", async ({
  page,
  env,
}) => {
  const folder = mkdtempSync(join(tmpdir(), "studio-browse-"))
  execFileSync("git", ["init", "-q", join(folder, "proj")])
  mkdirSync(join(folder, "notes"))
  mkdirSync(join(folder, ".hidden"))

  await page.goto(`/e/${env}/settings/git`)
  await page.getByRole("button", { name: "Add forge" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add forge" })
  await dialog.getByRole("combobox").filter({ hasText: "GitHub" }).click()
  await page
    .getByRole("option", { name: "Folder on this computer", exact: true })
    .click()
  await dialog.getByLabel("Folder", { exact: true }).fill(folder)
  await dialog.getByRole("button", { name: "Browse…" }).click()

  const picker = page.getByRole("dialog", { name: "Choose a folder" })
  const folders = picker.getByRole("list", { name: "Folders" })
  await expect(folders.getByRole("listitem")).toHaveText([
    "notes",
    "projrepository",
  ])
  await expect(picker).toContainText("1 repository in this folder.")
  await folders.getByRole("listitem").filter({ hasText: "proj" }).click()
  await expect(picker).toContainText("This folder is a repository itself.")
  await picker.getByRole("button", { name: "Up one folder" }).click()
  await picker.getByRole("button", { name: "Use this folder" }).click()
  await expect(picker).toBeHidden()
  await expect(dialog.getByLabel("Folder", { exact: true })).toHaveValue(folder)
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue(/^local(-\d+)?$/)
})
