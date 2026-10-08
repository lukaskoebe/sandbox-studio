/** Follows the system light/dark preference. */
export function followSystemTheme() {
  const media = window.matchMedia("(prefers-color-scheme: dark)")
  const apply = () =>
    document.documentElement.classList.toggle("dark", media.matches)
  apply()
  media.addEventListener("change", apply)
}
