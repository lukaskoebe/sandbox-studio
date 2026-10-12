import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { RouterProvider } from "@tanstack/react-router"
import { AuthGate } from "./components/auth-gate"
import { bootstrapAuth } from "./lib/auth"
import { createRouter } from "./router"
import { followSystemTheme } from "./lib/theme"
import { mockupsActive } from "./mockups/active"
import "./styles.css"

followSystemTheme()

// Consume and scrub a launch token before constructing anything that can mount
// route loaders. The promise also exchanges it before checking the cookie.
// The dev-only design mockups are static and skip the login.
const authBootstrap = mockupsActive ? null : bootstrapAuth()

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 1000, retry: 1 } },
})
const router = createRouter(queryClient)

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      {authBootstrap ? (
        <AuthGate bootstrap={authBootstrap}>
          <RouterProvider router={router} />
        </AuthGate>
      ) : (
        <RouterProvider router={router} />
      )}
    </QueryClientProvider>
  </StrictMode>
)
