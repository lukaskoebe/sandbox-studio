// A tiny deterministic motion engine for the landing page videos. Everything on screen is a
// function of the time t (in seconds): render.mjs calls seek(t) for each frame and takes a
// screenshot, and opening a video's page in a browser plays it in real time (Space pauses,
// the arrow keys step, a click on the bar seeks).
;(function () {
  const clamp = (v, a = 0, b = 1) => Math.min(b, Math.max(a, v))
  const lerp = (a, b, k) => a + (b - a) * k
  const ease = {
    linear: (k) => k,
    inCubic: (k) => k * k * k,
    outCubic: (k) => 1 - Math.pow(1 - k, 3),
    outQuart: (k) => 1 - Math.pow(1 - k, 4),
    outExpo: (k) => (k >= 1 ? 1 : 1 - Math.pow(2, -10 * k)),
    inOutCubic: (k) => (k < 0.5 ? 4 * k * k * k : 1 - Math.pow(-2 * k + 2, 3) / 2),
    inOutQuart: (k) => (k < 0.5 ? 8 * k * k * k * k : 1 - Math.pow(-2 * k + 2, 4) / 2),
    outBack: (k) => {
      const c1 = 1.70158
      const c3 = c1 + 1
      return 1 + c3 * Math.pow(k - 1, 3) + c1 * Math.pow(k - 1, 2)
    },
    // A damped spring that settles by k = 1.
    spring: (k) => (k >= 1 ? 1 : 1 - Math.exp(-6 * k) * Math.cos(k * Math.PI * 2.5)),
  }

  /** Progress from 0 to 1 between times a and b, eased. */
  function p(t, a, b, e = ease.outCubic) {
    if (b <= a) return t >= a ? 1 : 0
    return e(clamp((t - a) / (b - a)))
  }

  /** Sets opacity and a transform on an element; unset fields stay neutral. */
  function set(el, o) {
    if (!el) return
    if (o.o !== undefined) el.style.opacity = String(clamp(o.o))
    const tr = []
    if (o.x || o.y || o.z) tr.push(`translate3d(${o.x || 0}px, ${o.y || 0}px, ${o.z || 0}px)`)
    if (o.r) tr.push(`rotate(${o.r}deg)`)
    if (o.rx) tr.push(`rotateX(${o.rx}deg)`)
    if (o.ry) tr.push(`rotateY(${o.ry}deg)`)
    if (o.s !== undefined) tr.push(`scale(${o.s})`)
    if (o.sx !== undefined || o.sy !== undefined)
      tr.push(`scale(${o.sx ?? 1}, ${o.sy ?? 1})`)
    el.style.transform = tr.join(" ")
    if (o.blur !== undefined) el.style.filter = o.blur > 0.05 ? `blur(${o.blur}px)` : "none"
    if (o.w !== undefined) el.style.width = `${o.w}px`
  }

  /** Rises and fades an element in at a, and out at b (if given). */
  function rise(el, t, a, b, dist = 28, dur = 0.7) {
    const k = p(t, a, a + dur, ease.outQuart)
    const out = b === undefined ? 0 : p(t, b, b + 0.45, ease.inCubic)
    set(el, { o: k * (1 - out), y: (1 - k) * dist - out * 12, blur: (1 - k) * 8 + out * 6 })
  }

  /**
   * Splits an element's text into word spans once, so they can be revealed one by one. Child
   * elements, such as a <br> or an accent <span>, are kept, and their words split too.
   */
  function words(el) {
    if (el._words) return el._words
    const out = []
    const split = (node) => {
      for (const child of [...node.childNodes]) {
        if (child.nodeType === Node.ELEMENT_NODE) {
          split(child)
          continue
        }
        if (child.nodeType !== Node.TEXT_NODE) continue
        const frag = document.createDocumentFragment()
        for (const w of child.textContent.split(/(\s+)/)) {
          if (w === "") continue
          if (/^\s+$/.test(w)) {
            frag.appendChild(document.createTextNode(w))
            continue
          }
          const s = document.createElement("span")
          s.className = "w"
          s.textContent = w
          frag.appendChild(s)
          out.push(s)
        }
        node.replaceChild(frag, child)
      }
    }
    split(el)
    el._words = out
    return out
  }

  /** Reveals an element's words one after another from a, and fades them out at b. */
  function reveal(el, t, a, b, stagger = 0.055) {
    const ws = words(el)
    ws.forEach((w, i) => {
      const k = p(t, a + i * stagger, a + i * stagger + 0.75, ease.outQuart)
      const out = b === undefined ? 0 : p(t, b + i * 0.02, b + i * 0.02 + 0.4, ease.inCubic)
      set(w, { o: k * (1 - out), y: (1 - k) * 34 - out * 14, blur: (1 - k) * 10 + out * 6 })
    })
  }

  /** Types text into an element from a, at cps characters a second; returns whether done. */
  function type(el, text, t, a, cps = 28) {
    const n = Math.floor(clamp((t - a) * cps, 0, text.length))
    el.textContent = t < a ? "" : text.slice(0, n)
    return n >= text.length
  }

  /** A caret that blinks while idle and stays on while typing. */
  function caret(el, t, show = true, typing = false) {
    if (!el) return
    el.style.opacity = show && (typing || Math.floor(t * 1.9) % 2 === 0) ? "1" : "0"
  }

  /** Animates a number from a to b between times. */
  function count(el, t, a, b, from, to, fmt = (v) => String(Math.round(v))) {
    el.textContent = fmt(lerp(from, to, p(t, a, b, ease.outCubic)))
  }

  const scenes = []
  /**
   * A scene shows between start and end; update gets the time since start and the scene's
   * length. Scenes overlap for crossfades: each one handles its own entrance and exit.
   */
  function scene(start, end, el, update) {
    scenes.push({ start, end, el, update })
  }

  const always = []
  /** Runs on every frame, for backgrounds and anything that spans scenes. */
  function each(update) {
    always.push(update)
  }

  let duration = 0
  function seek(t) {
    for (const s of scenes) {
      const on = t >= s.start && t < s.end
      s.el.style.visibility = on ? "visible" : "hidden"
      if (on) s.update(t - s.start, s.end - s.start, t)
    }
    for (const u of always) u(t)
    window.__t = t
  }

  function start(total) {
    duration = total
    window.DURATION = total
    window.seek = seek
    const params = new URLSearchParams(location.search)
    if (params.has("render")) {
      document.documentElement.classList.add("render")
      seek(0)
      return
    }
    // Playback in a browser, for working on a video.
    let t = Number(params.get("t") || 0)
    let playing = true
    let last = performance.now()
    const bar = document.createElement("div")
    bar.className = "scrub"
    bar.innerHTML = '<div class="scrub-fill"></div><div class="scrub-time"></div>'
    document.body.appendChild(bar)
    bar.addEventListener("click", (e) => {
      t = (e.clientX / window.innerWidth) * duration
    })
    window.addEventListener("keydown", (e) => {
      if (e.key === " ") playing = !playing
      if (e.key === "ArrowRight") t = Math.min(duration, t + (e.shiftKey ? 5 : 1 / 30))
      if (e.key === "ArrowLeft") t = Math.max(0, t - (e.shiftKey ? 5 : 1 / 30))
    })
    const frame = (now) => {
      if (playing) t += (now - last) / 1000
      last = now
      if (t >= duration) t = 0
      seek(t)
      bar.firstChild.style.width = `${(t / duration) * 100}%`
      bar.lastChild.textContent = t.toFixed(2)
      requestAnimationFrame(frame)
    }
    requestAnimationFrame(frame)
  }

  /** Fits the 1920×1080 stage into the window when previewing. */
  function fit() {
    const stage = document.querySelector(".stage")
    if (!stage || document.documentElement.classList.contains("render")) return
    const k = Math.min(window.innerWidth / 1920, window.innerHeight / 1080)
    stage.style.transform = `scale(${k})`
  }
  window.addEventListener("resize", fit)
  window.addEventListener("load", fit)

  window.M = { clamp, lerp, ease, p, set, rise, words, reveal, type, caret, count, scene, each, start, $: (s, r = document) => r.querySelector(s), $$: (s, r = document) => [...r.querySelectorAll(s)] }
})()
