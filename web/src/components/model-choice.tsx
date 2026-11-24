import { knownModels } from "@/components/model-input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { Harness, Provider } from "@/lib/api/client"
import { harnessLabels, isSubscription } from "@/lib/teammates"

/** What a teammate runs on: a provider, the agent there, and its model ("" for the provider's own). */
export type ModelPick = { providerId: string; harness: Harness; model: string }

type Choice = ModelPick & { key: string; model: string; label: string }

const keyOf = (p: ModelPick) => `${p.providerId}/${p.harness}/${p.model}`

/** A provider's models for an agent: the known ones, and its own default where it has one. */
function modelsFor(p: Provider, h: Harness): string[] {
  if (p.kind === "openai_compatible") return [""]
  // Claude Code's family names (sonnet, opus) follow the ids; the settings take them.
  const known = knownModels(p.kind, h).filter((m) => m.includes("-"))
  return isSubscription(p.kind) ? ["", ...known] : known
}

function modelLabel(p: Provider, model: string) {
  if (model) return model
  return p.kind === "openai_compatible"
    ? (p.model ?? "The endpoint's model")
    : "The plan's default"
}

/** Every model a new teammate can start on, by provider and agent, in the providers' order. */
export function modelChoices(providers: Provider[]) {
  return providers.flatMap((p) =>
    (p.harnesses ?? []).map((h) => ({
      label: `${p.name} · ${harnessLabels[h]}`,
      choices: modelsFor(p, h).map((model): Choice => ({
        providerId: p.id,
        harness: h,
        model,
        key: keyOf({ providerId: p.id, harness: h, model }),
        label: `${modelLabel(p, model)} · ${harnessLabels[h]} · ${p.name}`,
      })),
    }))
  )
}

/**
 * The first provider's usual agent and model, as the server's default teammate picks them: the
 * kind's first agent and its first model.
 */
export function firstModel(providers: Provider[]): ModelPick | undefined {
  return modelChoices(providers)[0]?.choices[0]
}

/**
 * One menu for the provider, agent and model: each provider and agent is a group of its
 * models, and the closed menu says all three, such as "claude-sonnet-5-5 · Claude Code ·
 * Anthropic". Any other model, and the models a chat may switch to, are in the settings.
 */
export function ModelChoice({
  id,
  providers,
  value,
  onChange,
}: {
  id?: string
  providers: Provider[]
  value: ModelPick | undefined
  onChange: (pick: ModelPick) => void
}) {
  const groups = modelChoices(providers)
  const all = groups.flatMap((g) => g.choices)
  return (
    <Select
      value={value ? keyOf(value) : null}
      onValueChange={(v) => {
        const c = all.find((x) => x.key === v)
        if (c)
          onChange({
            providerId: c.providerId,
            harness: c.harness,
            model: c.model,
          })
      }}
      items={Object.fromEntries(all.map((c) => [c.key, c.label]))}
    >
      <SelectTrigger id={id} className="w-full">
        <SelectValue placeholder="Choose a model" />
      </SelectTrigger>
      <SelectContent>
        {groups.map((g) => (
          <SelectGroup key={g.label}>
            <SelectLabel>{g.label}</SelectLabel>
            {g.choices.map((c) => (
              <SelectItem key={c.key} value={c.key}>
                {c.label.split(" · ")[0]}
              </SelectItem>
            ))}
          </SelectGroup>
        ))}
      </SelectContent>
    </Select>
  )
}
