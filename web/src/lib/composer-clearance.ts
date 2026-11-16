import { useEffect, useLayoutEffect, useState, type RefObject } from "react"

/**
 * The page's composers, so that something fixed to the window's foot, the approval stack, sits
 * above one rather than over its Send button. A composer reports itself while it is mounted,
 * and again whenever it changes size.
 */
const composers = new Set<HTMLElement>()
const listeners = new Set<() => void>()
const changed = () => listeners.forEach((l) => l())

export function useComposerSpot(ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    const el = ref.current
    if (!el) return
    composers.add(el)
    const sizes = new ResizeObserver(changed)
    sizes.observe(el)
    changed()
    return () => {
      sizes.disconnect()
      composers.delete(el)
      changed()
    }
  }, [ref])
}

/**
 * How far above the window's foot `el`, fixed to it `inset` px up, has to sit to clear the
 * composers it would rest on, with `gap` px between: `inset` when none is under it.
 */
export function useComposerClearance(
  el: HTMLElement | null,
  inset: number,
  gap = 8
) {
  const [bottom, setBottom] = useState(inset)
  useLayoutEffect(() => {
    if (!el) return
    const measure = () => {
      const own = el.getBoundingClientRect()
      // Where it rests unlifted; its height and sides don't change with the lift.
      const foot = window.innerHeight - inset
      const head = foot - own.height
      let top = Infinity
      for (const c of composers) {
        const r = c.getBoundingClientRect()
        if (r.width === 0 || r.height === 0) continue
        if (r.right <= own.left || r.left >= own.right) continue
        if (r.bottom <= head || r.top >= foot) continue
        top = Math.min(top, r.top)
      }
      setBottom(
        top === Infinity
          ? inset
          : Math.max(inset, Math.ceil(window.innerHeight - top + gap))
      )
    }
    measure()
    listeners.add(measure)
    const sizes = new ResizeObserver(measure)
    sizes.observe(el)
    window.addEventListener("resize", measure)
    return () => {
      listeners.delete(measure)
      sizes.disconnect()
      window.removeEventListener("resize", measure)
    }
  }, [el, inset, gap])
  return bottom
}
