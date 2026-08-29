import fs from 'node:fs';
import path from 'node:path';
import Script from 'next/script';
import '@/styles/landing.css';

// The landing page markup lives as plain HTML so the story and its three
// interactions stay one editable piece, rather than being chopped into
// components that only ever render in one order.
//
// This is read from our own repo at build time and never from user input, so
// there is nothing here to sanitise — it is the same trust level as JSX we wrote.
const body = fs.readFileSync(
  path.join(process.cwd(), 'lib/landing-body.html'),
  'utf8'
);

export default function Home() {
  return (
    <>
      <div dangerouslySetInnerHTML={{ __html: body }} />
      <Script src="/landing.js" strategy="afterInteractive" />
    </>
  );
}
