import { createFileRoute, notFound } from "@tanstack/react-router"
import { BackToSetup } from "@/components/back-to-setup"
import { TeammatePage, type SearchChange } from "@/components/teammate/page"
import { $api } from "@/lib/api/client"
import { teammateSearch } from "@/lib/computer"

export const Route = createFileRoute("/e/$env/teammates/$id")({
  validateSearch: teammateSearch,
  loader: async ({ context, params }) => {
    const teammates = await context.queryClient.ensureQueryData(
      $api.queryOptions("get", "/api/environments/{env}/teammates", {
        params: { path: { env: params.env } },
      })
    )
    if (!teammates?.some((p) => p.id === params.id)) throw notFound()
  },
  component: Teammate,
})

/** A teammate and its computer; what the page shows is all in the URL's search. */
function Teammate() {
  const { env, id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const teammate = teammates.data?.find((p) => p.id === id)
  if (!teammate) return null
  const onSearch = (change: SearchChange, push: boolean | "quiet" = false) =>
    void navigate({
      search: (prev) =>
        teammateSearch(
          Object.fromEntries(
            Object.entries({ ...prev, ...change }).filter(
              ([, v]) => v !== undefined
            )
          )
        ),
      replace: !push,
      viewTransition: push === true,
    })
  return (
    <>
      {/* The guided setup's first computer opens here; its settings sheet has its own way back. */}
      {search.from === "setup" && !search.profile && (
        <div className="shrink-0 px-4 pt-3">
          <BackToSetup step="sandbox" />
        </div>
      )}
      <TeammatePage
        key={id}
        env={env}
        teammate={teammate}
        search={search}
        onSearch={onSearch}
      />
    </>
  )
}
