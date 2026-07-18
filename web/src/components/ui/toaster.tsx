"use client"

import { Toaster as SonnerToaster } from "sonner"

/**
 * Toaster primitive (design-system.md §6, §8).
 *
 * Global toast host. Mounted once in the dashboard layout. Styles bind to the
 * design tokens in globals.css (success/warning/destructive) rather than
 * sonner's defaults, so toasts match the rest of the console. Use the `toast`
 * helper from `@/lib/toast` to fire toasts from any client component.
 */
export function Toaster() {
  return (
    <SonnerToaster
      position="bottom-right"
      richColors={false}
      closeButton
      toastOptions={{
        classNames: {
          toast:
            "group toast group-[.toaster]:border-border group-[.toaster]:bg-background group-[.toaster]:text-foreground group-[.toaster]:shadow-lg",
          description: "text-muted-foreground",
          actionButton: "bg-primary text-primary-foreground",
          cancelButton: "bg-muted text-muted-foreground",
        },
      }}
    />
  )
}
