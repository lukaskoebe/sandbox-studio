import { addTeammate, expect, sandboxName, test } from "./fixtures"

// A teammate offers models besides its default; a thread picks one in its composer, and the
// scripted agent (internal/e2e/acp.go) is switched to it before the next prompt. It refuses
// the model "refused", as an agent does a model it doesn't know.
const done = "The tests passed."

test("a thread picks one of the models its teammate offers, from the next message", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = sandboxName("Modeler")
  const teammate = await addTeammate(api, env, name)
  const id = await api.createSandbox(sandboxName("models"), teammate)

  // The teammate offers two more models besides its default.
  await page.goto(`/e/${env}/teammates/${teammate}?profile=settings&open=agent`)
  await expect(page.getByLabel("Default model")).toHaveValue(
    "claude-sonnet-5-5"
  )
  // The provider's models are suggested; one picked becomes a token.
  const offered = page.getByLabel("Also offered in chats")
  await offered.click()
  await page
    .getByRole("listbox")
    .getByRole("option", { name: "claude-opus-5-5" })
    .click()
  await expect(
    page.getByRole("button", { name: "Remove claude-opus-5-5" })
  ).toBeVisible()
  await offered.fill("refused ")
  await expect(
    page.getByRole("button", { name: "Remove refused" })
  ).toBeVisible()
  const bar = page.getByRole("region", { name: "Unsaved changes" })
  await bar.getByRole("button", { name: "Save", exact: true }).click()
  await expect(bar).toBeHidden()
  expect(
    await api.json<{ models: string[] }>(
      "GET",
      `/api/environments/${env}/teammates/${teammate}`
    )
  ).toMatchObject({ models: ["claude-opus-5-5", "refused"] })

  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  const box = main.getByRole("textbox", { name: "Message" })
  const allow = main.getByRole("button", { name: "Allow", exact: true })

  // A new thread starts on the default.
  const picker = main.getByRole("button", { name: /^Model: / })
  await expect(picker).toHaveAccessibleName("Model: claude-sonnet-5-5")
  await box.fill("Please run the tests")
  await box.press("Enter")
  await allow.click()
  await expect(main.getByText(done)).toHaveCount(1)

  // Another model applies from the next message, and the thread says where it changed.
  await picker.click()
  const menu = page.getByRole("menu")
  await expect(menu).toContainText("Model for this thread")
  await expect(
    menu.getByRole("menuitemradio", { name: /claude-sonnet-5-5\s*default/ })
  ).toBeChecked()
  await menu.getByRole("menuitemradio", { name: "claude-opus-5-5" }).click()
  await expect(picker).toHaveAccessibleName("Model: claude-opus-5-5")
  await expect
    .poll(
      async () =>
        (
          await api.json<{ chat: { model?: string } }>(
            "GET",
            `/api/environments/${env}/chats/${chat.id}`
          )
        )?.chat.model
    )
    .toBe("claude-opus-5-5")
  await expect(main).not.toContainText("Switched to")
  await box.fill("Now on the bigger model")
  await box.press("Enter")
  await allow.click()
  await expect(main.getByText(done)).toHaveCount(2)
  await expect(main).toContainText("Switched to claude-opus-5-5")

  // A model the agent refuses says so, and the thread stays as it was.
  await picker.click()
  await page
    .getByRole("menu")
    .getByRole("menuitemradio", { name: "refused" })
    .click()
  await box.fill("And on one it doesn't know")
  await box.press("Enter")
  await expect(main).toContainText("can't switch this chat to refused")
  await expect(main.getByText(done)).toHaveCount(2)

  const agents = (await api.guest(id)).acp
  expect(agents).toHaveLength(1)
  expect(agents[0].models).toEqual(["claude-opus-5-5"])
  expect(agents[0].prompts).toHaveLength(2)

  // The thread keeps its model when it opens again.
  await page.reload()
  await expect(
    page.getByRole("main").getByRole("button", { name: /^Model: / })
  ).toHaveAccessibleName("Model: refused")
})

test("a new thread from the teammate's home starts on the model picked there", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const name = sandboxName("Starter")
  const teammate = await addTeammate(api, env, name, {
    models: ["claude-opus-5-5"],
  })
  const id = await api.createSandbox(sandboxName("start"), teammate)
  await api.guestReady(id)

  await page.goto(`/e/${env}/teammates/${teammate}`)
  const main = page.getByRole("main")
  await main.getByRole("button", { name: "Model: claude-sonnet-5-5" }).click()
  await page
    .getByRole("menu")
    .getByRole("menuitemradio", { name: "claude-opus-5-5" })
    .click()
  await main
    .getByRole("textbox", { name: "Message" })
    .fill("Please run the tests")
  await main.getByRole("button", { name: "Send" }).click()
  await expect(
    main.getByRole("button", { name: "Model: claude-opus-5-5" })
  ).toBeVisible()
  await expect
    .poll(async () => (await api.guest(id)).acp.at(0)?.models)
    .toEqual(["claude-opus-5-5"])
})
