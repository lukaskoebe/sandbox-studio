/** "1 of 2" beside a dialog's title, for a dialog that takes more than one step. */
export function StepCount({ children }: { children: string }) {
  return (
    <span className="ml-2 font-mono text-caption font-normal text-muted-foreground">
      {children}
    </span>
  )
}
