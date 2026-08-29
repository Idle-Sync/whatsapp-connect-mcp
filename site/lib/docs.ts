import fs from 'node:fs';
import path from 'node:path';
import matter from 'gray-matter';
import { unified } from 'unified';
import remarkParse from 'remark-parse';
import remarkGfm from 'remark-gfm';
import remarkDirective from 'remark-directive';
import remarkRehype from 'remark-rehype';
import rehypeRaw from 'rehype-raw';
import rehypeSlug from 'rehype-slug';
import rehypeStringify from 'rehype-stringify';
import { visit } from 'unist-util-visit';
import { toString } from 'mdast-util-to-string';

const DIR = path.join(process.cwd(), 'content/docs');

export type Heading = { id: string; text: string; depth: number };
export type Doc = {
  slug: string;
  title: string;
  description: string;
  html: string;
  headings: Heading[];
};

/** `:::caution[Title]` … `:::` becomes a coloured aside, the way the
 *  landing page colours things: danger for the ban risk, caution for
 *  anything gated, tip for anything safe. */
const ASIDE_TITLES: Record<string, string> = {
  note: 'Note',
  tip: 'Tip',
  caution: 'Caution',
  danger: 'Danger',
};

function remarkAsides() {
  return (tree: any) => {
    visit(tree, (node: any) => {
      if (node.type !== 'containerDirective') return;
      const kind = ASIDE_TITLES[node.name] ? node.name : 'note';
      let title = ASIDE_TITLES[kind];

      const first = node.children?.[0];
      if (first?.type === 'paragraph' && first.data?.directiveLabel) {
        title = toString(first);
        node.children.shift();
      }

      node.data = node.data || {};
      node.data.hName = 'aside';
      node.data.hProperties = { className: ['aside', `aside-${kind}`] };
      node.children.unshift({
        type: 'paragraph',
        data: { hName: 'p', hProperties: { className: ['aside-title'] } },
        children: [{ type: 'text', value: title }],
      });
    });
  };
}

function slugify(text: string) {
  return text
    .toLowerCase()
    .replace(/[^\w\s-]/g, '')
    .trim()
    .replace(/\s+/g, '-');
}

/** Collect h2/h3 for the "on this page" rail. */
function remarkHeadings(collected: Heading[]) {
  return (tree: any) => {
    visit(tree, 'heading', (node: any) => {
      if (node.depth !== 2 && node.depth !== 3) return;
      const text = toString(node);
      collected.push({ id: slugify(text), text, depth: node.depth });
    });
  };
}

function fileFor(slug: string) {
  return path.join(DIR, `${slug || 'index'}.md`);
}

export function allSlugs(): string[] {
  return fs
    .readdirSync(DIR)
    .filter((f) => f.endsWith('.md'))
    .map((f) => f.replace(/\.md$/, ''))
    .filter((s) => s !== 'index');
}

export async function getDoc(slug: string): Promise<Doc> {
  const raw = fs.readFileSync(fileFor(slug), 'utf8');
  const { data, content } = matter(raw);
  const headings: Heading[] = [];

  const file = await unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkDirective)
    .use(remarkAsides)
    .use(remarkHeadings, headings)
    .use(remarkRehype, { allowDangerousHtml: true })
    .use(rehypeRaw)
    .use(rehypeSlug)
    .use(rehypeStringify, { allowDangerousHtml: true })
    .process(content);

  return {
    slug,
    title: String(data.title ?? slug),
    description: String(data.description ?? ''),
    html: String(file),
    headings,
  };
}
