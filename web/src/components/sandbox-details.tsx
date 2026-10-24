import { CheckpointsSection } from "@/components/checkpoints-section"
import { LineageTimeline } from "@/components/lineage-timeline"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { ScrollArea } from "@/components/ui/scroll-area"
import { type Sandbox } from "@/lib/api/client"

export function formatMiB(mib: number) {
  return mib >= 1024 ? `${+(mib / 1024).toFixed(1)} GiB` : `${mib} MiB`
}

/** Where the sandbox came from and the checkpoints it can go back to, in a sheet. */
export function HistorySheet({
  env,
  sandbox: sb,
  open,
  onOpenChange,
}: {
  env: string
  sandbox: Sandbox
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const building = sb.status === "creating" || sb.status === "failed"
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>History and checkpoints</SheetTitle>
          <SheetDescription>
            Where <span className="font-mono">{sb.name}</span> came from, and
            the points it can go back to.
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1" viewportClassName="px-4 pb-4">
          <div className="grid gap-6 text-[0.8125rem]">
            <LineageTimeline environmentId={env} sandboxId={sb.id} />
            {!building && <CheckpointsSection env={env} sandbox={sb} />}
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}
