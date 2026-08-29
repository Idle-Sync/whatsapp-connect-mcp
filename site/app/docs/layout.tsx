import Link from 'next/link';
import type { ReactNode } from 'react';
import { NAV, href } from '@/lib/nav';
import '@/styles/docs.css';

export default function DocsLayout({ children }: { children: ReactNode }) {
  return (
    <div className="docs-shell">
      <header className="docs-top">
        <Link className="brand" href="/">
          <svg width="20" height="20" viewBox="0 0 32 32" aria-hidden="true">
            <rect width="32" height="32" rx="7" fill="#0c0e13" />
            <path d="M7 16h7.5" stroke="#6a80ff" strokeWidth="2.4" strokeLinecap="round" />
            <path d="M18.6 9.5v13" stroke="#ffb454" strokeWidth="2.6" strokeLinecap="round" />
            <circle cx="24" cy="16" r="2.1" fill="#2fd6a8" />
          </svg>
          whatsapp-connect-mcp
        </Link>
        <nav className="docs-top-links">
          <Link href="/">Home</Link>
          <a href="https://github.com/idle-sync/whatsapp-connect-mcp">GitHub</a>
        </nav>
      </header>

      <div className="docs-body">
        <aside className="docs-side">
          <nav aria-label="Docs">
            {NAV.map((group) => (
              <div className="docs-group" key={group.label}>
                <p className="docs-group-label">{group.label}</p>
                <ul>
                  {group.items.map((item) => (
                    <li key={item.slug || 'index'}>
                      <Link href={href(item.slug)}>{item.label}</Link>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </nav>
        </aside>
        <main className="docs-main">{children}</main>
      </div>
    </div>
  );
}
