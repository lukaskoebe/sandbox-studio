import {
  chromium,
  type CDPSession,
  type Page,
  type WebSocketRoute,
} from "@playwright/test"
import { addTeammate, expect, sandboxName, test, type Api } from "./fixtures"

// The live view's click mapping against a real Chromium. The fake browser's frames are drawn
// by the test, so here the live view's socket is answered by a Chromium page instead: its
// screencast frames with their own metadata go to the live view, as agent-browser relays them,
// and the live view's input_mouse goes back as CDP mouse events, as agent-browser sends them.
// Known targets are clicked at several viewer sizes and pixel ratios (a zoomed or HiDPI
// viewer), and with the page pinch-zoomed.

/** The page the far browser shows: buttons at known places, each click recorded. */
const targets = [
  { id: "top-left", x: 16, y: 16 },
  { id: "top-right", x: 1184, y: 16 },
  { id: "middle", x: 600, y: 268 },
  { id: "inner", x: 300, y: 150 },
  { id: "bottom-left", x: 16, y: 521 },
  { id: "bottom-right", x: 1184, y: 521 },
]
const targetW = 80
const targetH = 40
const far = { width: 1280, height: 577 } // agent-browser's page in a browser VM

const farPage = `<!doctype html><html><body style="margin:0;background:#fff">
${targets
  .map(
    (t) =>
      `<button id="${t.id}" style="position:absolute;left:${t.x}px;top:${t.y}px;width:${targetW}px;height:${targetH}px">${t.id}</button>`
  )
  .join("\n")}
<!-- Moves, so the page keeps sending frames; at the top, so a zoom keeps it in view. -->
<div style="position:absolute;left:200px;top:4px;width:8px;height:8px;background:#ccc;animation:spin 1s linear infinite"></div>
<style>@keyframes spin { to { transform: rotate(360deg) } }</style>
<script>
  window.hits = []
  document.addEventListener("click", (e) => window.hits.push({ id: e.target.id, x: e.clientX, y: e.clientY }))
</script>
</body></html>`

/**
 * Answers the live view's socket with the far page's screencast: each frame with its metadata,
 * the latest again when the view connects, and its mouse input dispatched over CDP.
 */
async function relay(page: Page, cdp: CDPSession) {
  let socket: WebSocketRoute | undefined
  let last: string | undefined
  let zoom = 1
  await page.routeWebSocket(/\/browser\/live/, (ws) => {
    socket = ws
    if (last) ws.send(last)
    ws.onMessage(async (raw) => {
      const msg = JSON.parse(String(raw))
      if (msg.type !== "input_mouse") return
      await cdp.send("Input.dispatchMouseEvent", {
        type: msg.eventType,
        x: msg.x,
        y: msg.y,
        button: msg.button,
        clickCount: msg.clickCount,
        modifiers: msg.modifiers,
      })
    })
  })
  cdp.on("Page.screencastFrame", (f) => {
    last = JSON.stringify({ type: "frame", data: f.data, metadata: f.metadata })
    zoom = f.metadata.pageScaleFactor
    socket?.send(last)
    cdp
      .send("Page.screencastFrameAck", { sessionId: f.sessionId })
      .catch(() => {})
  })
  await cdp.send("Page.startScreencast", {
    format: "jpeg",
    quality: 80,
    everyNthFrame: 4,
  })
  return { zoom: () => zoom }
}

/** A teammate with a running fake browser, on its Browser tab, driven by the user. */
async function driving(page: Page, api: Api, env: string) {
  const tm = await addTeammate(
    api,
    env,
    `Ada ${Math.random().toString(36).slice(2, 8)}`
  )
  await api.createSandbox(sandboxName("clicks"), tm)
  const base = `/api/environments/${env}/teammates/${tm}/browser`
  await api.json("POST", `${base}/start`)
  await expect
    .poll(async () => (await api.json<{ vm: string }>("GET", base))!.vm)
    .toBe("running")
  await page.goto(`/e/${env}/teammates/${tm}/browser`)
  const address = page.getByLabel("Browser address")
  await address.fill("https://example.com/clicks")
  await address.press("Enter")
  await expect(page.getByText("You're driving.")).toBeVisible()
}

const viewers = [
  { width: 1280, height: 800, dpr: 1 },
  { width: 1600, height: 1000, dpr: 1.25 },
  { width: 1024, height: 720, dpr: 2 },
  { width: 820, height: 900, dpr: 1.5 },
]

for (const viewer of viewers) {
  test.describe(`a ${viewer.width}×${viewer.height} viewer at ${viewer.dpr}x`, () => {
    test.use({
      viewport: { width: viewer.width, height: viewer.height },
      deviceScaleFactor: viewer.dpr,
    })

    test("clicks on the live view land on the far page's targets", async ({
      page,
      api,
      env,
    }) => {
      // The far browser is a Chromium of its own, as a browser VM's is: a page behind the
      // viewer's window in the same browser gets its frames throttled.
      const farBrowser = await chromium.launch({
        executablePath: process.env.PW_CHROMIUM || undefined,
      })
      try {
        const farContext = await farBrowser.newContext({ viewport: far })
        const target = await farContext.newPage()
        await target.setContent(farPage)
        const cdp = await farContext.newCDPSession(target)
        const frames = await relay(page, cdp)
        await driving(page, api, env)

        const canvas = page.getByLabel("Live view of the browser")
        await expect(canvas).toBeVisible()
        await expect
          .poll(() => canvas.evaluate((c: HTMLCanvasElement) => c.width))
          .toBe(far.width)

        for (const zoom of [1, 1.5]) {
          // A pinch from the top left corner, as a user's or an agent's, so the page
          // still starts at the frame's top left.
          if (zoom !== 1)
            await cdp.send("Input.synthesizePinchGesture", {
              x: 0,
              y: 0,
              scaleFactor: zoom,
            })
          // The live view maps through the frame it shows: wait for one with the new zoom.
          await expect.poll(frames.zoom).toBeCloseTo(zoom, 1)
          await page.waitForTimeout(300)
          const shown = targets.filter(
            (t) =>
              (t.x + targetW) * zoom <= far.width &&
              (t.y + targetH) * zoom <= far.height
          )
          expect(shown.length).toBeGreaterThan(0)
          for (const t of shown) {
            const cx = t.x + targetW / 2
            const cy = t.y + targetH / 2
            // A short window scrolls the live view, as a user would to reach the page's bottom.
            await canvas.evaluate(
              (c, end) => c.scrollIntoView({ block: end ? "end" : "start" }),
              cy * zoom > far.height / 2
            )
            // Where the target's centre is drawn: the frame is fitted into the canvas and
            // centred, and shows the page from its top left, zoom times larger.
            const box = (await canvas.boundingBox())!
            const fw = await canvas.evaluate((c: HTMLCanvasElement) => c.width)
            const fh = await canvas.evaluate((c: HTMLCanvasElement) => c.height)
            const fit = Math.min(box.width / fw, box.height / fh)
            const left = box.x + (box.width - fw * fit) / 2
            const top = box.y + (box.height - fh * fit) / 2
            const before = await target.evaluate(() => window.hits.length)
            await page.mouse.click(
              left + ((cx * zoom) / far.width) * fw * fit,
              top + ((cy * zoom) / far.height) * fh * fit
            )
            await expect
              .poll(() => target.evaluate(() => window.hits.length), {
                message: `a click on ${t.id} at ${zoom}x`,
              })
              .toBe(before + 1)
            const hit = await target.evaluate(() => window.hits.at(-1)!)
            expect(hit.id, `the click meant for ${t.id} at ${zoom}x`).toBe(t.id)
            expect(Math.abs(hit.x - cx)).toBeLessThanOrEqual(3)
            expect(Math.abs(hit.y - cy)).toBeLessThanOrEqual(3)
          }
        }
      } finally {
        await farBrowser.close()
      }
    })
  })
}

declare global {
  interface Window {
    hits: { id: string; x: number; y: number }[]
  }
}
