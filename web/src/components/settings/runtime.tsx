import { RuntimeSection } from "@/components/runtime-card"
import { SettingsPage } from "@/components/settings/page"

export function RuntimeSettings() {
  return (
    <SettingsPage title="Runtime">
      <RuntimeSection />
    </SettingsPage>
  )
}
