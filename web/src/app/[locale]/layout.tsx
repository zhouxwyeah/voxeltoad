import { NextIntlClientProvider } from "next-intl";
import { getMessages } from "next-intl/server";

/**
 * Locale layout — provides the next-intl client context. The <html>/<body>
 * tags live in the root layout (Next.js 16 requirement); this layer only
 * wires the message catalog into the client tree.
 */
export default async function LocaleLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const messages = await getMessages();

  return (
    <NextIntlClientProvider messages={messages}>
      {children}
    </NextIntlClientProvider>
  );
}
