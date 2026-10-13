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

function arrived(a: Approval) {
  return Date.parse(a.createdAt)
}

/**
 * The stack's cards for the approvals, in the order given. Network requests from one sandbox
 * form a group while each arrives within the window of the one before it, and the group takes
 * the place of its first request. Requests in `split` were chosen for individual review and
 * stay single cards.
 *
 * The server records when a request arrived to the second, so the window is approximate. A
 * request carries no page, so grouping is by sandbox and arrival time only; grouping by the
 * page that asked comes with site-scoped approvals.
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
  for (const a of arrivals) {
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
