import {
  Link,
  useRouter,
  type ErrorComponentProps,
} from "@tanstack/react-router"
import { ErrorNotice } from "@/components/error-boundary"
import { WindowBar } from "@/components/window-bar"

/**
 * A page that failed: what went wrong and the way on, in place of the page alone. `bar` gives
 * it the window's bar, for a route that draws the bar itself, since the app's window has no
 * other way to be moved or closed.
 */
export function RouteError({
  error,
  reset,
  bar,
}: ErrorComponentProps & { bar?: boolean }) {
  const router = useRouter()
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {bar && <WindowBar />}
      <ErrorNotice
        error={error}
        label="this page"
        onRetry={() => {
          reset()
          void router.invalidate()
        }}
      />
      {/* The app has no address bar, so this is the way out. */}
      <p className="pb-6 text-center text-xs">
        <Link to="/" className="underline underline-offset-4">
          Back to Studio
        </Link>
      </p>
    </div>
  )
}
