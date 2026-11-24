import type { ComponentProps } from "react"
import { Link, useParams } from "@tanstack/react-router"

/**
 * The environment's own pages, which the sidebar lists under Environment: used every day, and
 * Network can have requests waiting. The keywords are what search finds a page by besides its
 * name.
 */
export const environmentPages = [
  {
    id: "network",
    label: "Network",
    keywords:
      "rules level security hosts allow deny internet requests connections",
  },
  {
    id: "secrets",
    label: "Secrets",
    keywords: "keys tokens vault credentials passwords",
  },
  {
    id: "memory",
    label: "Memory",
    keywords: "shared facts pages conflicts knowledge",
  },
  {
    id: "activity",
    label: "Activity",
    keywords: "log history decisions pushes reviews requests",
  },
] as const

/**
 * The Settings page's sections, grouped by what they apply to: all of Studio on this machine,
 * the environment on show, and this browser. The keywords are what the palette finds a section
 * by besides its name.
 */
export const settingsGroups = [
  {
    title: "Studio",
    scope: "studio",
    sections: [
      {
        id: "general",
        label: "General",
        keywords: "start at login ssh previews terminal shell steps setup tips",
      },
      { id: "updates", label: "Updates", keywords: "version release" },
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
    ],
  },
  {
    title: "Environment",
    scope: "environment",
    sections: [
      {
        id: "environment",
        label: "Name and file",
        keywords: "environment rename export import file delete",
      },
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
    title: "This browser",
    scope: "browser",
    sections: [
      {
        id: "appearance",
        label: "Appearance",
        keywords: "style pixel quiet text size light dark theme",
      },
      {
        id: "notifications",
        label: "Notifications",
        keywords: "notify alerts held connections",
      },
    ],
  },
] as const

/** A Settings section, or one of the environment's pages that used to be one. */
export type SettingsSection =
  | (typeof settingsGroups)[number]["sections"][number]["id"]
  | (typeof environmentPages)[number]["id"]

/** Each section's and environment page's address. */
export const settingsPaths = {
  general: "/e/$env/settings/general",
  environment: "/e/$env/settings/environment",
  appearance: "/e/$env/settings/appearance",
  notifications: "/e/$env/settings/notifications",
  network: "/e/$env/network",
  secrets: "/e/$env/secrets",
  memory: "/e/$env/memory",
  activity: "/e/$env/activity",
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

/** Opens a section of the Settings page, or an environment page, of the environment on show. */
export function SettingsLink({
  section,
  ...props
}: { section: SettingsSection } & Omit<ComponentProps<"a">, "href">) {
  const { env } = useParams({ strict: false })
  return (
    <Link to={settingsPaths[section]} params={{ env: env ?? "" }} {...props} />
  )
}
