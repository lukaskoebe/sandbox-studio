import { expect, sandboxName, test } from "./fixtures"

// The fake guest reports the disks a test sets (PUT /api/e2e/sandboxes/{id}/disks), and the
// e2e Studio asks every second instead of every minute.
test("Home and the sandbox's page say when its home or Docker disk is nearly full, until it has room again", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("disks")
  const id = await api.createSandbox(name)
  await api.json("PUT", `/api/e2e/sandboxes/${id}/disks`, {
    disks: [
      { name: "home", free: 150 * 2 ** 20, total: 4 * 2 ** 30 },
      { name: "workspace", free: 9 * 2 ** 30, total: 10 * 2 ** 30 },
    ],
  })

  await page.goto(`/e/${env}`)
  const notice = page.getByRole("region", { name: "Disk space" })
  await expect(notice).toContainText(
    `${name}: The home disk has 150 MB of 4.0 GB free.`,
    { timeout: 15_000 }
  )
  await expect(notice).not.toContainText("workspace disk")
  await notice.getByRole("link", { name }).click()

  const strip = page.getByRole("status", { name: "Disk space" })
  await expect(strip).toContainText("The home disk has 150 MB of 4.0 GB free")
  await expect(strip).toContainText("rm -rf ~/.cache")

  // A nearly full Docker disk says how to free Docker's space instead.
  await api.json("PUT", `/api/e2e/sandboxes/${id}/disks`, {
    disks: [
      { name: "home", free: 3 * 2 ** 30, total: 4 * 2 ** 30 },
      { name: "docker", free: 300 * 2 ** 20, total: 20 * 2 ** 30 },
    ],
  })
  await expect(strip).toContainText(
    "The Docker disk has 300 MB of 20 GB free",
    { timeout: 15_000 }
  )
  await expect(strip).toContainText("docker builder prune")
  await expect(strip).not.toContainText("rm -rf ~/.cache")

  await api.json("PUT", `/api/e2e/sandboxes/${id}/disks`, { disks: null })
  await expect(strip).toBeHidden({ timeout: 15_000 })
})
