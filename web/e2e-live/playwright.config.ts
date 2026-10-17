import { defineConfig, devices } from "@playwright/test"

// End-to-end tests against a real Studio that is already running and boots real VMs, as the
// smoke workflow's Linux and Windows jobs run it. STUDIO_LOGIN_URL is a fresh link from `studio
// login-url`; Studio's address is taken from it. The fake-runtime suite is in ../e2e.
const login = process.env.STUDIO_LOGIN_URL

export default defineConfig({
  testDir: ".",
  globalSetup: "./global-setup.ts",
  // One Studio, one login, and the sandboxes are real VMs: one test at a time, no retries
  // (the login link works once).
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  // A first boot pulls images and npm packages.
  timeout: 20 * 60_000,
  expect: { timeout: 30_000 },
  outputDir: "../test-results/live",
  reporter: process.env.CI
    ? [
        ["list"],
        ["html", { open: "never", outputFolder: "../playwright-report/live" }],
      ]
    : [["list"]],
  use: {
    ...devices["Desktop Chrome"],
    baseURL: login ? new URL(login).origin : undefined,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    launchOptions: {
      // PW_CHROMIUM points at an installed Chromium instead of Playwright's download.
      executablePath: process.env.PW_CHROMIUM || undefined,
      // xterm.js falls back to its DOM renderer, so terminal output is readable text.
      args: ["--disable-webgl"],
    },
  },
})
