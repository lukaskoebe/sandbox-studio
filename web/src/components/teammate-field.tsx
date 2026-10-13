import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { $api } from "@/lib/api/client"

const none = "none"

/** Picks the teammate that will own a new sandbox; "" means none. */
export function TeammateField({
  env,
  id,
  value,
  onChange,
  disabled,
}: {
  env: string
  id: string
  value: string
  onChange: (teammateId: string) => void
  disabled?: boolean
}) {
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const list = teammates.data ?? []

  return (
    <Field>
      <FieldLabel htmlFor={id}>Teammate</FieldLabel>
      <Select
        value={value || none}
        disabled={disabled}
        onValueChange={(v) => onChange(!v || v === none ? "" : v)}
        items={{
          [none]: "None",
          ...Object.fromEntries(list.map((p) => [p.id, p.name])),
        }}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={none}>None</SelectItem>
          {list.map((p) => (
            <SelectItem key={p.id} value={p.id}>
              <TeammateAvatar
                name={p.name}
                avatar={p.avatar}
                className="size-4 text-[0.5rem]"
              />
              {p.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <FieldDescription>
        {list.length
          ? "The agent identity working in the sandbox. It can't change later."
          : "Add teammates on the Teammates page to give the sandbox an owner."}
      </FieldDescription>
    </Field>
  )
}
