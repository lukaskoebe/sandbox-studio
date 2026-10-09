import type { Template } from "@/lib/api/client"

/** The CPU, memory and disk a template gives a sandbox. */
export function TemplateResources({
  resources,
}: {
  resources: Template["resources"]
}) {
  return (
    <section className="rounded-md border p-3" aria-label="Template resources">
      <h3 className="mb-2 font-medium">Template resources</h3>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2">
        <div>
          <dt className="text-muted-foreground">CPU</dt>
          <dd>
            {resources.cpus} {plural(resources.cpus, "CPU")}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Memory</dt>
          <dd>
            {formatMiB(resources.memoryMiB)}
            {resources.maxMemoryMiB !== resources.memoryMiB &&
              ` (up to ${formatMiB(resources.maxMemoryMiB)})`}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Workspace disk</dt>
          <dd>{formatMiB(resources.workspaceMiB)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Docker disk</dt>
          <dd>{formatMiB(resources.dockerMiB)}</dd>
        </div>
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
