import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { $api, errorMessage, type Setup } from "@/lib/api/client"

/** The seven steps of the guided setup, in the order of the wizard (PLAN N5). */
export const setupSteps = [
  { key: "doctor", title: "Host check" },
  { key: "workMode", title: "Work mode" },
  { key: "provider", title: "AI provider" },
  { key: "teammate", title: "Teammate" },
  { key: "git", title: "Git" },
  { key: "network", title: "Network" },
  { key: "sandbox", title: "First sandbox" },
] as const

export type SetupKey = (typeof setupSteps)[number]["key"]

/** The steps as the API keeps them: "done" or "skipped", or undefined while still to do. */
export type SetupSteps = Setup["steps"]

/** Whether the setup is over for good: every step is done or skipped, or it was dismissed. */
export function setupOver(setup: Setup) {
  return !!setup.completedAt || setup.dismissed
}

/** How many steps are done, including the ones whose condition already holds. */
export function doneSteps(setup: Setup) {
  return setupSteps.filter((s) => setup.steps[s.key] === "done").length
}

/** The index of the first step with no outcome yet, or -1 when every step has one. */
export function firstOpenStep(setup: Setup) {
  return setupSteps.findIndex((s) => !setup.steps[s.key])
}

const routedKey = "studio.setup.routed"

/**
 * Whether a first load of the app opens the setup: once per browser session, while the setup
 * is neither completed nor dismissed and the environment has no sandbox yet.
 */
export async function setupOnFirstLoad(queryClient: QueryClient, env: string) {
  try {
    if (sessionStorage.getItem(routedKey)) return false
    sessionStorage.setItem(routedKey, "1")
    const setup = await queryClient.ensureQueryData(
      $api.queryOptions("get", "/api/setup", { params: { query: { env } } })
    )
    return !setupOver(setup) && setup.steps.sandbox !== "done"
  } catch {
    // Storage can be unavailable, and the API may not answer yet; the app opens as usual.
    return false
  }
}

/**
 * The guided setup of an environment, and a way to record its steps. Every change sends the
 * whole set of steps as the page shows them, so a step done by its condition is recorded too.
 */
export function useSetup(env: string) {
  const queryClient = useQueryClient()
  const query = $api.useQuery("get", "/api/setup", {
    params: { query: { env } },
  })
  const update = $api.useMutation("put", "/api/setup", {
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] }),
    onError: (err) =>
      toast.error("Could not save the setup", {
        description: errorMessage(err),
      }),
  })
  return {
    setup: query.data,
    isPending: query.isPending,
    error: query.error,
    saving: update.isPending,
    record: (steps: SetupSteps, dismissed = query.data?.dismissed ?? false) =>
      update.mutate({ params: { query: { env } }, body: { steps, dismissed } }),
  }
}
