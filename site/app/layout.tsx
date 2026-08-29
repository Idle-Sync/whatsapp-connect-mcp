import type { Metadata } from 'next';
import type { ReactNode } from 'react';

export const metadata: Metadata = {
  metadataBase: new URL('https://whatsapp.idlesync.in'),
  title: {
    default: 'whatsapp-connect-mcp',
    template: '%s · whatsapp-connect-mcp',
  },
  description:
    'Your WhatsApp, readable by your agent. One Go binary on your own machine, and every message it tries to send stops at a gate until you say yes.',
  icons: { icon: '/favicon.svg' },
  openGraph: {
    type: 'website',
    url: 'https://whatsapp.idlesync.in/',
    siteName: 'whatsapp-connect-mcp',
  },
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <head>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="" />
        <link
          rel="stylesheet"
          href="https://fonts.googleapis.com/css2?family=DM+Mono:wght@400;500&family=Familjen+Grotesk:wght@500;600;700&family=Figtree:wght@400;500;600&family=Unbounded:wght@500;600;700&display=swap"
        />
      </head>
      <body>{children}</body>
    </html>
  );
}
