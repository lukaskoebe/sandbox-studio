import type { ComponentProps } from "react"
import { ColumnPage } from "@/components/column-page"

/** A settings section. The Settings nav has the sidebar's toggle on phones, so it does not. */
export function SettingsPage(
  props: Omit<ComponentProps<typeof ColumnPage>, "toggle" | "narrow">
) {
  return <ColumnPage {...props} toggle={false} />
}
