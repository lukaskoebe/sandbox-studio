// Helpers for the overview video, on top of engine.js (M).
;(function () {
  const { p, set, ease, lerp } = M
  const byId = (id) => document.getElementById(id)

  // The background drifts the whole time, as in the first two videos.
  M.each((t) => {
    set(byId("glow1"), { x: Math.sin(t * 0.21) * 160, y: Math.cos(t * 0.17) * 90 })
    set(byId("glow2"), { x: 600 + Math.cos(t * 0.13) * 220, y: 420 + Math.sin(t * 0.19) * 120 })
    set(byId("grid"), { x: -((t * 6) % 40), y: -((t * 3) % 40) })
  })

  // The brand cube, the logo: any element with data-cube gets it.
  for (const el of document.querySelectorAll("[data-cube]")) {
    el.insertAdjacentHTML(
      "afterbegin",
      '<svg viewBox="0 0 512 512"><g stroke="#5cc3d1" stroke-width="16" stroke-linejoin="round">' +
        '<path class="f-top" d="M256 120 384 194 256 268 128 194Z" fill="#5cc3d1"/>' +
        '<path class="f-left" d="M128 194 256 268V416L128 342Z" fill="#5cc3d1" fill-opacity="0.35"/>' +
        '<path class="f-right" d="M384 194 256 268V416L384 342Z" fill="#5cc3d1" fill-opacity="0.65"/></g></svg>'
    )
  }

  // The agent: a small, friendly robot. Any element with data-agent gets it; mood sets how
  // happy it looks and where it looks, and it blinks on its own.
  const robot =
    '<svg viewBox="0 0 160 160"><path class="b-ant" d="M80 34V19"/><circle class="b-tip" cx="80" cy="14" r="7.5"/>' +
    '<rect class="b-ear" x="9" y="68" width="16" height="34" rx="8"/><rect class="b-ear" x="135" y="68" width="16" height="34" rx="8"/>' +
    '<rect class="b-head" x="20" y="32" width="120" height="106" rx="40"/><path class="b-shine" d="M46 47q16-7 34-6"/>' +
    '<rect class="b-face" x="35" y="53" width="90" height="68" rx="27"/><g class="b-look">' +
    '<g class="b-eyes"><rect x="55" y="72" width="14" height="22" rx="7"/><rect x="91" y="72" width="14" height="22" rx="7"/></g>' +
    '<path class="b-happy" d="M54 87q8-12 16 0M90 87q8-12 16 0"/><path class="b-smile" d="M71 103q9 7 18 0"/></g></svg>'
  document.querySelectorAll("[data-agent]").forEach((el, i) => {
    el.insertAdjacentHTML("afterbegin", robot)
    el._seed = i * 1.37
  })
  /** How happy an agent looks, from 0 to 1, and where it looks, from -1 (left) to 1. */
  function mood(el, lt, happy = 0, look = 0) {
    const b = (lt + el._seed) % 3.8
    const blink = b < 0.18 ? Math.sin((b / 0.18) * Math.PI) : 0
    set(el.querySelector(".b-eyes"), { o: 1 - happy, sy: 1 - Math.max(blink, happy) * 0.9 })
    set(el.querySelector(".b-happy"), { o: happy })
    set(el.querySelector(".b-smile"), { s: 1 + happy * 0.45 })
    set(el.querySelector(".b-look"), { x: look * 7 })
  }

  /** Fades a whole scene out over its last moments. */
  function exit(el, lt, len, d = 0.7) {
    const k = p(lt, len - d, len, ease.inCubic)
    set(el, { o: 1 - k, s: 1 + k * 0.03, blur: k * 10 })
  }
  /** A cursor's glide from one point to another, with a press. */
  function glide(el, lt, a, b, from, to, press, o = 1) {
    const k = p(lt, a, b, ease.inOutCubic)
    const down = press === undefined ? 0 : p(lt, press, press + 0.08) * (1 - p(lt, press + 0.12, press + 0.3))
    set(el, { x: lerp(from[0], to[0], k), y: lerp(from[1], to[1], k), s: 1 - down * 0.18, o })
  }
  /** A click's ripple on the centre of target, or its point fx, fy. */
  function ripple(el, lt, at, target, fx, fy) {
    const k = p(lt, at, at + 0.6, ease.outCubic)
    const { x, y } = M.centre(target, fx, fy)
    el.style.left = `${x}px`
    el.style.top = `${y}px`
    set(el, { o: lt < at ? 0 : 1 - k, s: 0.3 + k * 1.2 })
  }
  /** A button pressed at at. */
  function press(el, lt, at) {
    const d = p(lt, at, at + 0.08) * (1 - p(lt, at + 0.1, at + 0.35))
    set(el, { s: 1 - d * 0.08 })
    el.style.filter = d > 0.01 ? `brightness(${1 + d * 0.4})` : "none"
  }
  /**
   * A probe that flies from one point to another from at on, and the ring where it lands.
   * Returns how bright the landing is now, for a wall or a tile to light up with.
   */
  function shoot(probe, hit, lt, at, from, to, dur = 0.55) {
    const k = p(lt, at, at + dur, ease.inCubic)
    probe.style.left = `${lerp(from[0], to[0], k)}px`
    probe.style.top = `${lerp(from[1], to[1], k)}px`
    set(probe, { o: lt >= at && lt < at + dur + 0.05 ? 1 : 0 })
    const hk = p(lt, at + dur, at + dur + 0.65, ease.outCubic)
    hit.style.left = `${to[0]}px`
    hit.style.top = `${to[1]}px`
    set(hit, { o: lt < at + dur ? 0 : 1 - hk, s: 0.3 + hk * 1.4 })
    return Math.max(0, 1 - Math.abs(lt - at - dur) * 3)
  }
  /** Something that pops in with a little overshoot at a, and out at b if given. */
  function pop(el, lt, a, b) {
    const k = p(lt, a, a + 0.5, ease.outBack)
    const out = b === undefined ? 0 : p(lt, b, b + 0.35, ease.inCubic)
    set(el, { o: p(lt, a, a + 0.15) * (1 - out), s: 0.7 + k * 0.3 - out * 0.1, y: (1 - k) * 16 })
  }
  Object.assign(M, { byId, mood, exit, glide, ripple, press, shoot, pop })
})()
