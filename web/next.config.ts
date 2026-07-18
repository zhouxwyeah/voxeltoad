import path from "node:path";
import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin();

const nextConfig: NextConfig = {
  // The admin SDK is a file: dependency symlinked from ../sdk/typescript (outside
  // web/). Turbopack needs the workspace root widened to that ancestor to follow
  // the symlink, and the package transpiled so its subpath export ("/admin")
  // resolves. Without these, `@voxeltoad/gateway-sdk/admin` is not found.
  turbopack: {
    root: path.join(__dirname, ".."),
  },
  transpilePackages: ["@voxeltoad/gateway-sdk"],
};

export default withNextIntl(nextConfig);
