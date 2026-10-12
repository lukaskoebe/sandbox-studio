import { Link } from "@tanstack/react-router"
import { phaseDot, phaseOf } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { isSet, templateLabel } from "@/lib/lineage"
import { cn, formatAge } from "@/lib/utils"

type TreeNode = components["schemas"]["LineageTreeNode"]

/**
 * The environment's family tree: sandboxes grouped by template, each fork under the sandbox it
 * was made from. Deleted sandboxes stay, muted, so a family keeps its history.
 */
export function LineageTree({
  environmentId,
  compact = false,
}: {
  environmentId: string
  compact?: boolean
}) {
  const tree = $api.useQuery("get", "/api/environments/{env}/lineage", {
    params: { path: { env: environmentId } },
  })

  if (!tree.data) {
    return tree.isError ? (
      <p className="text-xs text-destructive">{errorMessage(tree.error)}</p>
    ) : (
      <div className="grid min-h-24 place-items-center">
        <Spinner />
      </div>
    )
  }
  const groups = (tree.data.templates ?? []).filter(
    (template) => (template.sandboxes ?? []).length > 0
  )
  if (groups.length === 0) {
    return <p className="text-xs text-muted-foreground">No sandboxes yet</p>
  }

  const list = (
    <div className="grid gap-3">
      {groups.map((template) => (
        <div
          key={template.id}
          role="group"
          aria-label={template.name || templateLabel(template.id)}
        >
          <p className="font-mono text-[0.6875rem] text-muted-foreground">
            {template.name || templateLabel(template.id)}
            {isSet(template.createdAt) && ` · ${formatAge(template.createdAt)}`}
          </p>
          <Forest
            environmentId={environmentId}
            nodes={template.sandboxes ?? []}
          />
        </div>
      ))}
    </div>
  )
  return compact ? (
    <ScrollArea viewportClassName="max-h-72">{list}</ScrollArea>
  ) : (
    list
  )
}

/** Sandboxes, each followed by the forks made from it, indented under it. */
function Forest({
  environmentId,
  nodes,
  nested = false,
}: {
  environmentId: string
  nodes: TreeNode[]
  nested?: boolean
}) {
  return (
    <ul className={cn("mt-1 grid gap-0.5", nested && "ml-2 border-l pl-2")}>
      {nodes.map((node) => (
        <li key={node.id}>
          <TreeRow environmentId={environmentId} node={node} />
          {(node.forks ?? []).length > 0 && (
            <Forest
              environmentId={environmentId}
              nodes={node.forks ?? []}
              nested
            />
          )}
        </li>
      ))}
    </ul>
  )
}

/** A sandbox's dot, name and status. A deleted one is muted and not a link. */
function TreeRow({
  environmentId,
  node,
}: {
  environmentId: string
  node: TreeNode
}) {
  const content = (
    <>
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-mono",
          node.deleted && "line-through"
        )}
      >
        {node.name}
      </span>
      {node.deleted ? (
        <span className="shrink-0 text-muted-foreground">deleted</span>
      ) : (
        <StatusDot
          {...phaseDot(phaseOf(node.status, true))}
          className="shrink-0 font-sans text-[0.6875rem]"
        />
      )}
    </>
  )
  if (node.deleted) {
    return (
      <div className="flex items-center gap-2 px-2 py-1.5 text-muted-foreground">
        {content}
      </div>
    )
  }
  return (
    <Link
      to="/e/$env/sandboxes/$id"
      params={{ env: environmentId, id: node.id }}
      className="flex items-center gap-2 rounded-[4px] px-2 py-1.5 transition-colors hover:bg-muted"
    >
      {content}
    </Link>
  )
}
