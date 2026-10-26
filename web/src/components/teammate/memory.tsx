import { MemoryTabs } from "@/components/memory-tabs"
import { MemoryUsage } from "@/components/memory-sessions"
import { $api } from "@/lib/api/client"

/** A teammate's own memory, in the profile's Memory sheet. */
export function TeammateMemory({ env, id }: { env: string; id: string }) {
  const scope = `teammate:${id}`
  const scopes = $api.useQuery("get", "/api/environments/{env}/memory/scopes", {
    params: { path: { env } },
  })
  const current = scopes.data?.find((s) => s.scope === scope)

  return (
    <div className="max-w-5xl space-y-4">
      {/* The sheet's description already says who sees the memory. */}
      <p className="text-xs text-muted-foreground">
        {current && (
          <>
            {current.facts} facts, {current.pages} pages; core pages use{" "}
            {current.coreChars} of {current.coreBudget} characters.{" "}
          </>
        )}
        <a
          href="https://lukaskoebe.github.io/sandbox-studio/memory/"
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-4"
        >
          How memory works
        </a>
      </p>
      <MemoryUsage env={env} shared={false} />
      <MemoryTabs env={env} scope={scope} />
    </div>
  )
}
