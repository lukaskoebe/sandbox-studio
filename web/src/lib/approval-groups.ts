import type { Approval } from "@/lib/api/client"
import { isNetworkApproval } from "@/lib/approval-kinds"

/** Network requests from one sandbox that arrive this close together share a card. */
export const groupWindowSeconds = 3

/** A card of the approval stack: one approval, or the network requests of one sandbox that came together. */
export type ApprovalCard =
  | { kind: "single"; approval: Approval }
  | { kind: "group"; approvals: Approval[] }

/** The approval a card is about: its only approval, or the first of a group. */
export function cardApproval(card: ApprovalCard): Approval {
  return card.kind === "single" ? card.approval : card.approvals[0]
}

/**
 * The top-level site a group's requests belong to, when they are a browser page's requests:
 * the group then reads "<site> wants N hosts". Undefined for a sandbox's own group.
 */
export function groupSite(approvals: Approval[]): string | undefined {
  return approvals[0].network?.site
}

function arrived(a: Approval) {
  return Date.parse(a.createdAt)
}

/**
 * The stack's cards for the approvals, in the order given. A browser's requests group by the
 * page they belong to: one sandbox's pending requests to one top-level site are one card. Other
 * network requests from one sandbox form a group while each arrives within the window of the
 * one before it. A group takes the place of its first request. Requests in `split` were chosen
 * for individual review and stay single cards, and so does a navigation, which needs its own
 * decision (Allow site).
 *
 * The server records when a request arrived to the second, so the window is approximate.
 */
export function approvalCards(
  list: Approval[],
  split: ReadonlySet<string> = new Set()
): ApprovalCard[] {
  const arrivals = list
    .filter((a) => isNetworkApproval(a) && !split.has(a.id))
    .sort((a, b) => arrived(a) - arrived(b) || a.id.localeCompare(b.id))
  const members = new Map<string, Approval[]>() // by approval ID: the list its group is in
  const open = new Map<string, { last: number; list: Approval[] }>()
  const pages = new Map<string, Approval[]>()
  for (const a of arrivals) {
    const network = a.network
    if (network?.navigation) continue
    if (network?.site) {
      const key = `${a.environmentId}/${a.sandboxId ?? ""}/${network.site}`
      let page = pages.get(key)
      if (!page) {
        page = []
        pages.set(key, page)
      }
      page.push(a)
      members.set(a.id, page)
      continue
    }
    const key = `${a.environmentId}/${a.sandboxId ?? ""}`
    let group = open.get(key)
    if (!group || arrived(a) - group.last > groupWindowSeconds * 1000) {
      group = { last: arrived(a), list: [] }
      open.set(key, group)
    }
    group.last = arrived(a)
    group.list.push(a)
    members.set(a.id, group.list)
  }

  const cards: ApprovalCard[] = []
  const emitted = new Set<Approval[]>()
  for (const a of list) {
    const group = members.get(a.id)
    if (!group || group.length < 2) {
      cards.push({ kind: "single", approval: a })
    } else if (!emitted.has(group)) {
      emitted.add(group)
      cards.push({ kind: "group", approvals: group })
    }
  }
  return cards
}
