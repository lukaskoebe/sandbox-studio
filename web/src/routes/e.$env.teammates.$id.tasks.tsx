import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  HeartbeatIcon,
  ListChecksIcon,
  PlayIcon,
  PlusIcon,
} from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { TaskDialog } from "@/components/task-dialog"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import {
  beatLabel,
  heartbeatSentence,
  runLabel,
  runTime,
  scheduleSentence,
  taskStatusLabels,
  type Task,
} from "@/lib/tasks"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/teammates/$id/tasks")({
  component: TeammateTasks,
})

function TeammateTasks() {
  const { env, id } = Route.useParams()
  const queryClient = useQueryClient()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const tasks = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/tasks",
    { params: { path: { env, id } } }
  )
  const beats = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/heartbeats",
    { params: { path: { env, id } } }
  )
  const runs = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/task-runs",
    { params: { path: { env, id } } }
  )
  const [dialog, setDialog] = useState<{ open: boolean; task?: Task }>({
    open: false,
  })
  const teammate = teammates.data?.find((t) => t.id === id)
  const name = teammate?.name ?? "the teammate"
  const list = tasks.data ?? []
  const open = list.filter((t) => t.status !== "done").length

  const update = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{id}/tasks/{task}",
    {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/teammates/{id}/tasks"],
        }),
      onError: (err) =>
        toast.error("Could not change the task", {
          description: errorMessage(err),
        }),
    }
  )

  const run = $api.useMutation(
    "post",
    "/api/environments/{env}/teammates/{id}/tasks/{task}/run",
    {
      onSuccess: () => {
        toast.success("The run started in a chat of its own")
        for (const path of [
          "/api/environments/{env}/teammates/{id}/tasks",
          "/api/environments/{env}/teammates/{id}/task-runs",
        ] as const)
          void queryClient.invalidateQueries({ queryKey: ["get", path] })
      },
      onError: (err) =>
        toast.error("The run did not start", {
          description: errorMessage(err),
        }),
    }
  )

  /**
   * Ticking a task marks it done; unticking reopens it. The task's other fields stay; done
   * ends a repeating schedule.
   */
  const setDone = (task: Task, done: boolean) =>
    update.mutate({
      params: { path: { env, id, task: task.id } },
      body: {
        title: task.title,
        details: task.details,
        due: task.due,
        status: done ? "done" : "open",
        schedule: task.schedule,
        sandboxId: task.sandboxId,
      },
    })

  const titles = new Map(list.map((t) => [t.id, t.title]))
  const activity = [
    ...(beats.data ?? []).map((b) => ({
      key: b.id,
      at: b.at,
      text: beatLabel(b),
      chatId: b.chatId,
    })),
    ...(runs.data ?? []).map((r) => ({
      key: r.id,
      at: r.at,
      text: runLabel(r, titles.get(r.taskId) ?? "a task"),
      chatId: r.chatId,
    })),
  ].sort((a, b) => Date.parse(b.at) - Date.parse(a.at))

  return (
    <div className="max-w-3xl space-y-8">
      <section className="space-y-3">
        <SectionHeader
          title="Tasks"
          icon={ListChecksIcon}
          area="chat"
          count={tasks.data ? open : undefined}
          actions={
            <Button size="sm" onClick={() => setDialog({ open: true })}>
              <PlusIcon />
              Add task
            </Button>
          }
        />
        <p className="text-xs text-muted-foreground">
          What {name} works on. Its open tasks are what a check-in gives it to
          do, and it can add its own. A task with a schedule runs at its own
          times instead, each time in a new chat.
        </p>
        {tasks.isPending ? (
          <Spinner className="mx-auto block" />
        ) : tasks.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {errorMessage(tasks.error)}
          </p>
        ) : list.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No tasks yet</EmptyTitle>
              <EmptyDescription>
                Add one for {name} to work on.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ul className="divide-y rounded-lg ring-1 ring-foreground/10">
            {list.map((task) => (
              <li key={task.id} className="flex items-start gap-3 px-3 py-2.5">
                <Checkbox
                  className="mt-0.5"
                  aria-label={`Mark ${task.title} ${task.status === "done" ? "open" : "done"}`}
                  checked={task.status === "done"}
                  disabled={update.isPending}
                  onCheckedChange={(done) => setDone(task, done)}
                />
                <button
                  type="button"
                  className="min-w-0 flex-1 text-left outline-none focus-visible:underline"
                  onClick={() => setDialog({ open: true, task })}
                >
                  <span
                    className={
                      task.status === "done"
                        ? "block truncate text-sm text-muted-foreground line-through"
                        : "block truncate text-sm font-medium"
                    }
                  >
                    {task.title}
                  </span>
                  <span className="mt-0.5 flex flex-wrap gap-x-2 text-[0.6875rem] text-muted-foreground">
                    <span>{taskStatusLabels[task.status]}</span>
                    {task.due && <span>Due {task.due}</span>}
                    {task.createdBy === "teammate" && (
                      <span>Added by {name}</span>
                    )}
                  </span>
                  {task.schedule && task.status !== "done" && (
                    <span className="mt-0.5 flex flex-wrap gap-x-2 text-[0.6875rem] text-muted-foreground">
                      <span>
                        ⟳ {scheduleSentence(task.schedule)}
                        {task.schedule.paused
                          ? " · paused"
                          : task.nextRunAt
                            ? ` · next ${runTime(task.nextRunAt)}`
                            : " · no run left"}
                      </span>
                      {task.lastRun && (
                        <span>
                          Last run {runTime(task.lastRun.at)},{" "}
                          {task.lastRun.outcome}
                        </span>
                      )}
                    </span>
                  )}
                </button>
                {task.schedule && task.status !== "done" && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={run.isPending}
                    onClick={() =>
                      run.mutate({
                        params: { path: { env, id, task: task.id } },
                      })
                    }
                  >
                    {run.isPending &&
                    run.variables.params.path.task === task.id ? (
                      <Spinner />
                    ) : (
                      <PlayIcon />
                    )}
                    Run now
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="space-y-3">
        <SectionHeader
          title="Activity"
          icon={HeartbeatIcon}
          area="chat"
          count={beats.data || runs.data ? activity.length : undefined}
        />
        <p className="text-xs text-muted-foreground">
          {teammate?.heartbeat ? (
            <>
              Check-ins: {heartbeatSentence(teammate.heartbeat)}, when it has
              open tasks.
            </>
          ) : (
            <>
              Check-ins are off. Turn them on in{" "}
              <Link
                to="/e/$env/teammates/$id/settings"
                params={{ env, id }}
                className="underline underline-offset-4"
              >
                Settings
              </Link>
              .
            </>
          )}{" "}
          Only check-ins that started a chat or were skipped are listed, with
          the runs of scheduled tasks.
        </p>
        {beats.isPending || runs.isPending ? (
          <Spinner className="mx-auto block" />
        ) : activity.length === 0 ? (
          <p className="text-xs text-muted-foreground">Nothing logged yet.</p>
        ) : (
          <ul className="divide-y rounded-lg ring-1 ring-foreground/10">
            {activity.map((b) => (
              <li
                key={b.key}
                className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 px-3 py-2 text-xs"
              >
                <span className="min-w-0">{b.text}</span>
                <span className="flex items-center gap-3 text-muted-foreground">
                  {b.chatId && (
                    <Link
                      to="/e/$env/chats/$chat"
                      params={{ env, chat: b.chatId }}
                      className="underline underline-offset-4"
                    >
                      Open chat
                    </Link>
                  )}
                  {formatAge(b.at)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <TaskDialog
        env={env}
        teammateId={id}
        task={dialog.task}
        open={dialog.open}
        onOpenChange={(next) => setDialog((d) => ({ ...d, open: next }))}
      />
    </div>
  )
}
