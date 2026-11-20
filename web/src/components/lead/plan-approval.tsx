import { Link } from "@tanstack/react-router"
import type { Approval } from "@/lib/api/client"
import { sendingSteps } from "@/lib/lead"
import { PlanDecision } from "./plan-page"

/**
 * A plan of Lead's on an approval card: each message as it would be sent, a link to the plan,
 * and Approve or Don't send. Approving sends these messages and nothing else.
 */
export function LeadPlanSummary({ approval }: { approval: Approval }) {
  const p = approval.leadPlan
  if (!p) return null
  const env = approval.environmentId
  const steps = sendingSteps(p.steps)
  return (
    <div className="grid min-w-0 gap-2 text-xs">
      <ol className="grid gap-1.5" aria-label="Messages">
        {steps.map((st, i) => (
          <li key={i} className="grid min-w-0 gap-0.5">
            <span className="truncate text-muted-foreground">
              {st.kind === "start" ? "New thread for " : "To "}
              <span className="font-medium text-foreground">{st.teammate}</span>
              {" · "}
              {st.thread}
            </span>
            <span className="line-clamp-3 break-words whitespace-pre-wrap">
              {st.text}
            </span>
          </li>
        ))}
      </ol>
      <Link
        to="/e/$env/lead/plans/$plan"
        params={{ env, plan: p.planId }}
        className="justify-self-start text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
      >
        Open the plan
      </Link>
      <PlanDecision env={env} approvalId={approval.id} name={p.lead} compact />
    </div>
  )
}
