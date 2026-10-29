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

/**
 * A template's display name, such as node-22+python-3.13@2. Without one, "Template" and the
 * first 8 characters of its ID; "No template" for a sandbox without a template.
 */
export function templateLabel(
  templateId: string | undefined,
  name?: string
): string {
  if (name) return name
  return templateId ? `Template ${templateId.slice(0, 8)}` : "No template"
}

/** The template's name for an arrow; without one, the first 8 characters of its ID, or "none". */
export function shortTemplate(
  templateId: string | undefined,
  name?: string
): string {
  if (name) return name
  return templateId ? templateId.slice(0, 8) : "none"
}

/** The stack of a template's name: node-22+python-3.13 of node-22+python-3.13@2. */
export function templateStack(name?: string) {
  return name?.replace(/@\d+$/, "")
}

/** Whether a timestamp is set. The Go zero time (year 1) means it is not. */
export function isSet(time: string): boolean {
  return Date.parse(time) > 0
}

/** A point on a sandbox's line: one event, in order. */
type StepBase = { id: number; generation: number }

export type Step =
  | (StepBase & {
      kind: "origin"
      templateId: string
      templateName?: string
      parent?: LineageNode
    })
  | (StepBase & {
      kind: "rebase"
      fromTemplateId: string
      fromTemplateName?: string
      templateId: string
      templateName?: string
    })
  | (StepBase & {
      kind: "disks"
      templateId: string
      templateName?: string
    })
  | (StepBase & {
      kind: "restore"
      name: string
      templateId: string
      templateName?: string
    })
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
          templateName: event.templateName,
        })
        break
      case "fork":
        if (index === 0) {
          steps.push({
            ...base,
            kind: "origin",
            templateId: event.templateId ?? "",
            templateName: event.templateName,
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
          fromTemplateName: event.fromTemplateName,
          templateId: event.templateId ?? "",
          templateName: event.templateName,
        })
        break
      case "disks":
        steps.push({
          ...base,
          kind: "disks",
          templateId: event.templateId ?? "",
          templateName: event.templateName,
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
          templateName: event.templateName,
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
      step.kind === "disks" ||
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
        templateLabel(step.templateId, step.templateName),
      ].join(", ")
    case "rebase":
      return `Rebased from ${shortTemplate(step.fromTemplateId, step.fromTemplateName)} to ${shortTemplate(step.templateId, step.templateName)}, ${generation}`
    case "disks":
      return `Disks resized, ${generation}`
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
