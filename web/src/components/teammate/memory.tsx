import { MemoryPanel } from "@/components/memory-panel"

/** A teammate's own memory, in the profile's Memory sheet. */
export function TeammateMemory({
  env,
  id,
  open,
  onOpen,
}: {
  env: string
  id: string
  /** The open page or group, in the URL as ?open=. */
  open?: string
  onOpen: (open: string | undefined) => void
}) {
  // The sheet's title and description already say whose memory it is and who sees it.
  return (
    <MemoryPanel
      env={env}
      scope={`teammate:${id}`}
      open={open}
      onOpen={onOpen}
    />
  )
}
