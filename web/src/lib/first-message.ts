import type { Prompt } from "@/components/chat/composer"

/**
 * A new thread's first message, handed from where it was written to the thread, which opens at
 * once and sends it the way it sends any other: it shows while the agent starts.
 */
export type FirstMessage = {
  prompt: Prompt
  /** Resolves once the computer, started as the message was typed, is up. */
  ready?: Promise<void>
}

const waiting = new Map<string, FirstMessage>()

// Its words also wait in the tab's sessionStorage until they went, so that a reload while the
// computer starts, or a send that failed once the thread was left, doesn't lose them.
const keptKey = (chatId: string) => `studio.first.${chatId}`

export function handFirstMessage(chatId: string, first: FirstMessage) {
  waiting.set(chatId, first)
  if (!first.prompt.text) return
  try {
    sessionStorage.setItem(keptKey(chatId), first.prompt.text)
  } catch {
    // Without storage only a reload loses them.
  }
}

/** The thread's first message, once: a second call gets nothing. */
export function takeFirstMessage(chatId: string): FirstMessage | undefined {
  const first = waiting.get(chatId)
  waiting.delete(chatId)
  return first
}

/** Forgets the first message's words: they went, or the composer holds them now. */
export function firstMessageDone(chatId: string) {
  try {
    sessionStorage.removeItem(keptKey(chatId))
  } catch {
    // Nothing kept.
  }
}

/** The words of a first message that never went, once, for the composer to send again. */
export function lostFirstMessage(chatId: string): string | undefined {
  let text: string | null = null
  try {
    text = sessionStorage.getItem(keptKey(chatId))
  } catch {
    return undefined
  }
  firstMessageDone(chatId)
  return text ?? undefined
}
