/**
 * Whether this page load shows the design mockups (N4). Dev builds only: the mockups are
 * static and need no Studio, so the auth gate and the app's live hooks stay off for them.
 */
export const mockupsActive =
  import.meta.env.DEV && window.location.pathname.startsWith("/mockups")
