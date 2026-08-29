# site

The landing page and docs for whatsapp-connect-mcp, at
[whatsapp.idlesync.in](https://whatsapp.idlesync.in).

Next.js 15 (App Router) exported to static files. No server, no database.

```sh
npm install
npm run dev      # http://localhost:3000
npm run build    # → out/
```

## Layout

| Path | What it is |
|---|---|
| `app/page.tsx` | The landing page. Tells the story: the flood, the agent, the catch, the gate, the risk, install. |
| `lib/landing-body.html` | The landing markup, kept as one HTML file so the story stays editable in one place. |
| `styles/landing.css` | Its theme, which moves from night to daylight across the arc. |
| `public/landing.js` | The three interactions: tapping into the group, asking the agent, approving a held send. |
| `app/docs/` | Docs routes. `page.tsx` is `/docs`, `[slug]/page.tsx` is everything else. |
| `content/docs/*.md` | The docs themselves. Add a file, add it to `lib/nav.ts`, done. |
| `lib/docs.ts` | Markdown → HTML: GFM tables, `:::caution[…]` asides, heading anchors. |
| `styles/docs.css` | Docs wearing the same palette. |

## Regenerating the tools reference

`content/docs/tools.md` is generated from a **running server**, so the docs
cannot drift from the release. Do not edit it by hand.

```sh
whatsapp-connect-mcp serve --http 127.0.0.1:2178   # in another terminal
npm run gen:tools
```

It reads the bearer token from the config dir's `.http-token`, or `WCM_TOKEN`.
Override the address with `WCM_URL`. Run it whenever tools are added, removed or
re-described, and commit the result.

## Deploying

Vercel detects Next.js and needs no configuration. `output: 'export'` in
`next.config.mjs` means it builds to plain files — nothing runs at request time.

The domain is `whatsapp.idlesync.in`. DNS is on Cloudflare, so when you add the
domain in Vercel set the CNAME to **DNS only** (grey cloud, not orange).
Proxying Cloudflare in front of Vercel breaks their certificate check and the
domain sticks on "Invalid Configuration".
