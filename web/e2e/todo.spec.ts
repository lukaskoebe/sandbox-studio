import { test } from "./fixtures"

// Scenarios the fake runtime can't drive yet (PLAN.md, N1).

test.fixme("git review: a push to the virtual remote is approved and reaches the upstream", async () => {
  // Needs a fake guest that pushes through the virtual git host to a local bare repository.
})

test.fixme("chat: a scripted ACP agent answers, asks for a permission and gets it", async () => {
  // Needs a fake guest that serves a scripted ACP agent on the acp stream instead of a harness.
})

test.fixme("browser: live view frames render and a click lands on the right page coordinates", async () => {
  // Needs a fake browser VM that answers agent-browser commands and streams known frames (N2's mouse fix).
})
