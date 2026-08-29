import Link from 'next/link';
import type { Doc } from '@/lib/docs';
import { href, neighbours } from '@/lib/nav';

export default function DocPage({ doc }: { doc: Doc }) {
  const { prev, next } = neighbours(doc.slug);

  return (
    <article className="doc">
      <div className="doc-col">
        <h1>{doc.title}</h1>
        {doc.description ? <p className="doc-lede">{doc.description}</p> : null}

        {/* Built from our own markdown at build time. */}
        <div className="doc-content" dangerouslySetInnerHTML={{ __html: doc.html }} />

        <nav className="doc-neighbours">
          {prev ? (
            <Link className="nb prev" href={href(prev.slug)}>
              <span>Previous</span>
              <b>{prev.label}</b>
            </Link>
          ) : (
            <span />
          )}
          {next ? (
            <Link className="nb next" href={href(next.slug)}>
              <span>Next</span>
              <b>{next.label}</b>
            </Link>
          ) : (
            <span />
          )}
        </nav>
      </div>

      {doc.headings.length > 1 ? (
        <aside className="doc-toc">
          <p className="doc-toc-label">On this page</p>
          <ul>
            {doc.headings.map((h) => (
              <li key={h.id} className={h.depth === 3 ? 'sub' : ''}>
                <a href={`#${h.id}`}>{h.text}</a>
              </li>
            ))}
          </ul>
        </aside>
      ) : null}
    </article>
  );
}
