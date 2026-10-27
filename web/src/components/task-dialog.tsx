import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { $api, errorMessage, errorStatus } from "@/lib/api/client"
import {
  hourLabel,
  maxTaskDetailsBytes,
  scheduleSentence,
  taskStatusLabels,
  weekdays,
  type Task,
  type TaskSchedule,
  type Weekday,
} from "@/lib/tasks"

/** Limit of a task's title in characters, as the server counts them. */
const maxTitleChars = 200

type Repeat = "off" | TaskSchedule["repeat"]

const repeatLabels: Record<Repeat, string> = {
  off: "Off",
  once: "Once",
  days: "Days",
  hours: "Every N hours",
}

const fromHours = Object.fromEntries(
  Array.from({ length: 24 }, (_, h) => [String(h), hourLabel(h)])
)
const toHours = Object.fromEntries(
  Array.from({ length: 24 }, (_, h) => [String(h + 1), hourLabel(h + 1)])
)

/** The sandbox picker's value for the teammate's latest sandbox. */
const latest = "latest"

/** Tomorrow as YYYY-MM-DD in local time: where a new one-off schedule starts. */
function tomorrow() {
  const d = new Date()
  d.setDate(d.getDate() + 1)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
}

/** Adds a task to a teammate's list, or edits one. */
export function TaskDialog({
  env,
  teammateId,
  task,
  open,
  onOpenChange,
}: {
  env: string
  teammateId: string
  /** The task to edit; absent to add one. */
  task?: Task
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl lg:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{task ? "Edit task" : "New task"}</DialogTitle>
          <DialogDescription>
            {task
              ? "Its details are markdown. Done tasks stay in the list."
              : "A task the teammate works on in its check-ins, at its scheduled times, or when you ask it to."}
          </DialogDescription>
        </DialogHeader>
        <TaskForm
          key={task?.id ?? "new"}
          env={env}
          teammateId={teammateId}
          task={task}
          onSaved={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function TaskForm({
  env,
  teammateId,
  task,
  onSaved,
}: {
  env: string
  teammateId: string
  task?: Task
  onSaved: () => void
}) {
  const queryClient = useQueryClient()
  const [title, setTitle] = useState(task?.title ?? "")
  const [details, setDetails] = useState(task?.details ?? "")
  const [due, setDue] = useState(task?.due ?? "")
  const [status, setStatus] = useState<Task["status"]>(task?.status ?? "open")
  const current = task?.schedule
  const [repeat, setRepeat] = useState<Repeat>(current?.repeat ?? "off")
  const [date, setDate] = useState(current?.date ?? tomorrow())
  const [time, setTime] = useState(current?.time ?? "09:00")
  const [days, setDays] = useState<Weekday[]>(
    current?.days ?? ["mon", "tue", "wed", "thu", "fri"]
  )
  const [everyHours, setEveryHours] = useState(current?.everyHours ?? 4)
  const [fromHour, setFromHour] = useState(current?.fromHour ?? 8)
  const [toHour, setToHour] = useState(current?.toHour ?? 20)
  const [paused, setPaused] = useState(current?.paused ?? false)
  const [sandboxId, setSandboxId] = useState(task?.sandboxId || latest)
  const [formError, setFormError] = useState<string>()
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const owned = (sandboxes.data ?? []).filter(
    (s) => s.teammateId === teammateId
  )
  const sandboxItems: Record<string, string> = {
    [latest]: "Latest sandbox",
    ...Object.fromEntries(owned.map((s) => [s.id, s.name])),
  }

  const schedule: TaskSchedule | undefined =
    repeat === "off"
      ? undefined
      : repeat === "once"
        ? { repeat, date, time, paused: paused || undefined }
        : repeat === "days"
          ? { repeat, days, time, paused: paused || undefined }
          : {
              repeat,
              everyHours,
              fromHour,
              toHour,
              paused: paused || undefined,
            }
  const scheduleError =
    repeat === "once" && (!date || !time)
      ? "Choose a day and a time."
      : repeat === "days" && (days.length === 0 || !time)
        ? "Choose at least one day and a time."
        : repeat === "hours" &&
            (!Number.isInteger(everyHours) || everyHours < 1 || everyHours > 24)
          ? "Runs are every 1 to 24 hours."
          : repeat === "hours" && fromHour >= toHour
            ? "The window must end after it starts."
            : undefined

  const titleChars = Array.from(title.trim()).length
  const detailsBytes = new TextEncoder().encode(details).length
  const valid =
    titleChars > 0 &&
    titleChars <= maxTitleChars &&
    detailsBytes <= maxTaskDetailsBytes &&
    scheduleError === undefined

  const saved = () => {
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/teammates/{id}/tasks"],
    })
    onSaved()
  }
  const onFailed = (err: unknown) => {
    if (errorStatus(err) === 422) setFormError(errorMessage(err))
    else
      toast.error("Could not save the task", {
        description: errorMessage(err),
      })
  }
  const create = $api.useMutation(
    "post",
    "/api/environments/{env}/teammates/{id}/tasks",
    { onSuccess: saved, onError: onFailed }
  )
  const update = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{id}/tasks/{task}",
    { onSuccess: saved, onError: onFailed }
  )
  const saving = create.isPending || update.isPending

  const submit = () => {
    if (!valid || saving) return
    setFormError(undefined)
    const body = {
      title: title.trim(),
      details: details.trim() || undefined,
      due: due || undefined,
      schedule,
      sandboxId: schedule && sandboxId !== latest ? sandboxId : undefined,
    }
    if (task)
      update.mutate({
        params: { path: { env, id: teammateId, task: task.id } },
        body: { ...body, status },
      })
    else create.mutate({ params: { path: { env, id: teammateId } }, body })
  }

  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <Field data-invalid={titleChars > maxTitleChars || undefined}>
        <FieldLabel htmlFor="task-title">Title</FieldLabel>
        <Input
          id="task-title"
          autoFocus
          aria-invalid={titleChars > maxTitleChars}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
        />
        {titleChars > maxTitleChars ? (
          <FieldError>The title is limited to 200 characters.</FieldError>
        ) : (
          <FieldDescription>One line.</FieldDescription>
        )}
      </Field>
      <Field data-invalid={detailsBytes > maxTaskDetailsBytes || undefined}>
        <FieldLabel htmlFor="task-details">Details</FieldLabel>
        <Textarea
          id="task-details"
          rows={6}
          className="font-mono text-xs/relaxed"
          placeholder={"Context, steps, and what done means"}
          aria-invalid={detailsBytes > maxTaskDetailsBytes}
          value={details}
          onChange={(e) => setDetails(e.target.value)}
        />
        {detailsBytes > maxTaskDetailsBytes ? (
          <FieldError>The details are limited to 8 KiB.</FieldError>
        ) : (
          <FieldDescription>Markdown, up to 8 KiB.</FieldDescription>
        )}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="task-due">Due</FieldLabel>
          <Input
            id="task-due"
            type="date"
            value={due}
            onChange={(e) => setDue(e.target.value)}
          />
          <FieldDescription>Optional.</FieldDescription>
        </Field>
        {task && (
          <Field>
            <FieldLabel htmlFor="task-status">Status</FieldLabel>
            <Select
              value={status}
              onValueChange={(v) => {
                if (v) setStatus(v)
              }}
              items={taskStatusLabels}
            >
              <SelectTrigger id="task-status" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(Object.keys(taskStatusLabels) as Task["status"][]).map(
                  (s) => (
                    <SelectItem key={s} value={s}>
                      {taskStatusLabels[s]}
                    </SelectItem>
                  )
                )}
              </SelectContent>
            </Select>
          </Field>
        )}
      </div>
      <Field>
        <FieldLabel htmlFor="task-schedule">Schedule</FieldLabel>
        <Select
          value={repeat}
          onValueChange={(v) => {
            if (v) setRepeat(v)
          }}
          items={repeatLabels}
        >
          <SelectTrigger id="task-schedule" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(Object.keys(repeatLabels) as Repeat[]).map((r) => (
              <SelectItem key={r} value={r}>
                {repeatLabels[r]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {repeat === "off" && (
          <FieldDescription>
            Run the task in a chat of its own at set times.
          </FieldDescription>
        )}
      </Field>
      {repeat === "once" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="task-run-date">Day</FieldLabel>
            <Input
              id="task-run-date"
              type="date"
              value={date}
              onChange={(e) => setDate(e.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="task-run-time">Time</FieldLabel>
            <Input
              id="task-run-time"
              type="time"
              value={time}
              onChange={(e) => setTime(e.target.value)}
            />
          </Field>
        </div>
      )}
      {repeat === "days" && (
        <div className="grid gap-4 sm:grid-cols-[1fr_auto]">
          <Field>
            <FieldLabel>Days</FieldLabel>
            <ToggleGroup
              aria-label="Days"
              variant="outline"
              size="sm"
              multiple
              value={days}
              onValueChange={(v) => setDays(v as Weekday[])}
              className="w-full flex-wrap"
            >
              {weekdays.map(([d, name]) => (
                <ToggleGroupItem key={d} value={d} className="flex-1">
                  {name}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </Field>
          <Field>
            <FieldLabel htmlFor="task-run-time">Time</FieldLabel>
            <Input
              id="task-run-time"
              type="time"
              value={time}
              onChange={(e) => setTime(e.target.value)}
            />
          </Field>
        </div>
      )}
      {repeat === "hours" && (
        <div className="grid gap-4 sm:grid-cols-3">
          <Field>
            <FieldLabel htmlFor="task-run-every">Every (hours)</FieldLabel>
            <Input
              id="task-run-every"
              type="number"
              min={1}
              max={24}
              value={Number.isNaN(everyHours) ? "" : everyHours}
              onChange={(e) => setEveryHours(Number(e.target.value))}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="task-run-from">From</FieldLabel>
            <Select
              value={String(fromHour)}
              onValueChange={(v) => {
                if (v) setFromHour(Number(v))
              }}
              items={fromHours}
            >
              <SelectTrigger id="task-run-from" className="w-full">
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
          <Field>
            <FieldLabel htmlFor="task-run-to">To</FieldLabel>
            <Select
              value={String(toHour)}
              onValueChange={(v) => {
                if (v) setToHour(Number(v))
              }}
              items={toHours}
            >
              <SelectTrigger id="task-run-to" className="w-full">
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
          </Field>
        </div>
      )}
      {schedule && (
        <>
          {scheduleError ? (
            <FieldError>{scheduleError}</FieldError>
          ) : (
            <FieldDescription>
              {scheduleSentence(schedule)}, in this Studio&apos;s local time.
              Each run is a new chat.
            </FieldDescription>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="task-sandbox">Sandbox</FieldLabel>
              <Select
                value={sandboxId}
                onValueChange={(v) => {
                  if (v) setSandboxId(v)
                }}
                items={sandboxItems}
              >
                <SelectTrigger id="task-sandbox" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {Object.entries(sandboxItems).map(([v, label]) => (
                    <SelectItem key={v} value={v}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {task?.schedule && (
              <label className="flex items-center gap-2 self-end pb-2 text-xs">
                <Switch checked={paused} onCheckedChange={setPaused} />
                Paused
              </label>
            )}
          </div>
        </>
      )}
      {formError && <FieldError>{formError}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={!valid || saving}>
          {saving && <Spinner />}
          {task ? "Save" : "Add task"}
        </Button>
      </DialogFooter>
    </form>
  )
}
