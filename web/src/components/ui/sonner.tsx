"use client"

import { Toaster as Sonner, type ToasterProps } from "sonner"
import {
  CheckCircleIcon,
  InfoIcon,
  WarningIcon,
  XCircleIcon,
  SpinnerIcon,
} from "@phosphor-icons/react"

import { useThemeMode } from "@/lib/theme"

const Toaster = ({ ...props }: ToasterProps) => {
  // Sonner colours a toast's text by its own theme: "system" follows the OS, so dark picked in
  // Studio on a light OS put light-theme grey on the dark popover.
  const theme = useThemeMode()
  return (
    <Sonner
      theme={theme}
      className="toaster group"
      icons={{
        success: <CheckCircleIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <WarningIcon className="size-4" />,
        error: <XCircleIcon className="size-4" />,
        loading: <SpinnerIcon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
          // The close button sits on the top right corner, where a dialog's is.
          "--toast-close-button-start": "unset",
          "--toast-close-button-end": "0",
          "--toast-close-button-transform": "translate(35%, -35%)",
        } as React.CSSProperties
      }
      toastOptions={{
        classNames: {
          toast: "cn-toast",
          description: "text-muted-foreground!",
          closeButton: "rounded-full!",
        },
      }}
      {...props}
    />
  )
}

export { Toaster }
