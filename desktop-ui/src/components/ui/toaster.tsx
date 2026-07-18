import { Toaster as SonnerToaster } from "sonner";

// Toaster — sonner host, mirroring web's src/components/ui/toaster.tsx
// (bottom-right, close button, token-bound colors). Mount once at the app root.
export function Toaster() {
  return (
    <SonnerToaster
      position="bottom-right"
      closeButton
      toastOptions={{
        style: {
          background: "var(--background)",
          color: "var(--foreground)",
          border: "1px solid var(--border)",
        },
      }}
    />
  );
}
