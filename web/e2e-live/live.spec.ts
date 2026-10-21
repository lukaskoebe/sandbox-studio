import { test as base, expect, type Page } from "@playwright/test"

// One pass through Studio on a real VM: create a sandbox, run a container in it, approve a
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
      await root.click()
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

  // Create the sandbox the way a user does.
  const name = `live-${Math.random().toString(36).slice(2, 8)}`
  await page.goto(`/e/${env}`)
  await page.getByRole("button", { name: "More to create" }).click()
  await page
    .getByRole("menuitem", { name: /Sandbox without a teammate/ })
    .click()
  const create = page.getByRole("dialog", { name: "New sandbox" })
  await create.getByLabel("Name", { exact: true }).fill(name)
  await create.getByRole("button", { name: "Create" }).click()
  await expect(page.getByRole("heading", { name })).toBeVisible({
    timeout: 10 * 60_000,
  })
  // A sandbox opens on its workspace, at /e/<env>/sandboxes/<id>/work.
  const id = page.url().match(/\/sandboxes\/([a-z0-9]+)/)![1]

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

    // The workspace's Previews menu lists the listening ports.
    await expect(page.getByText("ports :5173")).toBeVisible({
      timeout: 60_000,
    })
    await page.getByRole("button", { name: "Previews" }).click()
    const [preview] = await Promise.all([
      page.context().waitForEvent("page"),
      page.getByRole("menuitem", { name: ":5173", exact: true }).click(),
    ])
    await expect(
      preview.getByRole("heading", { name: "Live preview works" })
    ).toBeVisible({ timeout: 60_000 })
  } finally {
    // Back to the default level, and the VM gone.
    await level("guarded")
    await page.request.delete(`/api/environments/${env}/sandboxes/${id}`)
  }
})
