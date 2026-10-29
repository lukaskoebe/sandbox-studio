import { execFileSync } from "node:child_process"
import { mkdirSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import type { Locator, Page } from "@playwright/test"
import { expect, sandboxName, test } from "./fixtures"

/** Opens the Add forge dialog in Settings → Git. */
async function addForge(page: Page, env: string) {
  await page.goto(`/e/${env}/settings/git`)
  await page.getByRole("button", { name: "Add forge" }).first().click()
  return page.getByRole("dialog", { name: "Add forge" })
}

/** The first step: a repository's URL, and Detect. */
async function detect(dialog: Locator, url: string) {
  await dialog.getByLabel("Repository", { exact: true }).fill(url)
  await dialog.getByRole("button", { name: "Detect" }).click()
}

const found = (dialog: Locator) =>
  dialog.getByRole("region", { name: "What Studio found" })

/** Opens More, with the name and the address Studio found. */
const more = (dialog: Locator) =>
  dialog.getByRole("button", { name: /^More: / }).click()

// GitLab is a forge kind of its own: the Add forge dialog fills in its address and says which
// token scopes it needs. The name comes from the host, in kebab case.
test("the forge dialog offers GitLab with its own address and token scopes", async ({
  page,
  env,
}) => {
  const dialog = await addForge(page, env)
  await dialog
    .getByRole("button", { name: "Set it up by hand instead" })
    .click()
  await dialog.getByRole("radio", { name: "GitLab", exact: true }).click()
  await expect(
    dialog.getByRole("radio", { name: "GitLab", exact: true })
  ).toBeChecked()

  await expect(dialog.locator("#forge-url")).toHaveValue("https://gitlab.com")
  await more(dialog)
  const name = dialog.getByLabel("Name in Studio", { exact: true })
  // The forge is named after its host, unless the user names it.
  await expect(name).toHaveValue("gitlab")
  await dialog.locator("#forge-url").fill("https://git.example.co.uk")
  await expect(name).toHaveValue("example")
  await dialog.locator("#forge-url").fill("https://forgejo.mycompany.com")
  await expect(name).toHaveValue("forgejo-mycompany")
  await name.fill("work")
  await dialog.locator("#forge-url").fill("https://gitlab.com")
  await expect(name).toHaveValue("work")
  await expect(dialog).toContainText("https://git.studio.internal/work/")
  await expect(dialog).toContainText("api and write_repository scopes")
  await expect(dialog.getByLabel("Username", { exact: true })).toHaveCount(0)
})

// A pasted HTTPS URL of a host Studio doesn't know is asked which forge it runs: the e2e
// upstream is a Forgejo to it. The second step says so, with only the token to fill in; another
// HTTPS kind picked after keeps the host.
test("a self-hosted forge's URL is detected and kept across kinds", async ({
  page,
  env,
}) => {
  const dialog = await addForge(page, env)
  await detect(dialog, "https://git.example.org/team/app.git")
  await expect(found(dialog)).toContainText("git.example.org runs Forgejo")
  await expect(found(dialog)).toContainText("team/app")
  await expect(found(dialog).getByLabel(/^Pull requests: yes/)).toBeVisible()
  await expect(dialog.locator("#forge-token")).toBeVisible()
  await expect(dialog.getByRole("button", { name: /^More: / })).toContainText(
    "(example)"
  )

  await dialog.getByRole("button", { name: "Not right? Pick the kind" }).click()
  await expect(
    dialog.getByRole("radio", { name: "Forgejo", exact: true })
  ).toBeChecked()
  await expect(dialog.locator("#forge-url")).toHaveValue(
    "https://git.example.org"
  )
  await dialog.getByRole("radio", { name: "Other HTTPS", exact: true }).click()
  await expect(dialog.locator("#forge-url")).toHaveValue(
    "https://git.example.org"
  )
  await dialog.getByRole("radio", { name: "Forgejo", exact: true }).click()
  await expect(dialog.locator("#forge-url")).toHaveValue(
    "https://git.example.org"
  )

  // Back keeps the URL for another try.
  await dialog.getByRole("button", { name: "Back" }).click()
  await expect(dialog.getByLabel("Repository", { exact: true })).toHaveValue(
    "https://git.example.org/team/app.git"
  )
})

// A forge whose API Studio can't use with the token is refused, unless the user saves it
// anyway.
test("a forge's token is checked, and can be saved unchecked", async ({
  page,
  api,
  env,
}) => {
  const id = `fj-${Math.random().toString(36).slice(2, 8)}`
  const host = `${id}.example.org`
  const dialog = await addForge(page, env)
  await detect(dialog, `https://${host}/team/app.git`)
  await expect(found(dialog)).toContainText(`${host} runs Forgejo`)
  // Named after the host without its domain ending.
  const name = `${id}-example`
  await expect(dialog.getByRole("button", { name: /^More: / })).toContainText(
    `(${name})`
  )
  await dialog.locator("#forge-token").fill("rejected-token")
  await dialog.getByRole("button", { name: "Add forge" }).click()
  await expect(dialog.getByRole("alert")).toContainText(
    `Studio could not use the API at https://${host} with this token`
  )
  const skip = dialog.getByRole("checkbox", {
    name: "Save it anyway, without the check",
  })
  await skip.check()
  await dialog.getByRole("button", { name: "Add forge" }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole("row").filter({ hasText: host })).toBeVisible()
  const forges = await api.json<{ id: string; name: string }[]>(
    "GET",
    `/api/environments/${env}/forges`
  )
  const forge = forges!.find((f) => f.name === name)!
  await api.json("DELETE", `/api/environments/${env}/forges/${forge.id}`)
})

// A clone URL pasted into an SSH forge's address is detected as in the first step: the user
// and host from its address, the name from its host.
test("an SSH forge's address takes a clone URL and keeps its user", async ({
  page,
  env,
}) => {
  const dialog = await addForge(page, env)
  await dialog
    .getByRole("button", { name: "Set it up by hand instead" })
    .click()
  await dialog.getByRole("radio", { name: "Other SSH", exact: true }).click()
  await dialog
    .getByLabel("SSH address", { exact: true })
    .fill("forgejo@code.example.org:team/app.git")
  await expect(found(dialog)).toContainText("git over SSH")
  await expect(found(dialog)).toContainText("team/app")
  await more(dialog)
  await expect(dialog.locator("#forge-url")).toHaveValue(
    "ssh://forgejo@code.example.org"
  )
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue("code-example")
})

// A pasted SSH clone URL of a host with a known API suggests that API for pull requests, with
// an optional token.
test("an SSH forge of a known host is offered its API", async ({
  page,
  env,
}) => {
  const dialog = await addForge(page, env)
  await detect(dialog, "git@github.com:owner/repo.git")
  await expect(found(dialog)).toContainText(
    "git over SSH; github.com runs GitHub"
  )
  await expect(dialog.locator("#forge-api-token")).toBeVisible()
  await expect(dialog).toContainText("fine-grained token")
  await expect(dialog).toContainText("Leave it empty for git alone")
  await more(dialog)
  await expect(dialog.locator("#forge-url")).toHaveValue("ssh://git@github.com")
  await expect(dialog.locator("#forge-api-url")).toHaveValue(
    "https://github.com"
  )
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

  const dialog = await addForge(page, env)
  await detect(dialog, repo)
  await expect(found(dialog)).toContainText(`${folder} is a folder here`)
  await expect(found(dialog).getByLabel(/^Pull requests: no/)).toBeVisible()
  await expect(dialog.getByLabel("Access token")).toHaveCount(0)
  await more(dialog)
  await expect(dialog.getByLabel("Folder", { exact: true })).toHaveValue(folder)
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue("local")
  await expect(dialog).toContainText(
    "https://git.studio.internal/local/proj.git"
  )
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

// The first step picks a repository on this computer, or the folder repositories are in: a
// repository is one of the folder it is in, and a folder of repositories is the forge itself.
test("the first step picks a local repository or its folder", async ({
  page,
  env,
}) => {
  const folder = mkdtempSync(join(tmpdir(), "studio-pick-"))
  execFileSync("git", ["init", "-q", join(folder, "proj")])

  const dialog = await addForge(page, env)
  await dialog
    .getByRole("button", { name: "Pick a folder on this computer…" })
    .click()
  const picker = page.getByRole("dialog", { name: "Choose a folder" })
  await expect(picker).toContainText(
    "Pick a repository, or the folder your repositories are in."
  )
  // Typed into the field, the folder opens where it points.
  await picker.getByRole("button", { name: "Cancel" }).click()
  await dialog.getByLabel("Repository", { exact: true }).fill(folder)
  await dialog
    .getByRole("button", { name: "Pick a folder on this computer…" })
    .click()
  const folders = picker.getByRole("list", { name: "Folders" })
  await folders.getByRole("listitem").filter({ hasText: "proj" }).click()
  await expect(picker).toContainText("This folder is a repository.")
  await picker.getByRole("button", { name: "Use this folder" }).click()
  await expect(found(dialog)).toContainText(`${folder} is a folder here`)
  await expect(found(dialog)).toContainText("proj")

  await dialog.getByRole("button", { name: "Back" }).click()
  await dialog
    .getByRole("button", { name: "Pick a folder on this computer…" })
    .click()
  await picker.getByRole("button", { name: "Up one folder" }).click()
  await picker.getByRole("button", { name: "Use this folder" }).click()
  await expect(found(dialog)).toContainText(`${folder} is a folder here`)
  await expect(found(dialog)).not.toContainText("proj")
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

  const dialog = await addForge(page, env)
  await dialog
    .getByRole("button", { name: "Set it up by hand instead" })
    .click()
  await dialog
    .getByRole("radio", { name: "Folder on this computer", exact: true })
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
  await more(dialog)
  await expect(
    dialog.getByLabel("Name in Studio", { exact: true })
  ).toHaveValue(/^local(-\d+)?$/)
})

// git in a sandbox asking credentials for a host no forge covers gets a hint and posts a
// card; Add forge opens the dialog on the forge detected from the repository's URL.
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
  await expect(found(dialog)).toContainText("github.com runs GitHub")
  await expect(found(dialog)).toContainText("acme/web")
  await dialog.getByRole("button", { name: "Back" }).click()
  await expect(dialog.getByLabel("Repository", { exact: true })).toHaveValue(
    "https://github.com/acme/web.git"
  )
  await expect(card).toHaveCount(0)
  await dialog.getByRole("button", { name: "Close" }).click()
  await expect(page).toHaveURL(/\/settings\/forges$/)
})
