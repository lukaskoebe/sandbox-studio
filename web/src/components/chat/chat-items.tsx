import {
  ArrowCounterClockwiseIcon,
  ArrowsInSimpleIcon,
  CaretRightIcon,
  CompassIcon,
  FileIcon,
  ImageIcon,
  InfoIcon,
  StopCircleIcon,
  TerminalWindowIcon,
  WarningIcon,
  type Icon,
} from "@phosphor-icons/react"
import { useCallback } from "react"
import { Link } from "@tanstack/react-router"
import type { Prompt } from "@/components/chat/composer"
import {
  ChatPicture,
  chatPictureURL,
  isComputerPicture,
} from "@/components/chat/chat-picture"
import { Markdown, Pictures } from "@/components/chat/markdown"
import { MentionedText } from "@/components/chat/mention-picker"
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
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Marker, MarkerContent, MarkerIcon } from "@/components/ui/marker"
import { Message, MessageContent } from "@/components/ui/message"
import type { components } from "@/lib/api/schema.gen"
import type { ChatItem, ChatTurn } from "@/lib/chats"
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
      return <UserMessage item={item} env={ctx.env} />
    case "agent":
      return item.text ? (
        <Message>
          <MessageContent>
            <Bubble variant="ghost" className="w-full">
              <BubbleContent>
                <AgentText item={item} text={item.text.text} ctx={ctx} />
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

/** An agent's message, with the pictures it shows from its computer. */
function AgentText({
  item,
  text,
  ctx,
}: {
  item: ChatItem
  text: string
  ctx: ItemContext
}) {
  const { env, chatId, live } = ctx
  const pictures = useCallback(
    (src: string, alt: string) =>
      isComputerPicture(src) ? (
        <ChatPicture
          url={chatPictureURL(env, chatId, item.id, src)}
          src={src}
          alt={alt}
          writing={live}
        />
      ) : null,
    [env, chatId, item.id, live]
  )
  return (
    <Pictures.Provider value={pictures}>
      <Markdown text={text} />
    </Pictures.Provider>
  )
}

function UserMessage({ item, env }: { item: ChatItem; env: string }) {
  if (!item.user) return null
  const lead = item.user.lead
  if (!lead) return <UserBubble u={item.user} />
  return (
    <div className="grid justify-items-end gap-1">
      <UserBubble u={item.user} />
      <FromLead env={env} lead={lead} />
    </div>
  )
}

/**
 * Who sent a message Lead wrote: Lead, for the plan the user approved, with a link to the plan
 * (docs/lead.md#plans), or a follow-up it sent without asking, as its settings allow. The agent
 * is told the same before the message.
 */
function FromLead({
  env,
  lead,
}: {
  env: string
  lead: NonNullable<NonNullable<ChatItem["user"]>["lead"]>
}) {
  const at = new Date(lead.approvedAt).toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
  })
  return (
    <span className="flex items-center gap-1 text-meta text-muted-foreground">
      <CompassIcon className="size-3" aria-hidden />
      <span>
        {lead.auto
          ? `${lead.name}, follow-up without asking`
          : `${lead.name}, approved by you at ${at}`}{" "}
        ·{" "}
        <Link
          to="/e/$env/lead/plans/$plan"
          params={{ env, plan: lead.planId }}
          className="underline-offset-2 hover:text-foreground hover:underline"
        >
          {lead.plan || "the plan"}
        </Link>
      </span>
    </span>
  )
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
      <UserBubble
        u={{ text: prompt.text ?? "", attachments, mentions: prompt.mentions }}
      />
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
              <MentionedText text={u.text} mentions={u.mentions ?? undefined} />
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
  // How full the context is shows beside the composer instead (context-meter.tsx).
  return parts.join(" · ")
}
