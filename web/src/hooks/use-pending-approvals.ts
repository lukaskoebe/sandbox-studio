import { $api } from "@/lib/api/client"

/**
 * The pending approvals of every environment, loaded as the approval stack and presence load
 * them, so a page that counts them by sandbox shares the same request.
 */
export function usePendingApprovals() {
  return $api.useQuery(
    "get",
    "/api/approvals",
    { params: { query: { status: "pending" } } },
    { refetchInterval: 15000 }
  )
}
