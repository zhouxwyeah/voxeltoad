import type { Metadata } from "next";
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import { getLocale } from "next-intl/server";
import "./globals.css";

export const metadata: Metadata = {
  title: "Control Panel — voxeltoad",
  description: "Management console for voxeltoad",
};

/**
 * Root layout — must render <html> and <body> (Next.js 16 requirement).
 * The locale-aware lang is resolved here via next-intl's getLocale(); the
 * NextIntlClientProvider lives in [locale]/layout.tsx so it can read messages
 * from the locale segment.
 *
 * Fonts use the `geist` npm package (local font files) instead of
 * `next/font/google` to eliminate build-time network dependency on
 * fonts.gstatic.com (which is unreachable in some environments).
 */
export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const locale = await getLocale();

  return (
    <html
      lang={locale}
      className={`${GeistSans.variable} ${GeistMono.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}
