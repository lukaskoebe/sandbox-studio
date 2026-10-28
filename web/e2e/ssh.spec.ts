import { readFile } from "node:fs/promises"
import { homedir } from "node:os"
import path from "node:path"
import { expect, sandboxName, test } from "./fixtures"

type SSHAccess = {
  enabled: boolean
  userConfig: string
  config: string
  hosts: { alias: string; sandboxId: string }[]
}

const read = (file: string) => readFile(file, "utf8").catch(() => "")

// The e2e Studio's "~/.ssh/config" is a file in its data directory (cmd/studio/runtime_e2e.go),
// so turning SSH access on here never touches the real one.
test("SSH access names each sandbox in Studio's SSH config, and the user's config includes it while it is on", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("ssh")
  const id = await api.createSandbox(name)
  const st = (await api.json<SSHAccess>("GET", "/api/system/ssh"))!
  expect(path.resolve(st.userConfig)).not.toBe(
    path.join(homedir(), ".ssh", "config")
  )
  expect(st.hosts).toContainEqual({ alias: `${name}.studio`, sandboxId: id })

  // Studio rewrites its config when sandboxes change.
  await expect
    .poll(() => read(st.config))
    .toContain(`Host ${name}.studio\n    HostName ${id}.studio\n`)
  const config = await read(st.config)
  expect(config).toMatch(
    /ProxyCommand ".+" -addr 127\.0\.0\.1:\d+ ssh-proxy -data ".+" %h/
  )
  expect(config).toContain("User agent")

  // Off, the sandbox's SSH dialog offers to turn it on, then names the commands.
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await screen.getByRole("button", { name: `More for ${name}` }).click()
  await page.getByRole("menuitem", { name: "Connect over SSH" }).click()
  const dialog = page.getByRole("dialog", {
    name: `Connect to ${name} over SSH`,
  })
  await expect(dialog.getByLabel("SSH access is off")).toContainText(
    st.userConfig
  )
  await dialog.getByRole("button", { name: "Turn on SSH access" }).click()
  await expect(
    dialog.getByText(`ssh ${name}.studio`, { exact: true })
  ).toBeVisible()
  await expect(
    dialog.getByRole("link", { name: "Open in VS Code" })
  ).toHaveAttribute(
    "href",
    `vscode://vscode-remote/ssh-remote+${name}.studio/workspace`
  )
  expect(await read(st.userConfig)).toContain(`Include "${st.config}"\n`)
  await dialog.getByRole("button", { name: "Close" }).first().click()

  // Settings turns it off again, and the user's config loses the line.
  await page.goto(`/e/${env}/settings/general`)
  const ssh = page.getByRole("switch", { name: "Reach sandboxes over SSH" })
  await expect(ssh).toBeChecked()
  await ssh.click()
  await expect(ssh).not.toBeChecked()
  expect(await read(st.userConfig)).not.toContain("Include")

  // A deleted sandbox leaves the config.
  await api.json("DELETE", `/api/environments/${env}/sandboxes/${id}`)
  await expect.poll(() => read(st.config)).not.toContain(`Host ${name}.studio`)
})
