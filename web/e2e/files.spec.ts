import { expect, sandboxName, test } from "./fixtures"

// Every e2e computer's /workspace has the same few files (internal/e2e/files.go).
test("Files highlights a file, shows the tree beside it, goes to a file by name, and Back and Forward step through the places it showed", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("files")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await screen.getByRole("tab", { name: "Files" }).click()

  // With no file open, the view beside the tree is empty: the tree lists the folders.
  const tree = screen.getByRole("tree", { name: "Workspace" })
  const item = (n: string) =>
    tree.getByRole("treeitem", { name: new RegExp(`^${n}( |$)`) })
  const noFile = screen.getByLabel("No file open")
  await expect(noFile).toContainText("Pick a file in the tree")
  await expect(
    noFile.getByRole("button", { name: /^Upload into workspace/ })
  ).toBeVisible()
  await item("web").click()
  await item("src").click()
  await item("checkout").click()
  await expect(noFile).toBeVisible()
  await item("coupon\\.ts").click()
  await expect(page).toHaveURL(/file=web%2Fsrc%2Fcheckout%2Fcoupon\.ts/)
  const code = screen.locator("pre")
  await expect(code).toContainText("applyCoupon")
  await expect(code.locator(".tok-keyword", { hasText: "import" })).toHaveCount(
    1
  )
  await expect(code.locator(".tok-comment")).toContainText(
    "A code applies once per cart"
  )

  // Back, as Alt+← and the mouse's back button do, goes to the places Files showed rather
  // than another page; Forward comes back.
  await page.goBack()
  await expect(noFile).toBeVisible()
  await expect(item("checkout")).toHaveAttribute("aria-selected", "true")
  await page.goBack()
  await expect(item("src")).toHaveAttribute("aria-selected", "true")
  await expect(page).toHaveURL(/files=web%2Fsrc(&|$)/)
  await page.goForward()
  await page.goForward()
  await expect(code).toContainText("applyCoupon")

  // A place in Files is a link: it opens there again, and Back returns to it from a folder of
  // its path.
  await page.reload()
  await expect(code).toContainText("applyCoupon")
  await screen
    .getByRole("navigation", { name: "Path" })
    .getByRole("button", { name: "checkout" })
    .click()
  await expect(noFile).toBeVisible()
  await page.goBack()
  await expect(code).toContainText("applyCoupon")

  // The tree beside the file has opened to it. A file opens from the tree, whose folders stay
  // open; the keys move through it.
  await expect(item("coupon\\.ts")).toHaveAttribute("aria-selected", "true")
  await expect(item("checkout")).toHaveAttribute("aria-expanded", "true")
  await item("README\\.md").click()
  await expect(code).toContainText("shop with coupons")
  await expect(item("checkout")).toHaveAttribute("aria-expanded", "true")
  await item("README\\.md").press("Home")
  await expect(item("web")).toBeFocused()
  await page.keyboard.press("ArrowLeft")
  await expect(item("web")).toHaveAttribute("aria-expanded", "false")
  await expect(item("checkout")).toHaveCount(0)
  await page.keyboard.press("ArrowRight")
  await expect(item("checkout")).toHaveAttribute("aria-expanded", "true")

  // Go to file: / from anywhere in Files, then the best match opens.
  await page.keyboard.press("/")
  const find = screen.getByRole("combobox", { name: "Go to file" })
  await expect(find).toBeFocused()
  await find.pressSequentially("coup")
  await expect(
    screen
      .getByRole("listbox", { name: "Files to go to" })
      .getByRole("option")
      .first()
  ).toContainText("coupon.ts")
  await find.press("Enter")
  await expect(page).toHaveURL(/file=web%2Fsrc%2Fcheckout%2Fcoupon\.ts/)
  await expect(code).toContainText("applyCoupon")

  // On a phone the tree opens over the file and closes once a file is picked.
  await page.setViewportSize({ width: 390, height: 800 })
  await expect(tree).toBeHidden()
  await screen.getByRole("button", { name: "Show the tree" }).click()
  await item("README\\.md").click()
  await expect(tree).toBeHidden()
  await expect(code).toContainText("shop with coupons")

  // Go files are Go, Markdown is Markdown.
  await page.goto(`/e/${env}/sandboxes/${id}?tab=files&file=main.go`)
  await expect(
    screen.locator("pre .tok-keyword", { hasText: "package" })
  ).toBeVisible()
  // A link from before Files had a tab of its own still opens the file there.
  await page.goto(`/e/${env}/sandboxes/${id}?tab=changes&file=README.md`)
  await expect(
    screen.locator("pre .tok-keyword", { hasText: "Webshop" })
  ).toBeVisible()
})

test("Files browses /tmp from the path's root, without uploads or mentions there", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("files-tmp")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  await screen.getByRole("tab", { name: "Files" }).click()
  const path = screen.getByRole("navigation", { name: "Path" })
  await path.getByRole("button", { name: "Root: /workspace" }).click()
  await page.getByRole("menuitemradio", { name: "/tmp" }).click()

  // The tree shows the whole computer, open at /tmp.
  const tree = screen.getByRole("tree", { name: "Computer" })
  const item = (n: string) =>
    tree.getByRole("treeitem", { name: new RegExp(`^${n}( |$)`) })
  const noFile = screen.getByLabel("No file open")
  await expect(noFile).toContainText("Pick a file of /tmp in the tree")
  await expect(
    noFile.getByRole("button", { name: /^Upload into/ })
  ).toHaveCount(0)
  await item("vite-cache").click()
  await expect(item("deps\\.json")).toBeVisible()
  await item("build\\.log").click()
  await expect(page).toHaveURL(/file=%2Ftmp%2Fbuild\.log/)
  await expect(screen.locator("pre")).toContainText("42 modules transformed")
  await expect(path).toHaveAttribute("title", "/tmp/build.log")
  await expect(
    screen.getByRole("button", { name: "Mention in chat" })
  ).toHaveCount(0)

  // /workspace in the tree is the workspace again.
  await item("workspace").click()
  await expect(path).toHaveAttribute("title", "/workspace")
  await expect(screen.getByRole("tree", { name: "Workspace" })).toBeVisible()
  await path.getByRole("button", { name: "Root: /workspace" }).click()
  await page.getByRole("menuitemradio", { name: "/tmp" }).click()
  // Back to the workspace from the root.
  await path.getByRole("button", { name: "Root: /" }).click()
  await page.getByRole("menuitemradio", { name: "/workspace" }).click()
  await expect(
    screen.getByRole("tree", { name: "Workspace" }).getByRole("treeitem", {
      name: /^README\.md( |$)/,
    })
  ).toBeVisible()
})

test("Files goes to any path typed, a folder or a file, and / finds a file from anywhere", async ({
  page,
  api,
  env,
}) => {
  const name = sandboxName("files-path")
  const id = await api.createSandbox(name)
  await page.goto(`/e/${env}/sandboxes/${id}?tab=files`)
  const screen = page.getByRole("region", { name: `${name}'s screen` })
  const path = screen.getByRole("navigation", { name: "Path" })
  const field = screen.getByRole("textbox", { name: "Path to go to" })

  // The root's menu goes to the whole computer.
  await path.getByRole("button", { name: "Root: /workspace" }).click()
  await page.getByRole("menuitemradio", { name: /^\/ the computer/ }).click()
  const tree = screen.getByRole("tree", { name: "Computer" })
  await expect(tree.getByRole("treeitem", { name: /^etc( |$)/ })).toBeVisible()

  // A typed folder opens in the tree; a typed file opens beside it.
  await path.getByRole("button", { name: "Go to a path" }).click()
  await expect(field).toHaveValue("/")
  await field.fill("/home/agent")
  await field.press("Enter")
  await expect(path).toHaveAttribute("title", "/home/agent")
  await expect(
    tree.getByRole("treeitem", { name: /^\.bashrc( |$)/ })
  ).toBeVisible()
  await path.getByRole("button", { name: "Root: /" }).click()
  await page.getByRole("menuitem", { name: "Go to a path…" }).click()
  await field.fill("/etc/../etc/hostname")
  await field.press("Enter")
  await expect(path).toHaveAttribute("title", "/etc/hostname")
  await expect(screen.locator("pre")).toContainText("shop")

  // A path in /workspace is the workspace's, and Escape keeps the place.
  await path.getByRole("button", { name: "Go to a path" }).click()
  await field.fill("/workspace/main.go")
  await field.press("Enter")
  await expect(page).toHaveURL(/file=main\.go/)
  await path.getByRole("button", { name: "Go to a path" }).click()
  await field.fill("/nowhere")
  await field.press("Escape")
  await expect(field).toHaveCount(0)
  await expect(path).toHaveAttribute("title", "/workspace/main.go")
  await path.getByRole("button", { name: "Go to a path" }).click()
  await field.fill("/nowhere")
  await field.press("Enter")
  await expect(page.getByText("Can't open /nowhere")).toBeVisible()

  // / reaches Go to file also when nothing has the focus.
  await page.evaluate(() => (document.activeElement as HTMLElement)?.blur())
  await page.keyboard.press("/")
  await expect(
    screen.getByRole("combobox", { name: /Go to file/ })
  ).toBeFocused()
})
