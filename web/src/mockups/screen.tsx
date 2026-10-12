import type { ComponentType } from "react"
import { WorkbenchApprovals } from "./a/approvals"
import { WorkbenchHome } from "./a/home"
import { WorkbenchDashboard, WorkbenchWorkspace } from "./a/sandbox"
import { CalmApprovals } from "./b/approvals"
import { CalmHome } from "./b/home"
import { CalmDashboard, CalmWorkspace } from "./b/sandbox"
import { MockupPage, type Direction, type Screen } from "./shared"

const directions: Record<Direction, Record<Screen, ComponentType>> = {
  a: {
    home: WorkbenchHome,
    workspace: WorkbenchWorkspace,
    dashboard: WorkbenchDashboard,
    approvals: WorkbenchApprovals,
  },
  b: {
    home: CalmHome,
    workspace: CalmWorkspace,
    dashboard: CalmDashboard,
    approvals: CalmApprovals,
  },
}

export function MockupScreen({
  direction,
  screen,
}: {
  direction: Direction
  screen: Screen
}) {
  const Page = directions[direction][screen]
  return (
    <MockupPage direction={direction} screen={screen}>
      <Page />
    </MockupPage>
  )
}
