import createMiddleware from "next-intl/middleware";
import { routing } from "./i18n/routing";

export default createMiddleware(routing);

export const config = {
  // Match all URLs except for static files, API routes, etc.
  matcher: ["/((?!_next|_vercel|.*\\..*).*)"],
};
