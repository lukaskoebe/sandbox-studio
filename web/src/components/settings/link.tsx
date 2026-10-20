import type { ComponentProps } from "react"
import { Link, useParams } from "@tanstack/react-router"

/**
 * The Settings page's sections, in the order its nav lists them: what belongs to this
 * environment, the accounts Studio uses, and this computer. The keywords are what the nav's
 * search finds a section by besides its name.
 */
export const settingsGroups = [
  {
    title: "This environment",
    sections: [
      {
        id: "general",
        label: "General",
        keywords: "start at login setup environment file export import",
      },
      {
        id: "network",
        label: "Network",
        keywords: "rules level security hosts allow deny internet",
      },
      {
        id: "secrets",
        label: "Secrets",
        keywords: "keys tokens vault credentials passwords",
      },
      {
        id: "memory",
        label: "Shared memory",
        keywords: "facts pages conflicts knowledge",
      },
      {
        id: "activity",
        label: "Activity",
        keywords: "log history decisions pushes reviews requests",
      },
    ],
  },
  {
    title: "Accounts",
    sections: [
      {
        id: "providers",
        label: "AI providers",
        keywords: "anthropic openai api key models",
      },
      {
        id: "forges",
        label: "Git forges",
        keywords: "github gitlab forgejo codeberg ssh token folder",
      },
    ],
  },
  {
    title: "This computer",
    sections: [
      {
        id: "templates",
        label: "Templates",
        keywords: "builds images dockerfile",
      },
      {
        id: "runtime",
        label: "Runtime",
        keywords: "microsandbox vm cpu memory disk",
      },
      {
        id: "ports",
        label: "Ports and LAN",
        keywords: "host ports local network vpn private addresses",
      },
      { id: "updates", label: "Updates", keywords: "version release" },
    ],
  },
] as const

export type SettingsSection =
  (typeof settingsGroups)[number]["sections"][number]["id"]

/** Each section's address. */
export const settingsPaths = {
  general: "/e/$env/settings/general",
  network: "/e/$env/settings/network",
  secrets: "/e/$env/settings/secrets",
  memory: "/e/$env/settings/memory",
  activity: "/e/$env/settings/activity",
  providers: "/e/$env/settings/providers",
  forges: "/e/$env/settings/forges",
  templates: "/e/$env/settings/templates",
  runtime: "/e/$env/settings/runtime",
  ports: "/e/$env/settings/ports",
  updates: "/e/$env/settings/updates",
} as const satisfies Record<SettingsSection, string>

/**
 * The section an old ?settings= link meant: the settings dialog's Git was the forges, and its
 * Network this computer's ports and the local network list.
 */
export function oldSettingsSection(v: unknown): SettingsSection | undefined {
  if (v === "git") return "forges"
  if (v === "network") return "ports"
  return typeof v === "string" && v in settingsPaths
    ? (v as SettingsSection)
    : undefined
}

/** Opens a section of the Settings page of the environment on show. */
export function SettingsLink({
  section,
  ...props
}: { section: SettingsSection } & Omit<ComponentProps<"a">, "href">) {
  const { env } = useParams({ strict: false })
  return (
    <Link to={settingsPaths[section]} params={{ env: env ?? "" }} {...props} />
  )
}
