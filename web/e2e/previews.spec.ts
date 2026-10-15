import { expect, sandboxName, test } from "./fixtures"

// An app's service worker that answers navigations from its cache must not keep a preview
// from logging in, and Open fresh removes it.
test("previews: a service worker can't swallow Studio's login, and Open fresh removes it", async ({
  page,
  api,
  env,
}) => {
  const id = await api.createSandbox(sandboxName("pwa"))
  const open = async (fresh = false) =>
    (await api.json<{ url: string }>(
      "POST",
      `/api/environments/${env}/sandboxes/${id}/previews/3000/open${fresh ? "?fresh=true" : ""}`
    ))!.url
  const login = await open()
  const origin = new URL(login).origin
  await page.goto(login)
  await expect(page.getByText(/Preview of sandbox/)).toBeVisible()

  await page.goto(`${origin}/pwa`)
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
  })
  await page.goto(`${origin}/elsewhere`)
  await expect(page.getByText("cached app")).toBeVisible()

  // Studio's own paths pass the worker: a bad ticket gets Studio's answer, not the cache.
  await page.goto(`${origin}/__studio/preview-login?token=bogus`)
  await expect(
    page.getByText("Open this preview from Sandbox Studio.")
  ).toBeVisible()

  await page.goto(await open(true))
  await expect(
    page.getByText(/Preview of sandbox .* path \/\s*$/)
  ).toBeVisible()
  expect(
    await page.evaluate(
      async () => (await navigator.serviceWorker.getRegistrations()).length
    )
  ).toBe(0)
})
