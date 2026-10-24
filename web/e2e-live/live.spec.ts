import { test as base, expect, type Page } from "@playwright/test"

// One pass through Studio on a real VM: create a computer, run a container in it, approve a
// host it asks for, and open a preview of a server it runs. See playwright.config.ts.

const test = base.extend({
  // The session global-setup.ts logged in with. Playwright wants an object pattern for
  // fixtures; this one takes none.
  // eslint-disable-next-line no-empty-pattern
  storageState: async ({}, use) =>
    use(JSON.parse(process.env.STUDIO_LIVE_STATE ?? "{}")),
})

/**
 * The visible terminal, a real shell. xterm.js renders rows as DOM text (WebGL is off).
 * Each command clears the screen first, so its output is the only text on it.
 */
function terminal(page: Page) {
  const root = page.locator(".xterm").filter({ visible: true })
  const rows = root.locator(".xterm-rows")
  return {
    rows,
    async run(command: string) {
      // Focus rather than a click: tmux takes two clicks within 300 ms as a double-click,
      // which enters copy mode, and a fast command's result comes back sooner than that.
      await root.locator(".xterm-helper-textarea").focus()
      await page.keyboard.type(`clear; ${command}`)
      await page.keyboard.press("Enter")
    },
    /**
     * Waits for a line "<key>=<value>" a command printed and returns the value. The command
     * prints it as "<key>=$..." so that its own echo never matches. The rows are read as
     * inner text, one line each: their text content runs a line into the next row's prompt.
     */
    async result(key: string, timeout: number) {
      const pattern = new RegExp(`${key}=([\\w-]+)`)
      await expect(rows).toContainText(pattern, { timeout, useInnerText: true })
      return pattern.exec(await rows.innerText())![1]
    },
  }
}

test("a real sandbox runs a container, asks for a host and previews a server", async ({
  page,
}) => {
  const envs = (await (await page.request.get("/api/environments")).json()) as {
    id: string
  }[]
  const env = envs[0].id
  const level = (name: string) =>
    page.request.put(`/api/environments/${env}/security-level`, {
      data: { level: name },
    })

  // Previews are off by default: the scenario turns them on, and back as they were at the end.
  const previews = (await (
    await page.request.get("/api/system/previews")
  ).json()) as { enabled: boolean }
  await page.request.put("/api/system/previews", { data: { enabled: true } })

  // Create the computer the way a user does, from the sidebar.
  const name = `live-${Math.random().toString(36).slice(2, 8)}`
  await page.goto(`/e/${env}`)
  await page.getByRole("button", { name: "New computer" }).click()
  await page.getByRole("menuitem", { name: /^New computer/ }).click()
  const create = page.getByRole("dialog", { name: "New computer" })
  await create.getByLabel("Name", { exact: true }).fill(name)
  await create.getByRole("button", { name: "Create" }).click()
  // A computer without a teammate opens on its screen, at /e/<env>/sandboxes/<id>.
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await expect(screen).toBeVisible({ timeout: 10 * 60_000 })
  await expect(page).toHaveURL(/\/sandboxes\/[a-z0-9]+$/)
  const id = page.url().match(/\/sandboxes\/([a-z0-9]+)$/)![1]

  try {
    const term = terminal(page)
    // The first boot can take a while before the shell prompts.
    await expect(term.rows).toContainText("$", { timeout: 10 * 60_000 })

    // Docker in the VM, when the base image has it. Docker Hub's hosts are on the curated
    // list, but the CDN its layers come from may not be: Open lets the pull through
    // without a card per host.
    await term.run(
      `d=no; command -v docker >/dev/null && d=yes; echo "docker=$d"`
    )
    if ((await term.result("docker", 60_000)) === "yes") {
      expect((await level("open")).ok()).toBe(true)
      // Docker's output stays on screen, so a failed test's trace shows where it stopped.
      await term.run(
        `for i in $(seq 120); do docker info >/dev/null 2>&1 && break; sleep 1; done; ` +
          `r=failed; docker run --rm hello-world 2>&1 | tee /tmp/hello; ` +
          `grep -q 'Hello from Docker!' /tmp/hello && r=ok; echo "hello=$r"`
      )
      expect(await term.result("hello", 10 * 60_000)).toBe("ok")
    } else {
      test.info().annotations.push({
        type: "skipped",
        description: "docker run hello-world: the base image has no docker",
      })
    }

    // Locked asks for every host, so the npm registry, on the curated list, gets a card.
    expect((await level("locked")).ok()).toBe(true)
    await term.run(
      `c=$(curl -sS -o /dev/null --max-time 600 -w '%{http_code}' https://registry.npmjs.org/); echo "npm=$c"`
    )
    const card = page
      .getByRole("region", { name: "Approvals" })
      .getByTestId("approval-card")
      .filter({ hasText: "registry.npmjs.org" })
    await expect(card).toBeVisible({ timeout: 2 * 60_000 })
    await card.getByRole("button", { name: "Allow" }).click()
    await expect(card).toBeHidden()
    expect(await term.result("npm", 2 * 60_000)).toBe("200")

    // A Vite server, installed from the registry the rule now allows.
    await term.run(
      `mkdir -p ~/live-preview && cd ~/live-preview && ` +
        `echo '<h1>Live preview works</h1>' > index.html && ` +
        `echo 'export default { server: { allowedHosts: true } }' > vite.config.mjs && ` +
        `npx --yes vite@6.3.5 --host 0.0.0.0 --port 5173 --strictPort`
    )
    await expect(term.rows).toContainText("ready in", { timeout: 5 * 60_000 })

    // The screen's Preview tab lists the listening ports; Open in new tab opens one.
    await page
      .getByRole("tablist", { name: `${name}'s screen` })
      .getByRole("tab", { name: "Preview" })
      .click()
    const port = screen.getByRole("combobox", { name: "Port" })
    await expect(port).toBeVisible({ timeout: 60_000 })
    await port.click()
    await page.getByRole("option", { name: "Port 5173", exact: true }).click()
    const [preview] = await Promise.all([
      page.context().waitForEvent("page"),
      screen.getByRole("button", { name: "Open in new tab" }).click(),
    ])
    await expect(
      preview.getByRole("heading", { name: "Live preview works" })
    ).toBeVisible({ timeout: 60_000 })
  } finally {
    // Back to the default level and the previews setting, and the VM gone.
    await level("guarded")
    await page.request.put("/api/system/previews", {
      data: { enabled: previews.enabled },
    })
    await page.request.delete(`/api/environments/${env}/sandboxes/${id}`)
  }
})
