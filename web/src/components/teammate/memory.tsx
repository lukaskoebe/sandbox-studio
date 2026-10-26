import { MemoryPanel } from "@/components/memory-panel"

/** A teammate's own memory, in the profile's Memory sheet. */
export function TeammateMemory({ env, id }: { env: string; id: string }) {
  // The sheet's title and description already say whose memory it is and who sees it.
  return (
    <div className="max-w-4xl">
      <MemoryPanel env={env} scope={`teammate:${id}`} />
    </div>
  )
}
