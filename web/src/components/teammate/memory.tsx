import { MemoryTabs } from "@/components/memory-tabs"
import { MemoryUsage } from "@/components/memory-sessions"
import { $api } from "@/lib/api/client"

const explanation =
  "What this teammate remembers: only this teammate sees its own memory, and your edits here outrank anything inferred."

/** A teammate's own memory, in the profile's Memory sheet. */
export function TeammateMemory({ env, id }: { env: string; id: string }) {
  const scope = `teammate:${id}`
  const scopes = $api.useQuery("get", "/api/environments/{env}/memory/scopes", {
    params: { path: { env } },
  })
  const current = scopes.data?.find((s) => s.scope === scope)

  return (
    <div className="max-w-5xl space-y-4">
      <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
        {explanation}{" "}
        <a
          href="https://lukaskoebe.github.io/sandbox-studio/memory/"
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-4"
        >
          Learn more
        </a>
      </p>
      {current && (
        <p className="text-xs text-muted-foreground">
          {current.facts} facts, {current.pages} pages; core pages use{" "}
          {current.coreChars} of {current.coreBudget} characters.
        </p>
      )}
      <MemoryUsage env={env} shared={false} />
      <MemoryTabs env={env} scope={scope} />
    </div>
  )
}
