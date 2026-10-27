import {
  ArrowCounterClockwiseIcon,
  ArrowsInSimpleIcon,
  CaretRightIcon,
  FileIcon,
  ImageIcon,
  InfoIcon,
  StopCircleIcon,
  TerminalWindowIcon,
  WarningIcon,
  type Icon,
} from "@phosphor-icons/react"
import type { Prompt } from "@/components/chat/composer"
import { Markdown } from "@/components/chat/markdown"
import { PermissionCard } from "@/components/chat/permission-card"
import { PlanCard } from "@/components/chat/plan-card"
import { QuestionCard } from "@/components/chat/question-card"
import { ToolCallCard } from "@/components/chat/tool-call-card"
import {
  Attachment,
  AttachmentContent,
  AttachmentDescription,
  AttachmentGroup,
  AttachmentMedia,
  AttachmentTitle,
} from "@/components/ui/attachment"
import { Badge } from "@/components/ui/badge"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Marker, MarkerContent, MarkerIcon } from "@/components/ui/marker"
import { Message, MessageContent } from "@/components/ui/message"
import { Spinner } from "@/components/ui/spinner"
import type { components } from "@/lib/api/schema.gen"
import {
  chatStateLabels,
  type Chat,
  type ChatItem,
  type ChatTurn,
} from "@/lib/chats"
import { cn, formatBytes } from "@/lib/utils"

type MarkerKind = components["schemas"]["MarkerBody"]["kind"]

const markers: Record<MarkerKind, { icon: Icon; text: string }> = {
  stopped: { icon: StopCircleIcon, text: "Stopped" },
  interrupted: { icon: WarningIcon, text: "Interrupted" },
  error: { icon: WarningIcon, text: "Something went wrong" },
  limit: { icon: WarningIcon, text: "Hit a limit" },
  notice: { icon: InfoIcon, text: "Notice" },
  compaction: { icon: ArrowsInSimpleIcon, text: "Context compacted" },
  terminal: { icon: TerminalWindowIcon, text: "Continued in the terminal" },
  undo: { icon: ArrowCounterClockwiseIcon, text: "Changes undone" },
}

export type ItemContext = {
  env: string
  chatId: string
  teammateName: string
  /** Whether this item is still being written: the last one of a running turn. */
  live: boolean
}

/** One chat item, rendered by its kind (docs/design/chat-mode.md §2). */
export function ChatItemView({
  item,
  ctx,
}: {
  item: ChatItem
  ctx: ItemContext
}) {
  switch (item.kind) {
    case "user":
      return <UserMessage item={item} />
    case "agent":
      return item.text ? (
        <Message>
          <MessageContent>
            <Bubble variant="ghost" className="w-full">
              <BubbleContent>
                <Markdown text={item.text.text} />
                {item.text.truncated && <Truncated />}
              </BubbleContent>
            </Bubble>
          </MessageContent>
        </Message>
      ) : null
    case "thought":
      return item.text ? (
        <Thought text={item.text.text} live={ctx.live} />
      ) : null
    case "tool":
      return item.tool ? <ToolCallCard tool={item.tool} /> : null
    case "plan":
      return item.plan ? (
        <PlanCard entries={item.plan.entries ?? []} live={ctx.live} />
      ) : null
    case "permission":
      return (
        <PermissionCard
          env={ctx.env}
          item={item}
          teammateName={ctx.teammateName}
        />
      )
    case "question":
      return (
        <QuestionCard
          env={ctx.env}
          chatId={ctx.chatId}
          item={item}
          teammateName={ctx.teammateName}
        />
      )
    case "marker": {
      const m = item.marker
      if (!m) return null
      const kind = markers[m.kind]
      return (
        <Marker
          variant="separator"
          className={cn(
            (m.kind === "error" || m.kind === "interrupted") &&
              "text-destructive"
          )}
        >
          <MarkerIcon>
            <kind.icon />
          </MarkerIcon>
          <MarkerContent>{m.text || kind.text}</MarkerContent>
        </Marker>
      )
    }
    default:
      return null
  }
}

function UserMessage({ item }: { item: ChatItem }) {
  return item.user ? <UserBubble u={item.user} /> : null
}

/**
 * A message the user just sent, shown until the chat's log has it, so it leaves the composer
 * at once even while the agent takes a moment to start.
 */
export function SendingMessage({ prompt }: { prompt: Prompt }) {
  const attachments = (prompt.attachments ?? []).map((a) => ({
    name: a.name,
    mimeType: a.mimeType ?? "",
    size: Math.floor((a.data.length * 3) / 4), // base64
  }))
  return (
    <div className="grid justify-items-end gap-1">
      <UserBubble u={{ text: prompt.text ?? "", attachments }} />
      <span className="shimmer text-meta text-muted-foreground">Sending…</span>
    </div>
  )
}

function UserBubble({ u }: { u: NonNullable<ChatItem["user"]> }) {
  return (
    <Message align="end">
      <MessageContent>
        {u.attachments && u.attachments.length > 0 && (
          <AttachmentGroup className="justify-end">
            {u.attachments.map((a, i) => (
              <Attachment key={i} size="sm">
                <AttachmentMedia>
                  {a.mimeType.startsWith("image/") ? (
                    <ImageIcon />
                  ) : (
                    <FileIcon />
                  )}
                </AttachmentMedia>
                <AttachmentContent>
                  <AttachmentTitle>{a.name}</AttachmentTitle>
                  <AttachmentDescription>
                    {formatBytes(a.size)}
                  </AttachmentDescription>
                </AttachmentContent>
              </Attachment>
            ))}
          </AttachmentGroup>
        )}
        {u.text && (
          <Bubble align="end" variant="secondary">
            <BubbleContent className="whitespace-pre-wrap">
              {u.text}
            </BubbleContent>
          </Bubble>
        )}
      </MessageContent>
    </Message>
  )
}

function Thought({ text, live }: { text: string; live: boolean }) {
  return (
    <Collapsible className="group/thought min-w-0 text-xs text-muted-foreground">
      <CollapsibleTrigger className="flex items-center gap-1 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30">
        <CaretRightIcon className="size-3 transition-transform group-data-[open]/thought:rotate-90" />
        <span className={cn(live && "shimmer")}>
          {live ? "Thinking…" : "Thought it through"}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="mt-1.5 border-l-2 pl-3">
        <Markdown text={text} />
      </CollapsibleContent>
    </Collapsible>
  )
}

function Truncated() {
  return (
    <p className="mt-1 text-muted-foreground">
      The rest of this message was too long to keep.
    </p>
  )
}

/** Usage the agent reported for a turn, as one muted line; empty if it reported none. */
export function turnUsage(t: ChatTurn): string {
  const u = t.usage
  if (!u) return ""
  const parts: string[] = []
  const tokens = u.tokens as
    | { totalTokens?: number; inputTokens?: number; outputTokens?: number }
    | undefined
  const total =
    tokens?.totalTokens ??
    (tokens?.inputTokens !== undefined || tokens?.outputTokens !== undefined
      ? (tokens.inputTokens ?? 0) + (tokens.outputTokens ?? 0)
      : undefined)
  if (total !== undefined) parts.push(`${total.toLocaleString()} tokens`)
  const cost = u.cost as { amount?: number; currency?: string } | undefined
  if (typeof cost?.amount === "number") {
    parts.push(
      `${cost.amount.toLocaleString(undefined, {
        style: "currency",
        currency: cost.currency || "USD",
        maximumFractionDigits: 4,
      })} so far`
    )
  }
  if (u.contextUsed && u.contextSize)
    parts.push(
      `${Math.round((u.contextUsed / u.contextSize) * 100)}% of memory used`
    )
  return parts.join(" · ")
}

/** What the chat is doing, as a badge. */
export function ChatStateBadge({ chat }: { chat: Chat }) {
  return (
    <Badge variant={chat.state === "running" ? "secondary" : "outline"}>
      {chat.state === "running" && <Spinner className="size-2.5" />}
      {chatStateLabels[chat.state]}
    </Badge>
  )
}
