export type NavItem = { label: string; slug: string };
export type NavGroup = { label: string; items: NavItem[] };

/** The docs sidebar. Order is the reading order, not alphabetical. */
export const NAV: NavGroup[] = [
  {
    label: 'Start',
    items: [
      { label: 'What this is', slug: '' },
      { label: 'Install', slug: 'install' },
      { label: 'Pair your phone', slug: 'pair' },
      { label: 'Connect your client', slug: 'clients' },
    ],
  },
  {
    label: 'Safety',
    items: [
      { label: 'The send gate', slug: 'send-gate' },
      { label: 'Ban risk', slug: 'ban-risk' },
    ],
  },
  {
    label: 'Running it',
    items: [
      { label: 'stdio and http', slug: 'transports' },
      { label: 'Run as a service', slug: 'service' },
      { label: 'Your data on disk', slug: 'data' },
    ],
  },
  {
    label: 'Reference',
    items: [
      { label: 'Tools', slug: 'tools' },
      { label: 'Limitations', slug: 'limitations' },
      { label: 'Troubleshooting', slug: 'troubleshooting' },
    ],
  },
];

export const FLAT: NavItem[] = NAV.flatMap((g) => g.items);

export function href(slug: string) {
  return slug ? `/docs/${slug}/` : '/docs/';
}

export function neighbours(slug: string) {
  const i = FLAT.findIndex((x) => x.slug === slug);
  return {
    prev: i > 0 ? FLAT[i - 1] : null,
    next: i >= 0 && i < FLAT.length - 1 ? FLAT[i + 1] : null,
  };
}
