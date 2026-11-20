import { useState } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CaretLeftIcon } from "@phosphor-icons/react"
import { ColumnPage } from "@/components/column-page"
import { SettingsGroup } from "@/components/setting-row"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { $api, errorMessage } from "@/lib/api/client"
import {
  clock,
  daysLabel,
  planStateLabel,
  today,
  type LeadOrder,
  type OrderInput,
} from "@/lib/lead"
import { DaysPicker, TimeInput, zoneName } from "./settings-page"

const maxOrders = 20

function useRefreshOrders() {
  const queryClient = useQueryClient()
  return () =>
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/lead/orders"],
    })
}

/**
 * Lead's standing orders (docs/lead.md#standing-orders): the user's words for a plan Lead
 * drafts on its own at a set time. Each plan waits for approval as any plan does; an order only
 * saves asking for it. text starts the new order's box, as a done plan's link fills it.
 */
export function LeadOrdersPage({ env, text }: { env: string; text?: string }) {
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const orders = $api.useQuery("get", "/api/environments/{env}/lead/orders", {
    params: { path: { env } },
  })
  const name = lead.data?.settings.name ?? "Lead"
  const list = orders.data ?? []
  const zone = lead.data ? zoneName(lead.data.zone, lead.data.offset) : ""
  return (
    <ColumnPage
      title="Standing orders"
      narrow
      actions={
        <Button
          size="sm"
          variant="ghost"
          render={<Link to="/e/$env/lead" params={{ env }} />}
        >
          <CaretLeftIcon />
          Back to the day
        </Button>
      }
      intro={
        <p>
          What {name} should do at a set time, such as each morning. When an
          order comes due, {name} drafts a plan from your words and it waits for
          your approval like any other plan. Times are in Studio's time
          {zone && `, ${zone}`}.
        </p>
      }
    >
      {orders.isPending ? (
        <p className="flex items-center gap-2 text-muted-foreground">
          <Spinner /> Loading…
        </p>
      ) : orders.error ? (
        <p role="alert" className="text-destructive">
          {errorMessage(orders.error)}
        </p>
      ) : (
        <>
          {list.length > 0 && (
            <SettingsGroup
              label="Orders"
              aside={
                <span className="text-caption text-muted-foreground">
                  {list.length} of {maxOrders}
                </span>
              }
            >
              {list.map((o) => (
                <OrderRow key={o.id} env={env} o={o} name={name} />
              ))}
            </SettingsGroup>
          )}
          {list.length < maxOrders ? (
            <NewOrder
              key={text ?? ""}
              env={env}
              name={name}
              text={text}
              first={list.length === 0}
            />
          ) : (
            <p className="text-caption text-muted-foreground">
              An environment has at most {maxOrders} standing orders. Delete one
              to add another.
            </p>
          )}
        </>
      )}
    </ColumnPage>
  )
}

/** When an order comes due next: "today 09:00", "tomorrow 09:00" or "Mon 09:00". */
function nextLabel(at: string): string {
  const d = new Date(at)
  const t = new Date()
  const tomorrow = new Date(t)
  tomorrow.setDate(t.getDate() + 1)
  const day =
    today(d) === today(t)
      ? "today"
      : today(d) === today(tomorrow)
        ? "tomorrow"
        : d.toLocaleDateString(undefined, { weekday: "short" })
  return `${day} ${clock(at)}`
}

function OrderRow({
  env,
  o,
  name,
}: {
  env: string
  o: LeadOrder
  name: string
}) {
  const refresh = useRefreshOrders()
  const navigate = useNavigate()
  const [editing, setEditing] = useState<string>()
  const [deleting, setDeleting] = useState(false)
  const save = $api.useMutation(
    "put",
    "/api/environments/{env}/lead/orders/{order}",
    {
      // Each save sends the whole order, so they go one after another.
      scope: { id: `lead-order:${o.id}` },
      onSuccess: () => {
        setEditing(undefined)
        refresh()
      },
      onError: (err) =>
        toast.error("Could not save the standing order", {
          description: errorMessage(err),
        }),
    }
  )
  const run = $api.useMutation(
    "post",
    "/api/environments/{env}/lead/orders/{order}/run",
    {
      onSuccess: (p) => {
        refresh()
        void navigate({
          to: "/e/$env/lead/plans/$plan",
          params: { env, plan: p.id },
        })
      },
      onError: (err) =>
        toast.error(`Could not ask ${name} for the plan`, {
          description: errorMessage(err),
        }),
    }
  )
  const del = $api.useMutation(
    "delete",
    "/api/environments/{env}/lead/orders/{order}",
    {
      onSuccess: refresh,
      onError: (err) =>
        toast.error("Could not delete the standing order", {
          description: errorMessage(err),
        }),
    }
  )
  // While a save is on its way the controls show what it sends.
  const v: OrderInput = save.isPending ? save.variables.body : o
  const set = (patch: Partial<OrderInput>) => {
    const next = { text: v.text, at: v.at, days: v.days, enabled: v.enabled }
    save.mutate({
      params: { path: { env, order: o.id } },
      body: { ...next, ...patch },
    })
  }
  const busy = save.isPending || del.isPending
  return (
    <div
      role="group"
      aria-label={`Standing order: ${o.text.slice(0, 80)}`}
      className="grid gap-3 px-4 py-3"
    >
      <div className="flex items-start gap-3">
        {editing === undefined ? (
          <p className="min-w-0 flex-1 text-sm/relaxed break-words whitespace-pre-wrap">
            {v.text}
          </p>
        ) : (
          <form
            className="grid min-w-0 flex-1 gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (editing.trim()) set({ text: editing.trim() })
            }}
          >
            <Textarea
              aria-label="What Lead should do"
              value={editing}
              maxLength={2000}
              autoFocus
              disabled={busy}
              onChange={(e) => setEditing(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") setEditing(undefined)
              }}
              className="max-h-40 min-h-16"
            />
            <div className="flex gap-2">
              <Button
                type="submit"
                size="sm"
                disabled={!editing.trim() || busy}
              >
                {save.isPending && <Spinner />}
                Save
              </Button>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => setEditing(undefined)}
              >
                Cancel
              </Button>
            </div>
          </form>
        )}
        <Switch
          aria-label="On"
          checked={v.enabled}
          disabled={busy}
          onCheckedChange={(enabled) => set({ enabled })}
        />
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <TimeInput
          label="Comes due at"
          minutes={v.at}
          disabled={busy}
          onSave={(at) => set({ at })}
        />
        <DaysPicker
          label="Days it comes due on"
          days={v.days}
          disabled={busy}
          onChange={(days) => days !== 0 && set({ days })}
        />
      </div>
      <p className="text-caption text-muted-foreground">
        {v.enabled
          ? o.next
            ? `Comes due next ${nextLabel(o.next)}`
            : daysLabel(v.days)
          : "Off: it doesn't come due until you turn it on."}
        {o.lastPlan && (
          <>
            {" · last plan "}
            <Link
              to="/e/$env/lead/plans/$plan"
              params={{ env, plan: o.lastPlan.id }}
              className="text-foreground underline-offset-2 hover:underline"
            >
              {o.lastPlan.title || "Plan"}
            </Link>
            {`, ${planStateLabel(o.lastPlan.state).toLowerCase()}`}
          </>
        )}
      </p>
      {o.note && (
        <p className="text-caption text-(--st-waiting-fg)">{o.note}</p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={run.isPending || busy}
          onClick={() => run.mutate({ params: { path: { env, order: o.id } } })}
        >
          {run.isPending && <Spinner />}
          Run now
        </Button>
        {editing === undefined && (
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => setEditing(o.text)}
          >
            Edit words
          </Button>
        )}
        <Button
          size="sm"
          variant="ghost"
          disabled={busy}
          onClick={() => setDeleting(true)}
        >
          Delete
        </Button>
      </div>
      <AlertDialog open={deleting} onOpenChange={setDeleting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this standing order?</AlertDialogTitle>
            <AlertDialogDescription>
              {name} no longer drafts its plan. The plans it drafted stay.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep it</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() =>
                del.mutate({ params: { path: { env, order: o.id } } })
              }
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function NewOrder({
  env,
  name,
  text: start,
  first,
}: {
  env: string
  name: string
  text?: string
  first: boolean
}) {
  const refresh = useRefreshOrders()
  const [text, setText] = useState(start ?? "")
  const [at, setAt] = useState("09:00")
  const [days, setDays] = useState(0b0011111)
  const add = $api.useMutation("post", "/api/environments/{env}/lead/orders", {
    onSuccess: () => {
      setText("")
      refresh()
    },
    onError: (err) =>
      toast.error("Could not add the standing order", {
        description: errorMessage(err),
      }),
  })
  const [h, m] = at.split(":").map(Number)
  const minutes = Number.isFinite(h) && Number.isFinite(m) ? h * 60 + m : -1
  const ok = text.trim() !== "" && minutes >= 0 && days !== 0
  return (
    <SettingsGroup label={first ? "Your first standing order" : "New order"}>
      <form
        aria-label="New standing order"
        className="grid gap-3 px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (!ok || add.isPending) return
          add.mutate({
            params: { path: { env } },
            body: { text: text.trim(), at: minutes, days, enabled: true },
          })
        }}
      >
        <Textarea
          aria-label="What Lead should do"
          placeholder="For example: hand failing checks on open pull requests to whoever is idle, and tell me who took what"
          value={text}
          maxLength={2000}
          autoFocus={!!start}
          onChange={(e) => setText(e.target.value)}
          className="max-h-40 min-h-16"
        />
        <div className="flex flex-wrap items-center gap-2">
          <Input
            type="time"
            aria-label="Comes due at"
            className="w-28 font-mono"
            value={at}
            onChange={(e) => setAt(e.target.value)}
          />
          <DaysPicker
            label="Days it comes due on"
            days={days}
            onChange={setDays}
          />
          <Button
            type="submit"
            size="sm"
            className="ml-auto"
            disabled={!ok || add.isPending}
          >
            {add.isPending && <Spinner />}
            Add order
          </Button>
        </div>
        <p className="text-caption text-muted-foreground">
          It first comes due at its next time. {name} skips a day while the plan
          from last time is still open. When Studio wasn't running at the time,{" "}
          {name} drafts it once Studio runs, that same day only.
        </p>
      </form>
    </SettingsGroup>
  )
}
