import { Component, type ErrorInfo, type ReactNode } from "react"
import { ArrowClockwiseIcon, WarningIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/**
 * Whether an error is a part of the app that didn't load: a script or a style the page asks
 * for after it started. After an update, the page still names the files of the version it
 * loaded with, which are gone; trying again asks for the same file, so only a reload helps.
 * Each browser words it its own way.
 */
export function isLoadError(error: unknown) {
  const message =
    error instanceof Error ? `${error.name} ${error.message}` : String(error)
  return /dynamically imported module|module script|MIME type|Unable to preload|Loading (CSS )?chunk|ChunkLoadError/i.test(
    message
  )
}

/** What a part that failed shows in its place: what went wrong, and the way on. */
export function ErrorNotice({
  error,
  label,
  onRetry,
  className,
}: {
  error: unknown
  /** What failed to show, as "the changes". */
  label?: string
  /** Shows the part again; absent when only a reload can help. */
  onRetry?: () => void
  className?: string
}) {
  const load = isLoadError(error)
  const message = error instanceof Error ? error.message : String(error)
  return (
    <div
      role="alert"
      className={cn(
        "flex min-h-0 flex-1 items-center justify-center p-6",
        className
      )}
    >
      <div className="grid max-w-md gap-3 border bg-card p-4 text-xs">
        <p className="flex items-center gap-2 text-sm font-medium">
          <WarningIcon className="size-4 shrink-0 text-amber-600 dark:text-amber-400" />
          {label ? `Couldn't show ${label}` : "Something went wrong"}
        </p>
        <p className="text-muted-foreground">
          {load
            ? "Studio was updated since this window loaded, so a part of the old version is gone. Reload to get the new one."
            : "The rest of Studio still works."}
        </p>
        {message && (
          <p className="max-h-24 overflow-auto font-mono text-caption wrap-anywhere text-muted-foreground">
            {message}
          </p>
        )}
        <div className="flex gap-2">
          {!load && onRetry && (
            <Button size="sm" variant="outline" onClick={onRetry}>
              Try again
            </Button>
          )}
          <Button
            size="sm"
            variant={load ? "default" : "ghost"}
            onClick={() => window.location.reload()}
          >
            <ArrowClockwiseIcon />
            Reload
          </Button>
        </div>
      </div>
    </div>
  )
}

/**
 * Keeps an error in one part of the page to that part: it shows ErrorNotice in its place,
 * and the rest of Studio stays. A change of `resetKey`, such as another file, tries again.
 */
export class ErrorBoundary extends Component<
  {
    children: ReactNode
    /** What it shows, as "the changes", for the notice. */
    label?: string
    resetKey?: unknown
    className?: string
  },
  { error: unknown; key: unknown; failed: boolean }
> {
  state = { error: undefined, key: this.props.resetKey, failed: false }

  static getDerivedStateFromError(error: unknown) {
    return { error, failed: true }
  }

  static getDerivedStateFromProps(
    props: { resetKey?: unknown },
    state: { key: unknown; failed: boolean }
  ) {
    if (props.resetKey === state.key) return null
    return { key: props.resetKey, failed: false, error: undefined }
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error(error, info.componentStack)
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <ErrorNotice
        error={this.state.error}
        label={this.props.label}
        className={this.props.className}
        onRetry={() => this.setState({ failed: false, error: undefined })}
      />
    )
  }
}
