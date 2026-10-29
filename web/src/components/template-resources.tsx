import { CpuIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import type { Template } from "@/lib/api/client"

const row = "flex items-center justify-between gap-3 px-3 py-2"

/** The CPU, memory and disk a template gives a sandbox. */
export function TemplateResources({
  resources,
}: {
  resources: Template["resources"]
}) {
  return (
    <section className="grid gap-2" aria-label="Template resources">
      <SectionHeader title="Template resources" icon={CpuIcon} />
      <dl className="divide-y rounded-md border">
        <div className={row}>
          <dt className="text-muted-foreground">CPU</dt>
          <dd>
            {resources.cpus} {plural(resources.cpus, "CPU")}
          </dd>
        </div>
        <div className={row}>
          <dt className="text-muted-foreground">Memory</dt>
          <dd>
            {formatMiB(resources.memoryMiB)}
            {resources.maxMemoryMiB !== resources.memoryMiB &&
              ` (up to ${formatMiB(resources.maxMemoryMiB)})`}
          </dd>
        </div>
        <div className={row}>
          <dt className="text-muted-foreground">Workspace disk</dt>
          <dd>{formatMiB(resources.workspaceMiB)}</dd>
        </div>
        <div className={row}>
          <dt className="text-muted-foreground">Docker disk</dt>
          <dd>{formatMiB(resources.dockerMiB)}</dd>
        </div>
        {/* Unnamed, a new sandbox's is 4 GiB and a rebased one keeps its own. */}
        {resources.homeMiB ? (
          <div className={row}>
            <dt className="text-muted-foreground">Home disk</dt>
            <dd>{formatMiB(resources.homeMiB)}</dd>
          </div>
        ) : null}
      </dl>
    </section>
  )
}

function plural(count: number, word: string) {
  return count === 1 ? word : `${word}s`
}

function formatMiB(mib: number) {
  return mib % 1024 === 0 ? `${mib / 1024} GiB` : `${mib} MiB`
}
