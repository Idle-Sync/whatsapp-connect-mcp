import type { Metadata } from 'next';
import DocPage from '@/components/DocPage';
import { getDoc } from '@/lib/docs';

export async function generateMetadata(): Promise<Metadata> {
  const doc = await getDoc('');
  return { title: doc.title, description: doc.description };
}

export default async function DocsIndex() {
  const doc = await getDoc('');
  return <DocPage doc={doc} />;
}
