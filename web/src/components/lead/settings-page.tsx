import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BellIcon,
  CalendarIcon,
  ClockIcon,
  CurrencyDollarIcon,
  ChatTextIcon,
  EyeIcon,
  LockIcon,
  MonitorIcon,
  PencilSimpleIcon,
  SparkleIcon,
  TextHIcon,
  TrayIcon,
} from "@phosphor-icons/react"
import { ColumnPage } from "@/components/column-page"
import { SettingRow, SettingsGroup } from "@/components/setting-row"
import { Button } from "@/components/ui/button"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { $api, errorMessage } from "@/lib/api/client"
import {
  dollars,
  hhmm,
  weekdays,
  type LeadSettings,
  type LeadView,
} from "@/lib/lead"
import { LeadBack } from "./back"

const notifyChoices: Record<LeadSettings["notify"], string> = {
  waiting: "Only if something needs you",
  always: "Every time",
  never: "Never",
}

const followUpChoices = {
  ask: "When you approve",
  own: "Follow-ups in its own threads",
}

/**
 * How Lead writes the day (docs/lead.md): its name, when, on which days, what goes in, the
 * model it calls and what that may cost, and when it tells you. Each control saves when it
 * changes. What Lead may do is mostly fixed: it reads, and sends a plan's messages once you approve
 * them; only its follow-ups in threads its plan started may go without asking.
 */
export function LeadSettingsPage({ env }: { env: string }) {
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const v = lead.data
  return (
    <ColumnPage
      title={v ? `${v.settings.name}'s settings` : "Lead's settings"}
      narrow
      back={<LeadBack env={env} />}
    >
      {lead.isPending ? (
        <p className="flex items-center gap-2 text-muted-foreground">
          <Spinner /> Loading…
        </p>
      ) : lead.error ? (
        <p role="alert" className="text-destructive">
          {errorMessage(lead.error)}
        </p>
      ) : (
        v && <Settings env={env} v={v} />
      )}
    </ColumnPage>
  )
}

function Settings({ env, v }: { env: string; v: LeadView }) {
  const queryClient = useQueryClient()
  const save = $api.useMutation(
    "put",
    "/api/environments/{env}/lead/settings",
    {
      // Each save sends every setting, so they go one after another: a change made while one
      // is on its way can't be overwritten by it.
      scope: { id: `lead-settings:${env}` },
      onSuccess: () =>
        void queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/lead"],
        }),
      onError: (err) =>
        toast.error("Could not save Lead's settings", {
          description: errorMessage(err),
        }),
    }
  )
  // While a save is on its way the controls show what it sends.
  const s: LeadSettings = save.isPending
    ? { ...v.settings, ...save.variables.body }
    : v.settings
  const set = (patch: Partial<LeadSettings>) => {
    const next = { ...s, ...patch }
    save.mutate({
      params: { path: { env } },
      body: {
        name: next.name,
        writeAt: next.writeAt,
        days: next.days,
        skipQuiet: next.skipQuiet,
        ownComputers: next.ownComputers,
        showCosts: next.showCosts,
        heading: next.heading,
        dailyLimit: next.dailyLimit,
        notify: next.notify,
        followUps: next.followUps,
      },
    })
  }
  const browserOffset = -new Date().getTimezoneOffset()
  const m = v.model
  return (
    <>
      <SettingsGroup label="The day">
        <NameRow
          name={s.name}
          busy={save.isPending}
          onSave={(name) => set({ name })}
        />
        <SettingRow
          icon={ClockIcon}
          title="Write the day at"
          description={`In Studio's time, ${zoneName(v.zone, v.offset)}${
            browserOffset !== v.offset
              ? `; this browser is at ${zoneName("", browserOffset)}`
              : ""
          }. Summarize now on the day writes it whenever you like.`}
          control={
            <TimeInput
              label="Write the day at"
              minutes={s.writeAt}
              disabled={save.isPending}
              onSave={(writeAt) => set({ writeAt })}
            />
          }
        />
        <SettingRow
          icon={CalendarIcon}
          title="On"
          description="The days it is written on. On the others the facts are still there, and Summarize now still writes them."
          control={
            <DaysPicker
              label="Days the day is written on"
              days={s.days}
              disabled={save.isPending}
              onChange={(days) => set({ days })}
            />
          }
        />
        <Toggle
          icon={TrayIcon}
          title="Skip days without turns"
          description="A day on which no teammate had a turn isn't written."
          checked={s.skipQuiet}
          disabled={save.isPending}
          onChange={(skipQuiet) => set({ skipQuiet })}
        />
        <Toggle
          icon={MonitorIcon}
          title="Include your own computers"
          description="The sessions you started by hand on your own computers. Off, Lead reads only teammates' work."
          checked={s.ownComputers}
          disabled={save.isPending}
          onChange={(ownComputers) => set({ ownComputers })}
        />
        <Toggle
          icon={CurrencyDollarIcon}
          title="Show costs"
          description="What each teammate's turns cost, as the agents reported it, and what Lead's own calls cost."
          checked={s.showCosts}
          disabled={save.isPending}
          onChange={(showCosts) => set({ showCosts })}
        />
        <Toggle
          icon={TextHIcon}
          title="A heading for the day"
          description="A few words on the day as a whole, after its date."
          checked={s.heading}
          disabled={save.isPending}
          onChange={(heading) => set({ heading })}
        />
      </SettingsGroup>

      <SettingsGroup label="Model and budget">
        <SettingRow
          icon={SparkleIcon}
          title="The model it calls"
          description={
            m
              ? `The utility model of ${m.provider ? `the ${m.provider} provider` : "a teammate's provider"}, as memory uses. It writes a short note on each teammate after their turns and the day's sentences from those notes.`
              : "None of this environment's teammates has a provider with a utility model, so the day has its facts and links but no sentences. Settings → Providers sets one."
          }
          control={
            m && (
              <span className="font-mono text-xs text-muted-foreground">
                {m.model}
                {m.priced && m.priceInPerM !== undefined
                  ? ` · $${m.priceInPerM}/$${m.priceOutPerM} per M`
                  : ""}
              </span>
            )
          }
        />
        <LimitRow
          v={v}
          limit={s.dailyLimit}
          busy={save.isPending}
          onSave={(dailyLimit) => set({ dailyLimit })}
        />
      </SettingsGroup>

      <SettingsGroup label="What it may do">
        <SettingRow
          icon={EyeIcon}
          title="Read threads, sessions, pushes, approvals and spend"
          description="Always: this is what Lead is. It reads what Studio records, not the teammates' files, terminals or screens."
          control={<Locked>Always</Locked>}
        />
        <SettingRow
          icon={ChatTextIcon}
          title="Message teammates in their threads, or start a thread on their computer"
          description={`Only in a plan you approved, and only the messages as you saw them; a change to the plan withdraws its approval. ${
            s.followUps
              ? "Once a plan's messages are answered, Lead may follow up in a thread the plan started without asking, at most twice; a follow-up in any other thread waits for you."
              : "When Lead would follow up on the answers, the follow-up waits for you too."
          }`}
          control={
            <Select
              value={s.followUps ? "own" : "ask"}
              items={followUpChoices}
              disabled={save.isPending}
              onValueChange={(c) => c && set({ followUps: c === "own" })}
            >
              <SelectTrigger
                size="sm"
                aria-label="When Lead messages teammates"
                className="w-60"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(followUpChoices).map(([k, text]) => (
                  <SelectItem key={k} value={k}>
                    {text}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          }
        />
        <SettingRow
          icon={LockIcon}
          title="Steer or stop their work, start a task's run"
          description="Not in this version."
          control={<Locked>Not yet</Locked>}
        />
        <SettingRow
          icon={LockIcon}
          title="Approve or deny requests, change rules, secrets or budgets, push, merge or comment"
          description="Never. Lead has no tool for these."
          control={<Locked>Never</Locked>}
        />
      </SettingsGroup>

      <SettingsGroup label="Notifications">
        <SettingRow
          icon={BellIcon}
          title="When the day is written"
          description="A notification from this browser or the desktop app, while Studio is open in one."
          control={
            <Select
              value={s.notify}
              items={notifyChoices}
              disabled={save.isPending}
              onValueChange={(n) => n && set({ notify: n })}
            >
              <SelectTrigger
                size="sm"
                aria-label="When the day is written"
                className="w-60"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(notifyChoices).map(([k, text]) => (
                  <SelectItem key={k} value={k}>
                    {text}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          }
        />
      </SettingsGroup>
    </>
  )
}

function Toggle({
  icon,
  title,
  description,
  checked,
  disabled,
  onChange,
}: {
  icon: typeof BellIcon
  title: string
  description: string
  checked: boolean
  disabled: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <SettingRow
      icon={icon}
      title={title}
      description={description}
      control={
        <Switch
          aria-label={title}
          checked={checked}
          disabled={disabled}
          onCheckedChange={onChange}
        />
      }
    />
  )
}

function Locked({ children }: { children: string }) {
  return (
    <span className="inline-flex items-center gap-1 font-mono text-meta text-muted-foreground">
      <LockIcon aria-hidden className="size-3" />
      {children}
    </span>
  )
}

function NameRow({
  name,
  busy,
  onSave,
}: {
  name: string
  busy: boolean
  onSave: (name: string) => void
}) {
  const [draft, setDraft] = useState<string>()
  const value = draft ?? name
  const changed = value.trim() !== "" && value.trim() !== name
  return (
    <SettingRow
      icon={PencilSimpleIcon}
      title="Name"
      description="What the sidebar and the day call it."
      control={
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (changed) onSave(value.trim())
            setDraft(undefined)
          }}
        >
          <Input
            aria-label="Lead's name"
            className="w-40"
            value={value}
            maxLength={40}
            disabled={busy}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") setDraft(undefined)
            }}
          />
          <Button
            type="submit"
            size="sm"
            variant="outline"
            disabled={!changed || busy}
          >
            Rename
          </Button>
        </form>
      }
    />
  )
}

/** "CET (UTC+1)", or "UTC+5:30" without a name. */
export function zoneName(zone: string, offset: number): string {
  const h = Math.floor(Math.abs(offset) / 60)
  const m = Math.abs(offset) % 60
  const utc = `UTC${offset < 0 ? "−" : "+"}${h}${m ? `:${String(m).padStart(2, "0")}` : ""}`
  return zone && !/^[+-]/.test(zone) ? `${zone} (${utc})` : utc
}

/** The days of the week as toggles, Monday first. */
export function DaysPicker({
  label,
  days,
  disabled,
  onChange,
}: {
  label: string
  days: number
  disabled?: boolean
  onChange: (days: number) => void
}) {
  return (
    <ToggleGroup
      aria-label={label}
      variant="outline"
      size="sm"
      multiple
      value={weekdays.filter((_, i) => days & (1 << i))}
      disabled={disabled}
      onValueChange={(picked) =>
        onChange(
          weekdays.reduce(
            (bits, d, i) => (picked.includes(d) ? bits | (1 << i) : bits),
            0
          )
        )
      }
    >
      {weekdays.map((d) => (
        <ToggleGroupItem key={d} value={d} className="w-8 px-0">
          {d}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/** A time of day that saves on Enter or when it loses focus. */
export function TimeInput({
  label,
  minutes,
  disabled,
  onSave,
}: {
  label: string
  minutes: number
  disabled?: boolean
  onSave: (min: number) => void
}) {
  const [draft, setDraft] = useState<string>()
  const commit = () => {
    if (draft === undefined) return
    const [h, m] = draft.split(":").map(Number)
    setDraft(undefined)
    if (Number.isFinite(h) && Number.isFinite(m)) {
      const min = h * 60 + m
      if (min !== minutes) onSave(min)
    }
  }
  return (
    <Input
      type="time"
      aria-label={label}
      className="w-28 font-mono"
      value={draft ?? hhmm(minutes)}
      disabled={disabled}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit()
        if (e.key === "Escape") setDraft(undefined)
      }}
    />
  )
}

function LimitRow({
  v,
  limit,
  busy,
  onSave,
}: {
  v: LeadView
  limit: number
  busy: boolean
  onSave: (micros: number) => void
}) {
  const [draft, setDraft] = useState<string>()
  const used = v.usage.cost
  const priced = v.model?.priced ?? false
  const commit = () => {
    if (draft === undefined) return
    const n = Number(draft)
    setDraft(undefined)
    if (Number.isFinite(n) && n >= 0 && n <= 100) {
      const micros = Math.round(n * 1e6)
      if (micros !== limit) onSave(micros)
    }
  }
  const share = limit > 0 ? Math.min(1, used / limit) : 1
  return (
    <SettingRow
      icon={CurrencyDollarIcon}
      title="Daily limit"
      description={
        priced
          ? "What Lead's calls may cost a day. When it runs out, the day keeps its facts and links but loses its sentences."
          : "The model has no known price, so Lead stops at 60 calls a day instead."
      }
      control={
        <span className="flex items-center gap-1 font-mono text-xs">
          $
          <Input
            type="number"
            inputMode="decimal"
            min={0}
            max={100}
            step={0.25}
            aria-label="Daily limit in dollars"
            className="w-20 font-mono"
            value={draft ?? String(limit / 1e6)}
            disabled={busy || !priced}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={commit}
            onKeyDown={(e) => {
              if (e.key === "Enter") commit()
              if (e.key === "Escape") setDraft(undefined)
            }}
          />
        </span>
      }
    >
      <div className="grid max-w-sm gap-1">
        <div
          role="meter"
          aria-label="Used today"
          aria-valuemin={0}
          aria-valuemax={priced ? limit : 60}
          aria-valuenow={priced ? used : v.usage.calls}
          className="h-1.5 overflow-hidden rounded-full bg-muted"
        >
          <div
            className="h-full rounded-full bg-(--st-running)"
            style={{
              width: `${(priced ? share : Math.min(1, v.usage.calls / 60)) * 100}%`,
            }}
          />
        </div>
        <p className="text-muted-foreground">
          {priced
            ? `${dollars(used)} of ${dollars(limit)} used today`
            : `${v.usage.calls} of 60 calls today`}
          {v.usage.skipped > 0 &&
            ` · ${v.usage.skipped} ${v.usage.skipped === 1 ? "call" : "calls"} left out at the limit`}
        </p>
      </div>
    </SettingRow>
  )
}
