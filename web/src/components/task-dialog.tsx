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
import { Textarea } from "@/components/ui/textarea"
import { $api, errorMessage, errorStatus } from "@/lib/api/client"
import { maxTaskDetailsBytes, taskStatusLabels, type Task } from "@/lib/tasks"

/** Limit of a task's title in characters, as the server counts them. */
const maxTitleChars = 200

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
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{task ? "Edit task" : "New task"}</DialogTitle>
          <DialogDescription>
            {task
              ? "Its details are markdown. Done tasks stay in the list."
              : "A task the teammate works on in its check-ins, or when you ask it to."}
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
  const [formError, setFormError] = useState<string>()

  const titleChars = Array.from(title.trim()).length
  const detailsBytes = new TextEncoder().encode(details).length
  const valid =
    titleChars > 0 &&
    titleChars <= maxTitleChars &&
    detailsBytes <= maxTaskDetailsBytes

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
