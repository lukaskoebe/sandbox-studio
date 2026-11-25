import { crc32, deflateSync } from "node:zlib"
import type { Locator } from "@playwright/test"
import { addTeammate, expect, sandboxName, test } from "./fixtures"

/** A width × height PNG of a gradient. */
function png(width: number, height: number) {
  const chunk = (type: string, data: Buffer) => {
    const body = Buffer.concat([Buffer.from(type), data])
    const out = Buffer.alloc(body.length + 8)
    out.writeUInt32BE(data.length, 0)
    body.copy(out, 4)
    out.writeUInt32BE(crc32(body), body.length + 4)
    return out
  }
  const rows = Buffer.alloc((width * 3 + 1) * height)
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++)
      rows.set([40 + x * 4, 90 + y * 4, 200], y * (width * 3 + 1) + 1 + x * 3)
  const header = Buffer.alloc(13)
  header.writeUInt32BE(width, 0)
  header.writeUInt32BE(height, 4)
  header.set([8, 2, 0, 0, 0], 8)
  return Buffer.concat([
    Buffer.from("\x89PNG\r\n\x1a\n", "latin1"),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(rows)),
    chunk("IEND", Buffer.alloc(0)),
  ])
}

const loaded = (img: Locator) =>
  expect
    .poll(() => img.evaluate((e: HTMLImageElement) => e.naturalWidth))
    .toBe(48)

test("attached pictures show as thumbnails in the composer and the thread, and large on a click", async ({
  page,
  api,
  env,
}) => {
  await api.dismissApprovals()
  const teammate = await addTeammate(api, env, `Looker ${sandboxName("")}`)
  const id = await api.createSandbox(sandboxName("pics"), teammate)
  await api.guestReady(id)
  const chat = (await api.json<{ id: string }>(
    "POST",
    `/api/environments/${env}/sandboxes/${id}/chats`
  ))!
  await page.goto(`/e/${env}/chats/${chat.id}`)
  const main = page.getByRole("main")
  await main.locator('input[type="file"]').setInputFiles([
    { name: "shot.png", mimeType: "image/png", buffer: png(48, 32) },
    { name: "other.png", mimeType: "image/png", buffer: png(48, 32) },
    {
      name: "notes-from-the-checkout-run.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("hi"),
    },
  ])

  // A picture is a thumbnail with its ✕; another file stays a chip with its size.
  const shot = main.getByRole("img", { name: "shot.png" })
  await loaded(shot)
  await expect(main.getByText("notes-from-the-checkout-run.txt")).toBeVisible()
  await expect(main.getByText("2 B")).toBeVisible()
  await main.getByRole("button", { name: "Remove other.png" }).click()
  await expect(main.getByRole("img", { name: "other.png" })).toHaveCount(0)
  await main.getByRole("button", { name: "Show shot.png large" }).click()
  const large = page.getByRole("dialog", { name: "shot.png" })
  await loaded(large.getByRole("img", { name: "shot.png" }))
  await page.keyboard.press("Escape")
  await expect(large).toBeHidden()

  await main
    .getByRole("textbox", { name: "Message" })
    .fill("What's wrong here?")
  await main.getByRole("button", { name: "Send" }).click()
  // Once sent, the thread shows the picture Studio kept, after a reload too.
  const sent = main.getByRole("img", { name: "shot.png" })
  await expect(sent).toHaveAttribute("src", /\/image\?src=attachment%3A0$/)
  await loaded(sent)
  await expect(main.getByText("notes-from-the-checkout-run.txt")).toBeVisible()
  await page.reload()
  await loaded(sent)
  // On a phone the files run past the message's width; the row scrolls from the first one.
  await page.setViewportSize({ width: 390, height: 844 })
  const strip = main.locator("[data-slot=attachment-group]").last()
  await expect
    .poll(() =>
      strip.evaluate((el) => {
        const [row, message] = [
          el.getBoundingClientRect(),
          el.parentElement!.getBoundingClientRect(),
        ]
        const first = el.firstElementChild!.getBoundingClientRect()
        return [
          el.scrollWidth > el.clientWidth,
          row.left >= message.left - 1 && first.left >= row.left - 1,
        ]
      })
    )
    .toEqual([true, true])
  await page.setViewportSize({ width: 1280, height: 800 })
  await main.getByRole("button", { name: "Show shot.png large" }).click()
  await loaded(large.getByRole("img", { name: "shot.png" }))
  await page.keyboard.press("Escape")

  // The scripted turn asks before it goes on.
  await main.getByRole("button", { name: "Allow once", exact: true }).click()
  await expect(main).toContainText("The tests passed.")
})
