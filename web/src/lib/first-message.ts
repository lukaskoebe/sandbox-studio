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

export function handFirstMessage(chatId: string, first: FirstMessage) {
  waiting.set(chatId, first)
}

/** The thread's first message, once: a second call gets nothing. */
export function takeFirstMessage(chatId: string): FirstMessage | undefined {
  const first = waiting.get(chatId)
  waiting.delete(chatId)
  return first
}
