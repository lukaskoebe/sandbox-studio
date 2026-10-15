import { expect, test } from "./fixtures"

// Studio is an installable app: Chromium finds its manifest and icons and reports nothing in
// the way of installing it.
test("Studio can be installed as an app", async ({ page, env }) => {
  await page.goto(`/e/${env}`)
  const cdp = await page.context().newCDPSession(page)
  const manifest = (await cdp.send("Page.getAppManifest")) as {
    url: string
    errors: { message: string }[]
  }
  expect(manifest.url).toMatch(/\/manifest\.webmanifest$/)
  expect(manifest.errors).toEqual([])
  await expect
    .poll(async () => {
      const { installabilityErrors } = (await cdp.send(
        "Page.getInstallabilityErrors"
      )) as { installabilityErrors: { errorId: string }[] }
      // Playwright's contexts are incognito, where nothing installs.
      return installabilityErrors
        .map((e) => e.errorId)
        .filter((id) => id !== "in-incognito")
    })
    .toEqual([])
})
