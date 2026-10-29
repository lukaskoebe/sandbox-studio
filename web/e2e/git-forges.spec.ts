import { execFileSync } from "node:child_process"
import { mkdirSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { expect, sandboxName, test } from "./fixtures"

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

// A pasted SSH clone URL of a host with a known API suggests that API for pull requests, and
// the form then wants its token.
test("an SSH forge of a known host is offered its API", async ({
  page,
  env,
}) => {
  await page.goto(`/e/${env}/settings/git`)
  await page.getByRole("button", { name: "Add forge" }).first().click()
  const dialog = page.getByRole("dialog", { name: "Add forge" })
  await dialog
    .getByLabel("Repository URL", { exact: true })
    .fill("git@github.com:owner/repo.git")
  await expect(dialog.locator("#forge-url")).toHaveValue("ssh://git@github.com")
  await expect(
    dialog.getByRole("combobox").filter({ hasText: "GitHub" })
  ).toBeVisible()
  await expect(dialog.locator("#forge-api-url")).toHaveValue(
    "https://github.com"
  )
  await expect(dialog.locator("#forge-api-token")).toBeVisible()
  await expect(dialog).toContainText("fine-grained token")
})

// An SSH forge gets an API token later in its row, and loses it again, keeping git over SSH.
test("an SSH forge's API token is added and removed in its row", async ({
  page,
  api,
  env,
}) => {
  const name = `gh-${Math.random().toString(36).slice(2, 8)}`
  const forge = await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/forges`,
    {
      name,
      kind: "ssh",
      baseUrl: "git@github.com",
      // github.com's key is built into Studio, so nothing is scanned.
      hostKey:
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
    }
  )
  try {
    await page.goto(`/e/${env}/settings/git`)
    const row = page.getByRole("row").filter({ hasText: name })
    await expect(row).toContainText("No pull requests without the forge's API")
    await row.getByRole("button", { name: "Add an API token" }).click()
    const dialog = page.getByRole("dialog", {
      name: `Add an API token of ${name}`,
    })
    await expect(dialog.locator("#forge-api-url")).toHaveValue(
      "https://github.com"
    )
    await dialog.locator("#forge-api-token").fill("github_pat_e2e")
    await dialog.getByRole("button", { name: "Save" }).click()
    await expect(dialog).toHaveCount(0)
    await expect(row).toContainText(
      "Pull requests through the GitHub API at https://github.com"
    )

    await row.getByRole("button", { name: "Remove", exact: true }).click()
    await page
      .getByRole("alertdialog", { name: `Remove the API of ${name}?` })
      .getByRole("button", { name: "Remove" })
      .click()
    await expect(row).toContainText("No pull requests without the forge's API")
  } finally {
    await api.json("DELETE", `/api/environments/${env}/forges/${forge!.id}`)
  }
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

// git in a sandbox asking credentials for a host no forge covers gets a hint and posts a
// card; Add forge opens the dialog filled in from the repository's URL.
test("git asking for credentials offers to add the forge", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = sandboxName("cred")
  const sb = await api.createSandbox(name)
  const call = (await api.json<{ result: { message: string } }>(
    "POST",
    `/api/e2e/sandboxes/${sb}/calls`,
    {
      method: "git_credential",
      params: { protocol: "https", host: "github.com", path: "acme/web.git" },
      wait: true,
    }
  ))!
  expect(call.result.message).toContain("no credentials for github.com")

  await page.goto(`/e/${env}`)
  const card = page
    .getByTestId("approval-card")
    .filter({ hasText: `${name} needs access to github.com` })
  await expect(card.getByText("https://github.com/acme/web.git")).toBeVisible()
  await card.getByRole("button", { name: "Add forge" }).click()
  await expect(page).toHaveURL(/\/settings\/forges\?add=/)
  const dialog = page.getByRole("dialog", { name: "Add forge" })
  await expect(dialog.getByLabel("Repository URL")).toHaveValue(
    "https://github.com/acme/web.git"
  )
  await expect(card).toHaveCount(0)
  await dialog.getByRole("button", { name: "Close" }).click()
  await expect(page).toHaveURL(/\/settings\/forges$/)
})
