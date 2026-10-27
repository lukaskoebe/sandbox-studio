import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { HeartbeatIcon } from "@phosphor-icons/react"
import { useSavePart } from "@/components/save-bar"
import { SectionHeader } from "@/components/section-header"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"
import {
  defaultHeartbeat,
  heartbeatSentence,
  hourLabel,
  type Heartbeat,
} from "@/lib/tasks"

const fromHours = Object.fromEntries(
  Array.from({ length: 24 }, (_, h) => [String(h), hourLabel(h)])
)
const toHours = Object.fromEntries(
  Array.from({ length: 24 }, (_, h) => [String(h + 1), hourLabel(h + 1)])
)

/**
 * A teammate's check-ins: off by default. On, Studio gives the teammate a chat turn with its
 * open tasks every few hours, within a window of local hours.
 */
export function TeammateHeartbeat({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const queryClient = useQueryClient()
  const saved = teammate.heartbeat
  const [draft, setDraft] = useState<Heartbeat>(saved ?? defaultHeartbeat)

  const refresh = () =>
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/teammates"],
    })
  const failed = (err: unknown) =>
    toast.error("Could not change the check-ins", {
      description: errorMessage(err),
    })
  const path = { params: { path: { env, id: teammate.id } } }
  const set = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{id}/heartbeat",
    { onSuccess: refresh, onError: failed }
  )
  const clear = $api.useMutation(
    "delete",
    "/api/environments/{env}/teammates/{id}/heartbeat",
    { onSuccess: refresh, onError: failed }
  )
  const busy = set.isPending || clear.isPending

  const windowError =
    draft.fromHour >= draft.toHour
      ? "The window must end after it starts."
      : undefined
  const valid =
    Number.isInteger(draft.everyHours) &&
    draft.everyHours >= 1 &&
    draft.everyHours <= 24 &&
    windowError === undefined
  const unchanged =
    saved !== undefined &&
    saved.everyHours === draft.everyHours &&
    saved.fromHour === draft.fromHour &&
    saved.toHour === draft.toHour
  // In a SaveBar the page's bar saves the times with the teammate's other changes.
  const inBar = useSavePart("checkins", {
    dirty: saved !== undefined && !unchanged,
    valid,
    save: () => set.mutateAsync({ ...path, body: draft }),
    discard: () => setDraft(saved ?? defaultHeartbeat),
  })

  return (
    <section className="grid gap-4">
      <SectionHeader title="Check-ins" icon={HeartbeatIcon} area="chat" />
      <p className="text-xs text-muted-foreground">
        When {teammate.name} has open tasks, Studio starts a chat turn for it in
        its latest sandbox at these times, with the tasks to work on. A check-in
        that can't run is logged in its Tasks tab.
      </p>
      <label className="flex items-center gap-2 text-xs">
        <Switch
          checked={saved !== undefined}
          disabled={busy}
          onCheckedChange={(on) =>
            on ? set.mutate({ ...path, body: draft }) : clear.mutate(path)
          }
        />
        Check in on open tasks
      </label>
      {saved && (
        <form
          className="grid gap-4 sm:grid-cols-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (valid && !busy && !unchanged)
              set.mutate({ ...path, body: draft })
          }}
        >
          <Field>
            <FieldLabel htmlFor="heartbeat-every">Every (hours)</FieldLabel>
            <Input
              id="heartbeat-every"
              type="number"
              min={1}
              max={24}
              value={Number.isNaN(draft.everyHours) ? "" : draft.everyHours}
              onChange={(e) =>
                setDraft({ ...draft, everyHours: Number(e.target.value) })
              }
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="heartbeat-from">From</FieldLabel>
            <Select
              value={String(draft.fromHour)}
              onValueChange={(v) => {
                if (v) setDraft({ ...draft, fromHour: Number(v) })
              }}
              items={fromHours}
            >
              <SelectTrigger id="heartbeat-from" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(fromHours).map(([h, label]) => (
                  <SelectItem key={h} value={h}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field data-invalid={windowError ? true : undefined}>
            <FieldLabel htmlFor="heartbeat-to">To</FieldLabel>
            <Select
              value={String(draft.toHour)}
              onValueChange={(v) => {
                if (v) setDraft({ ...draft, toHour: Number(v) })
              }}
              items={toHours}
            >
              <SelectTrigger id="heartbeat-to" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(toHours).map(([h, label]) => (
                  <SelectItem key={h} value={h}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {windowError && <FieldError>{windowError}</FieldError>}
          </Field>
          <div className="flex flex-wrap items-center justify-between gap-3 sm:col-span-3">
            <FieldDescription>
              {heartbeatSentence(draft)}, in this Studio's local time.
            </FieldDescription>
            {!inBar && (
              <Button type="submit" disabled={!valid || busy || unchanged}>
                {busy && <Spinner />}
                Save check-ins
              </Button>
            )}
          </div>
        </form>
      )}
    </section>
  )
}
