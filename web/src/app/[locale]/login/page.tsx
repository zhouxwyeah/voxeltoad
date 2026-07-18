"use client";

import { useActionState } from "react";
import { useTranslations } from "next-intl";
import Image from "next/image";
import { loginAction } from "./actions";
import { Button, Input } from "@/components/ui";

/**
 * Login form (design/frontend.md §3). Client component so it can use
 * useActionState to render the Server Action's inline error. The operator token
 * never touches this component — loginAction stores it server-side.
 */
export default function LoginPage() {
  const tAuth = useTranslations("auth");
  const tErrors = useTranslations("errors");
  const [state, formAction, pending] = useActionState(loginAction, null);

  return (
    <main className="flex min-h-screen items-center justify-center bg-muted px-4">
      <div className="w-full max-w-sm">
        {/* Brand header */}
        <div className="mb-6 flex flex-col items-center gap-3">
          <Image
            src="/logo.svg"
            alt="voxeltoad"
            width={48}
            height={48}
            className="h-12 w-12"
            priority
          />
          <div className="text-center">
            <h1 className="text-xl font-semibold text-foreground">
              {tAuth("heading")}
            </h1>
            <p className="text-sm text-muted-foreground">
              {tAuth("subtitle")}
            </p>
          </div>
        </div>

        {/* Login card */}
        <div className="rounded-lg border border-border bg-background p-6 shadow-sm">
          <form action={formAction} className="flex flex-col gap-4">
            <Input
              label={tAuth("email.label")}
              name="email"
              type="email"
              autoComplete="username"
              required
            />
            <Input
              label={tAuth("password.label")}
              name="password"
              type="password"
              autoComplete="current-password"
              required
            />
            {state && !state.ok && (
              <p
                role="alert"
                className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {state.errorKey
                  ? tErrors(state.errorKey)
                  : state.error}
              </p>
            )}
            <Button type="submit" disabled={pending} className="w-full">
              {pending ? tAuth("signingIn") : tAuth("signIn")}
            </Button>
          </form>
        </div>
      </div>
    </main>
  );
}
