import { useEffect, useId, useSyncExternalStore } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { $api, errorMessage, type Onboarding } from "@/lib/api/client"

export type FeatureKey = Onboarding["features"][number]["key"]

/** The features Home's Get to know Studio card shows, in its order. */
export const onboardingFeatures: {
  key: FeatureKey
  title: string
  short: string
}[] = [
  {
    key: "network",
    title: "Network",
    short: "You decide what leaves the sandbox.",
  },
  {
    key: "push",
    title: "Pushes and PRs",
    short: "Every push waits for your review.",
  },
  { key: "browser", title: "Browser", short: "Watch it, or take the wheel." },
  { key: "preview", title: "Previews", short: "Its app, one click away." },
  { key: "secret", title: "Secrets", short: "Keys it uses but never reads." },
]

/** Whether a feature is ticked off on the card: it happened, or its tip was closed. */
export function featureDone(o: Onboarding, key: FeatureKey) {
  const f = o.features.find((x) => x.key === key)
  return !!f && (f.happened || f.seen)
}

/**
 * The onboarding of an environment (docs/frontend.md#onboarding), and ways to record it. Each
 * change sends the whole stored state as the page knows it.
 */
export function useOnboarding(env: string | undefined) {
  const queryClient = useQueryClient()
  const query = $api.useQuery(
    "get",
    "/api/onboarding",
    { params: { query: { env: env ?? "" } } },
    // Short: features happen elsewhere, and every tip's control reads this.
    { enabled: !!env, staleTime: 5_000 }
  )
  const update = $api.useMutation("put", "/api/onboarding", {
    onSuccess: (data) => {
      queryClient.setQueryData(
        ["get", "/api/onboarding", { params: { query: { env } } }],
        data
      )
      void queryClient.invalidateQueries({
        queryKey: ["get", "/api/onboarding"],
      })
    },
    onError: (err) =>
      toast.error("Could not save the tips", {
        description: errorMessage(err),
      }),
  })
  const o = query.data
  const save = (
    change: Partial<{
      seen: FeatureKey[]
      dismissed: boolean
      tipsOff: boolean
    }>
  ) => {
    if (!env || !o) return
    update.mutate({
      params: { query: { env } },
      body: {
        seen: change.seen ?? o.features.filter((f) => f.seen).map((f) => f.key),
        dismissed: change.dismissed ?? o.dismissed,
        tipsOff: change.tipsOff ?? o.tipsOff,
      },
    })
  }
  const seenOf = () =>
    (o?.features ?? []).filter((f) => f.seen).map((f) => f.key)
  return {
    onboarding: o,
    saving: update.isPending,
    /** Records that the user closed the feature's tip, or met the feature where it lives. */
    see: (key: FeatureKey) => {
      // An effect may ask again before the answer is back.
      if (!update.isPending && !seenOf().includes(key))
        save({ seen: [...seenOf(), key] })
    },
    stopTips: () => save({ tipsOff: true }),
    dismiss: () => save({ dismissed: true }),
    /** Shows every tip again, and the card for what is left. */
    restart: () => save({ seen: [], dismissed: false, tipsOff: false }),
  }
}

// One tip shows at a time: the tips that would show line up, and the first one shows. A tip
// leaves the line when it closes or its control goes, and the next one shows.
let line: { id: string; key: FeatureKey }[] = []
const listeners = new Set<() => void>()
const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => listeners.delete(l)
}
const changed = () => listeners.forEach((l) => l())
const first = () => line[0]?.id

/**
 * Whether the feature's tip shows at this control: while active, until its tip is closed or the
 * tips are stopped, and when no other tip shows. A tip of the same feature at two controls shows
 * at the one that came first.
 */
export function useFeatureTip(
  env: string | undefined,
  key: FeatureKey,
  active: boolean
) {
  const id = useId()
  const { onboarding, see, stopTips } = useOnboarding(env)
  const wanted =
    active &&
    !!onboarding &&
    !onboarding.tipsOff &&
    !onboarding.features.find((f) => f.key === key)?.seen
  useEffect(() => {
    if (!wanted) return
    line = [...line, { id, key }]
    changed()
    return () => {
      line = line.filter((t) => t.id !== id)
      changed()
    }
  }, [wanted, id, key])
  const shown = useSyncExternalStore(subscribe, first, first) === id
  return {
    show: wanted && shown,
    /** Closes the tip for good. */
    close: () => see(key),
    stopTips,
  }
}
