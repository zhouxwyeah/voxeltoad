import { type NextRequest, NextResponse } from "next/server";
import { clearSession } from "@/lib/session";

/**
 * Logout Route Handler. Clears the encrypted session cookie and bounces to
 * /login. Used as the redirect target from RSC renders that hit a back-end
 * 401 — RSCs cannot modify cookies (only Server Actions and Route Handlers
 * can), so onAuthExpired redirects here instead of calling clearSession()
 * directly (design/frontend.md §4, ADR-0020).
 *
 * The user-initiated "Sign out" button still goes through the logoutAction
 * Server Action (cookie modification is allowed there); this route is for the
 * programmatic clear-and-bounce path.
 */
export async function GET(req: NextRequest): Promise<NextResponse> {
  await clearSession();
  return NextResponse.redirect(new URL("/login", req.url));
}
