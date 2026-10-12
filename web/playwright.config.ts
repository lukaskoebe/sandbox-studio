import { defineConfig, devices } from "@playwright/test"

// End-to-end tests against Studio built with in-process fakes (`make e2e`, see
// CONTRIBUTING.md). Each worker runs its own Studio, so two workers are plenty.
export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  workers: 2,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI
    ? [["list"], ["html", { open: "never" }]]
    : [["list"]],
  use: {
    ...devices["Desktop Chrome"],
    trace: "retain-on-failure",
    launchOptions: {
      // PW_CHROMIUM points at an installed Chromium instead of Playwright's download.
      executablePath: process.env.PW_CHROMIUM || undefined,
      // xterm.js falls back to its DOM renderer, so terminal output is readable text.
      args: ["--disable-webgl"],
    },
  },
})
