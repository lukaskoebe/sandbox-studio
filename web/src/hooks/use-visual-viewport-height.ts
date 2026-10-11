import { useEffect, useState } from "react"

/** Keep fixed-height workspaces within the visible area when a mobile keyboard opens. */
export function useVisualViewportHeight(): number | undefined {
  const [height, setHeight] = useState<number>()

  useEffect(() => {
    const viewport = window.visualViewport
    const updateHeight = () => {
      // Pinch zoom should magnify the workspace, not reflow it to a smaller height.
      if (viewport && viewport.scale !== 1) return
      const nextHeight = viewport?.height ?? window.innerHeight
      if (nextHeight > 0) setHeight(Math.round(nextHeight))
    }

    updateHeight()
    viewport?.addEventListener("resize", updateHeight)
    window.addEventListener("resize", updateHeight)
    return () => {
      viewport?.removeEventListener("resize", updateHeight)
      window.removeEventListener("resize", updateHeight)
    }
  }, [])

  return height
}
