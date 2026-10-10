import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { PersonaAvatar } from "@/components/persona-avatar"
import { $api } from "@/lib/api/client"

const none = "none"

/** Picks the persona that will own a new sandbox; "" means none. */
export function PersonaField({
  env,
  id,
  value,
  onChange,
  disabled,
}: {
  env: string
  id: string
  value: string
  onChange: (personaId: string) => void
  disabled?: boolean
}) {
  const personas = $api.useQuery("get", "/api/environments/{env}/personas", {
    params: { path: { env } },
  })
  const list = personas.data ?? []

  return (
    <Field>
      <FieldLabel htmlFor={id}>Persona</FieldLabel>
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
              <PersonaAvatar name={p.name} className="size-4 text-[0.5rem]" />
              {p.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <FieldDescription>
        {list.length
          ? "The agent identity working in the sandbox. It can't change later."
          : "Add personas on the Personas page to give the sandbox an owner."}
      </FieldDescription>
    </Field>
  )
}
