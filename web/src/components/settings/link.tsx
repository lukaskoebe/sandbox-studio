import type { ComponentProps } from "react"
import { Link } from "@tanstack/react-router"

/** The settings dialog's sections, in the order its nav lists them. */
export const settingsSections = [
  "general",
  "providers",
  "git",
  "network",
  "templates",
  "runtime",
] as const

export type SettingsSection = (typeof settingsSections)[number]

export function isSettingsSection(v: unknown): v is SettingsSection {
  return settingsSections.some((s) => s === v)
}

/**
 * Opens a section of the settings dialog over the page on show: the page keeps its address and
 * gains ?settings=<section>, so the dialog can be linked and closes back to the page.
 */
export function SettingsLink({
  section,
  ...props
}: { section: SettingsSection } & Omit<ComponentProps<"a">, "href">) {
  return (
    <Link
      to="."
      search={(prev: Record<string, unknown>) => ({
        ...prev,
        settings: section,
      })}
      {...props}
    />
  )
}
