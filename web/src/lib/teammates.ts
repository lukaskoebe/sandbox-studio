import type { Harness, ProviderKind } from "@/lib/api/client"

/** Provider kinds in the order the provider dialog offers them. */
export const providerKinds: ProviderKind[] = [
  "anthropic_api",
  "openai_api",
  "openai_compatible",
  "claude_subscription",
  "chatgpt_subscription",
]

export const kindLabels: Record<ProviderKind, string> = {
  anthropic_api: "Anthropic API key",
  openai_api: "OpenAI API key",
  openai_compatible: "OpenAI-compatible endpoint",
  claude_subscription: "Claude subscription",
  chatgpt_subscription: "ChatGPT subscription",
}

export const harnessLabels: Record<Harness, string> = {
  opencode: "OpenCode",
  claude: "Claude Code",
  codex: "Codex",
}

/** Whether a kind is a subscription login rather than an API key. */
export function isSubscription(kind: ProviderKind) {
  return kind === "claude_subscription" || kind === "chatgpt_subscription"
}
