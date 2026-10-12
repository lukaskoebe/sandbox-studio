import type { components } from "@/lib/api/schema.gen"

export type Lineage = components["schemas"]["Lineage"]
export type LineageNode = components["schemas"]["LineageNode"]
export type LineageCheckpoint = components["schemas"]["LineageCheckpoint"]

/** A status's colour: running green, waiting amber, failed red, anything else neutral. */
export type Tone = "green" | "amber" | "red" | "neutral"

export function statusTone(status: string): Tone {
  switch (status) {
    case "running":
      return "green"
    case "waiting":
      return "amber"
    case "failed":
      return "red"
    default:
      return "neutral"
  }
}

/** The fill of a status marker, per tone: the status colors of the shared StatusDot. */
export const toneDot: Record<Tone, string> = {
  green: "bg-(--st-running)",
  amber: "bg-(--st-waiting)",
  red: "bg-(--st-failed)",
  neutral: "bg-(--st-stopped)",
}

/** "Template 1a2b3c4d", or "No template" for a sandbox without one. */
export function templateLabel(templateId: string | undefined): string {
  return templateId ? `Template ${templateId.slice(0, 8)}` : "No template"
}

/** The first 8 characters of a template, for an arrow; "none" without one. */
export function shortTemplate(templateId: string | undefined): string {
  return templateId ? templateId.slice(0, 8) : "none"
}

/** Whether a timestamp is set. The Go zero time (year 1) means it is not. */
export function isSet(time: string): boolean {
  return Date.parse(time) > 0
}

/** A point on a sandbox's line: one event, in order. */
type StepBase = { id: number; generation: number }

export type Step =
  | (StepBase & { kind: "origin"; templateId: string; parent?: LineageNode })
  | (StepBase & {
      kind: "rebase"
      fromTemplateId: string
      templateId: string
    })
  | (StepBase & { kind: "restore"; name: string; templateId: string })
  | (StepBase & {
      kind: "checkpoint"
      name: string
      checkpoint?: LineageCheckpoint
    })
  | (StepBase & { kind: "fork"; child?: LineageNode })

/** A generation's stretch of the line, from the step that starts it. */
export type Segment = { generation: number; templateId: string; start: number }

export type Timeline = { steps: Step[]; segments: Segment[] }

/**
 * The points of a sandbox's history, oldest first. The first create or fork opens the
 * sandbox; a later fork branches off to its child. A delete is only a tombstone's event, so
 * it is left out.
 */
export function buildTimeline(lineage: Lineage): Timeline {
  const checkpoints = lineage.checkpoints ?? []
  const children = lineage.children ?? []
  const steps: Step[] = []
  for (const [index, event] of (lineage.events ?? []).entries()) {
    const base = { id: event.id, generation: event.generation }
    switch (event.kind) {
      case "create":
        steps.push({
          ...base,
          kind: "origin",
          templateId: event.templateId ?? "",
        })
        break
      case "fork":
        if (index === 0) {
          steps.push({
            ...base,
            kind: "origin",
            templateId: event.templateId ?? "",
            parent: lineage.parent,
          })
        } else {
          const child = children.find(
            (node) => node.id === event.otherSandboxId
          )
          steps.push({ ...base, kind: "fork", child })
        }
        break
      case "rebase":
        steps.push({
          ...base,
          kind: "rebase",
          fromTemplateId: event.fromTemplateId ?? "",
          templateId: event.templateId ?? "",
        })
        break
      case "restore": {
        const checkpoint = checkpoints.find(
          (item) => item.id === event.checkpointId
        )
        steps.push({
          ...base,
          kind: "restore",
          name: checkpoint?.name ?? event.checkpointName ?? "",
          templateId: event.templateId ?? "",
        })
        break
      }
      case "checkpoint": {
        // A renamed checkpoint shows its current name; a deleted one keeps the name it was saved with.
        const checkpoint = checkpoints.find(
          (item) => item.id === event.checkpointId
        )
        steps.push({
          ...base,
          kind: "checkpoint",
          name: checkpoint?.name ?? event.checkpointName ?? "",
          checkpoint,
        })
        break
      }
      case "delete":
        break
    }
  }

  const segments: Segment[] = []
  steps.forEach((step, start) => {
    if (
      step.kind === "origin" ||
      step.kind === "rebase" ||
      step.kind === "restore"
    ) {
      segments.push({
        generation: step.generation,
        templateId: step.templateId,
        start,
      })
    }
  })
  return { steps, segments }
}

/** What a step says in words: its tooltip, and its line in the text fallback. */
export function summarize(step: Step): string {
  const generation = `generation ${step.generation}`
  switch (step.kind) {
    case "origin":
      return [
        step.parent ? `Forked from ${step.parent.name}` : "Created",
        generation,
        templateLabel(step.templateId),
      ].join(", ")
    case "rebase":
      return `Rebased from ${shortTemplate(step.fromTemplateId)} to ${shortTemplate(step.templateId)}, ${generation}`
    case "restore":
      return `Restored checkpoint ${step.name}, ${generation}`
    case "checkpoint":
      return `Checkpoint ${step.name}, ${generation}${step.checkpoint ? "" : ", deleted"}`
    case "fork":
      return step.child
        ? `Forked to ${step.child.name}, ${step.child.deleted ? "deleted" : step.child.status}`
        : "Forked to a sandbox that is no longer listed"
  }
}
