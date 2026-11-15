import type { ComponentProps } from "react"
import { ColumnPage } from "@/components/column-page"

/** A settings section, in the Settings page's column. */
export function SettingsPage(
  props: Omit<ComponentProps<typeof ColumnPage>, "narrow">
) {
  return <ColumnPage {...props} />
}
