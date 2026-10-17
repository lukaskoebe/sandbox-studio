// Renders a landing page video frame by frame and encodes it.
//
//   node video/render.mjs <name> [--fps 60] [--stills 1,4.5,9 --out dir]
//
// <name> is a page in this folder, such as everyone or developers. Each frame is a screenshot
// of the page after seek(t), so the result doesn't depend on how fast the machine is. The
// videos land in public/video/<name>.mp4 and .webm with a poster, <name>.jpg. --stills
// writes PNGs of the given times instead, for checking a scene. It uses an installed
// Chromium (CHROMIUM, default /snap/bin/chromium) and ffmpeg with libx264 and libvpx-vp9.
//
// The lossless master is kept in $TMPDIR/sandbox-studio-video/<name>.mkv until both encodings
// are done, and a later run encodes from it instead of rendering again; a run that stops
// while rendering continues from its last complete ten-second segment. Point TMPDIR at a
// disk: the master is several hundred MB, and /tmp is often memory.
import { spawn } from "node:child_process"
import { existsSync } from "node:fs"
import { mkdir, rename, rm, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"
import { chromium } from "playwright-core"

const args = process.argv.slice(2)
const name = args[0]
const opt = (flag, def) => {
  const i = args.indexOf(flag)
  return i >= 0 ? args[i + 1] : def
}
if (!name) {
  console.error("usage: node video/render.mjs <name> [--fps 60] [--stills 1,4.5 --out dir]")
  process.exit(2)
}
const fps = Number(opt("--fps", 60))
const here = fileURLToPath(new URL(".", import.meta.url))
const publicDir = fileURLToPath(new URL("../public/video/", import.meta.url))

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM ?? "/snap/bin/chromium",
  headless: true,
  args: ["--force-color-profile=srgb", "--font-render-hinting=none"],
})
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 })
await page.goto(`${pathToFileURL(join(here, `${name}.html`))}?render`)
await page.evaluate(() => document.fonts.ready)
const duration = await page.evaluate(() => window.DURATION)
const stage = page.locator(".stage")

const stills = opt("--stills")
if (stills) {
  const out = opt("--out", join(tmpdir(), "video-stills"))
  await mkdir(out, { recursive: true })
  for (const t of stills.split(",").map(Number)) {
    await page.evaluate((t) => window.seek(t), t)
    await stage.screenshot({ path: join(out, `${name}-${t.toFixed(2)}.png`) })
  }
  console.log(`stills in ${out}`)
  await browser.close()
  process.exit(0)
}

function ffmpeg(argv) {
  const p = spawn("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", ...argv], {
    stdio: ["pipe", "inherit", "inherit"],
  })
  const done = new Promise((resolve, reject) =>
    p.on("exit", (code) => (code === 0 ? resolve() : reject(new Error(`ffmpeg exited ${code}`))))
  )
  return { stdin: p.stdin, done }
}

// Frames go into a lossless master first, which both encodings are made from. It is rendered
// in segments of ten seconds, each written under a temporary name and renamed when complete,
// so an interrupted run picks up at the first missing segment.
const work = join(process.env.TMPDIR ?? tmpdir(), "sandbox-studio-video")
await mkdir(work, { recursive: true })
const master = join(work, `${name}.mkv`)
if (existsSync(master)) {
  console.log(`${name}: encoding the master from an earlier run, ${master}`)
} else {
  const frames = Math.round(duration * fps)
  const perSegment = fps * 10
  const segments = []
  const started = Date.now()
  for (let first = 0; first < frames; first += perSegment) {
    const segment = join(work, `${name}.${String(first / perSegment).padStart(3, "0")}.mkv`)
    segments.push(segment)
    if (existsSync(segment)) continue
    const partial = `${segment}.partial.mkv`
    const enc = ffmpeg(["-f", "image2pipe", "-framerate", String(fps), "-c:v", "png", "-i", "-",
      "-c:v", "libx264", "-preset", "ultrafast", "-qp", "0", "-pix_fmt", "yuv444p", partial])
    for (let i = first; i < Math.min(frames, first + perSegment); i++) {
      await page.evaluate((t) => window.seek(t), i / fps)
      const png = await stage.screenshot({ type: "png" })
      if (!enc.stdin.write(png)) await new Promise((r) => enc.stdin.once("drain", r))
    }
    enc.stdin.end()
    await enc.done
    await rename(partial, segment)
    const s = (Date.now() - started) / 1000
    console.log(`${name}: ${Math.min(frames, first + perSegment)}/${frames} frames, ${s.toFixed(0)} s`)
  }
  const list = join(work, `${name}.txt`)
  await writeFile(list, segments.map((f) => `file '${f}'`).join("\n"))
  await ffmpeg(["-f", "concat", "-safe", "0", "-i", list, "-c", "copy", `${master}.partial.mkv`]).done
  await rename(`${master}.partial.mkv`, master)
  for (const f of [...segments, list]) await rm(f, { force: true })
}
await browser.close()

await mkdir(publicDir, { recursive: true })
const out = (ext) => join(publicDir, `${name}.${ext}`)
// H.264 for every browser, VP9 for a smaller file where it plays. Few threads keep the
// encoders' memory down; each encoding writes to a temporary name first.
const encode = async (ext, argv) => {
  await ffmpeg(["-i", master, ...argv, "-an", `${out(ext)}.part.${ext}`]).done
  await rename(`${out(ext)}.part.${ext}`, out(ext))
}
await encode("mp4", ["-c:v", "libx264", "-preset", "veryslow", "-crf", "20", "-tune", "animation",
  "-threads", "4", "-pix_fmt", "yuv420p", "-movflags", "+faststart"])
await encode("webm", ["-c:v", "libvpx-vp9", "-b:v", "0", "-crf", "34", "-row-mt", "1",
  "-threads", "4", "-deadline", "good", "-cpu-used", "2", "-pix_fmt", "yuv420p"])
const poster = Number(opt("--poster", Math.min(duration - 0.1, 3.5)))
await ffmpeg(["-ss", String(poster), "-i", master, "-frames:v", "1", "-q:v", "3", out("jpg")]).done
await rm(master, { force: true })
console.log(`${name}: ${out("mp4")}, .webm and .jpg`)
