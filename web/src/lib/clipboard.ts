/**
 * Write text to the system clipboard, including on LAN HTTP pages where the
 * async Clipboard API is unavailable.
 */
export async function copyText(text: string): Promise<void> {
  if (typeof navigator !== "undefined") {
    try {
      const clipboard = (navigator as Partial<Navigator>).clipboard
      if (typeof clipboard?.writeText === "function") {
        await clipboard.writeText(text)
        return
      }
    } catch {
      // The Clipboard API can be missing, denied, or unavailable outside a
      // secure context. Try the synchronous browser fallback below.
    }
  }

  copyTextWithExecCommand(text)
}

function copyTextWithExecCommand(text: string): void {
  if (typeof document === "undefined") {
    throw new Error("Clipboard access is unavailable")
  }

  const activeElement = document.activeElement
  const inputSelection = getInputSelection(activeElement)
  const selection = document.getSelection()
  const selectionSnapshot = selection
    ? {
        anchorNode: selection.anchorNode,
        anchorOffset: selection.anchorOffset,
        focusNode: selection.focusNode,
        focusOffset: selection.focusOffset,
        ranges: Array.from({ length: selection.rangeCount }, (_, index) =>
          selection.getRangeAt(index).cloneRange()
        ),
      }
    : null
  const scrollPositions = captureScrollPositions(
    activeElement,
    selectionSnapshot?.anchorNode ?? null,
    selectionSnapshot?.focusNode ?? null
  )
  const windowScroll = { x: window.scrollX, y: window.scrollY }

  const textarea = document.createElement("textarea")
  textarea.value = text
  textarea.readOnly = true
  textarea.tabIndex = -1
  textarea.setAttribute("aria-hidden", "true")
  textarea.style.position = "fixed"
  textarea.style.top = "0"
  textarea.style.left = "-9999px"
  textarea.style.width = "1px"
  textarea.style.height = "1px"
  textarea.style.padding = "0"
  textarea.style.border = "0"
  textarea.style.opacity = "0"
  textarea.style.fontSize = "16px"

  let copied = false
  try {
    document.body.appendChild(textarea)
    textarea.focus({ preventScroll: true })
    textarea.setSelectionRange(0, textarea.value.length)
    copied = document.execCommand("copy")
  } finally {
    textarea.remove()
    try {
      restoreSelection(selectionSnapshot)
    } finally {
      try {
        // DOM selection restoration can reset an input's own selection in Chrome.
        // Restore its focus and range last so typing resumes at the original caret.
        restoreFocus(activeElement, inputSelection)
      } finally {
        restoreScrollPositions(scrollPositions, windowScroll)
      }
    }
  }

  if (!copied) throw new Error("Clipboard access is unavailable")
}

type InputSelection = {
  element: HTMLInputElement | HTMLTextAreaElement
  start: number | null
  end: number | null
  direction: "forward" | "backward" | "none" | null
}

function getInputSelection(element: Element | null): InputSelection | null {
  if (
    !(element instanceof HTMLInputElement) &&
    !(element instanceof HTMLTextAreaElement)
  ) {
    return null
  }

  try {
    return {
      element,
      start: element.selectionStart,
      end: element.selectionEnd,
      direction: element.selectionDirection,
    }
  } catch {
    // Some input types do not support text selection.
    return null
  }
}

function captureScrollPositions(
  activeElement: Element | null,
  anchorNode: Node | null,
  focusNode: Node | null
): Array<{ element: Element; left: number; top: number }> {
  const elements = new Set<Element>()
  const addAncestors = (node: Node | null) => {
    let element = node instanceof Element ? node : node?.parentElement
    while (element) {
      elements.add(element)
      element = element.parentElement
    }
  }

  addAncestors(activeElement)
  addAncestors(anchorNode)
  addAncestors(focusNode)
  elements.add(document.documentElement)
  elements.add(document.body)

  return Array.from(elements, (element) => ({
    element,
    left: element.scrollLeft,
    top: element.scrollTop,
  }))
}

function restoreFocus(
  activeElement: Element | null,
  inputSelection: InputSelection | null
): void {
  if (!(activeElement instanceof HTMLElement) || !activeElement.isConnected) {
    return
  }

  activeElement.focus({ preventScroll: true })
  if (
    inputSelection &&
    inputSelection.element === activeElement &&
    inputSelection.start !== null &&
    inputSelection.end !== null
  ) {
    try {
      inputSelection.element.setSelectionRange(
        inputSelection.start,
        inputSelection.end,
        inputSelection.direction ?? undefined
      )
    } catch {
      // Ignore selection restoration failures for unsupported input types.
    }
  }
}

function restoreSelection(
  snapshot: {
    anchorNode: Node | null
    anchorOffset: number
    focusNode: Node | null
    focusOffset: number
    ranges: Range[]
  } | null
): void {
  const selection = document.getSelection()
  if (!selection || !snapshot) return

  selection.removeAllRanges()
  const { anchorNode, anchorOffset, focusNode, focusOffset, ranges } = snapshot
  if (
    anchorNode?.isConnected &&
    focusNode?.isConnected &&
    typeof selection.setBaseAndExtent === "function"
  ) {
    selection.setBaseAndExtent(anchorNode, anchorOffset, focusNode, focusOffset)
    return
  }

  for (const range of ranges) {
    if (range.startContainer.isConnected && range.endContainer.isConnected) {
      selection.addRange(range)
    }
  }
}

function restoreScrollPositions(
  positions: Array<{ element: Element; left: number; top: number }>,
  windowScroll: { x: number; y: number }
): void {
  for (const { element, left, top } of positions) {
    element.scrollLeft = left
    element.scrollTop = top
  }
  try {
    window.scrollTo(windowScroll.x, windowScroll.y)
  } catch {
    // Retain the element scroll restoration if window scrolling is unavailable.
  }
}
