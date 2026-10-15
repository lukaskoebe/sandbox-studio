import { test } from "./fixtures"

// Scenarios the fake runtime can't drive yet (PLAN.md, N1).

test.fixme("browser: live view frames render and a click lands on the right page coordinates", async () => {
  // Needs a fake browser VM that answers agent-browser commands and streams known frames (N2's mouse fix).
})
