import { useEffect, useState } from "react"

/**
 * Whether key has stayed the same for the last ms milliseconds; true from the first render. A
 * list keyed by what it shows uses it to ignore clicks for a moment after it changed, so a
 * click meant for the row that just left doesn't land on the one that took its place.
 */
export function useSettled(key: string, ms = 500): boolean {
  const [settled, setSettled] = useState(key)
  useEffect(() => {
    const t = setTimeout(() => setSettled(key), ms)
    return () => clearTimeout(t)
  }, [key, ms])
  return settled === key
}
