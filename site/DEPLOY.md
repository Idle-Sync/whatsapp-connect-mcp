# What to do next

Internal runbook for getting the site live at
[whatsapp.idlesync.in](https://whatsapp.idlesync.in) and keeping it honest
afterwards. Not part of the published docs.

---

## 1. Land the branch

The site is on `site/landing-and-docs`.

```sh
gh pr create --fill --base main --head site/landing-and-docs
# or, if you would rather skip the review
git checkout main && git merge site/landing-and-docs && git push
```

Nothing else in the repo changes. The Go build does not see `site/`.

---

## 2. Create the Vercel project

Import `Idle-Sync/whatsapp-connect-mcp` at
[vercel.com/new](https://vercel.com/new), then set **one thing that is easy to
miss**:

| Setting | Value |
|---|---|
| **Root Directory** | `site` |
| Framework preset | Next.js (auto-detected once the root is right) |
| Build command | default — `next build` |
| Output directory | default — Vercel handles `output: 'export'` |
| Install command | default |
| Environment variables | none |

:warning: If you leave Root Directory at the repo root, Vercel tries to build
the Go project and fails with something unhelpful about a missing framework.
That is the only failure mode worth pre-empting.

Deploy. You get a `*.vercel.app` URL. Check it before touching DNS.

---

## 3. Point the domain

In **Vercel → Project → Settings → Domains**, add:

```
whatsapp.idlesync.in
```

Vercel gives you a CNAME target. In **Cloudflare DNS**, add it as:

| Type | Name | Target | Proxy |
|---|---|---|---|
| CNAME | `whatsapp` | (whatever Vercel shows) | **DNS only — grey cloud** |

:warning: **Grey cloud, not orange.** Proxying Cloudflare in front of Vercel
breaks their certificate validation and the domain sits on "Invalid
Configuration" indefinitely. This is the single thing most likely to cost you
an hour.

Certificate issues within a minute or two once DNS resolves.

---

## 4. Check it before telling anyone

The landing page is mostly interaction, so a glance at the homepage proves
almost nothing. Walk it:

- [ ] Chat list loads with counts **climbing**, then stopping at 412 and 118.
- [ ] The Studio badge starts glowing and the hint appears once counts rest.
- [ ] Tapping Studio slides into the group and the flood arrives, fast at
      first, then settling.
- [ ] **Ask instead** dims the flood, shows three lines, and messages **keep
      arriving behind it**.
- [ ] **Back to the list** resets and it can be played again.
- [ ] The Claude window waits, a question chip pulses, clicking it types into
      the input and fires.
- [ ] **Reply to Ma** returns `send_message` as amber `held`, not green.
- [ ] **Approve and send** flips it to `sent` with a live timestamp;
      **Not now** strikes it out.
- [ ] The strengths list lights up per question, amber on the gated one.
- [ ] The hazard band is the only loud colour on the page.
- [ ] `/docs`, `/docs/tools`, `/docs/ban-risk` all load; sidebar, on-this-page
      and prev/next all work.
- [ ] A wrong URL gives the 404 page, not a Vercel error.
- [ ] Favicon shows in the tab.
- [ ] Open it on a phone. The phone mock and the agent window both stack.

---

## 5. On every release, regenerate the tools page

`content/docs/tools.md` is **generated**. Editing it by hand is how the docs
start lying.

```sh
whatsapp-connect-mcp serve --http 127.0.0.1:2178   # in another terminal
cd site && npm run gen:tools
git commit -am "docs: regenerate tools for vX.Y.Z"
```

It reads the token from the config dir's `.http-token`, or `WCM_TOKEN`.
Override the address with `WCM_URL`.

Run it whenever a tool is **added, removed, renamed, re-described, or has its
arguments changed**. If the read/gated split changes, also update the `GATED`
set at the top of `scripts/gen-tools.mjs` — it is the one thing the generator
cannot learn from the server.

Three places quote tool counts and will need the same edit:

- `content/docs/tools.md` — generated, handled by the script.
- `lib/landing-body.html` — the "Twenty-eight tools. Twelve of them stop at
  the gate." headline and the two columns beneath it.
- `public/landing.js` — the `STRENGTHS` list mentions tool names.

---

## 6. Known follow-ups

Ordered by how much they matter.

**Fix the tool count in the project README.** It says "fourteen read-only" and
"Twenty-four tools" while the table beneath lists fifteen and the running
server exposes twenty-eight. This was found by pointing the generator at a live
server. Worth fixing at the source, and worth considering whether the README's
table should be generated the same way.

**Decide what the hosted tier actually says.** The landing page currently ends
with one quiet line — "a hosted version is being worked on" — and no pricing.
That was deliberate: publishing tiers before they exist invites questions you
cannot answer yet. When the hosted side is real, the pricing block goes at the
very bottom of the page, after the tool list, never above it.

**Add a real screen recording.** The phone mock is a reconstruction. Once the
binary's pairing flow is settled, a short recording of an actual `setup` run
belongs near the install section, where "here it is really working" carries more
weight than an illustration. It does not replace the interactive opening.

**Open Graph image.** There is none, so links posted anywhere render as bare
text. A single static image of the phone mid-flood would do it.

**Search.** Twelve pages do not need it. Past twenty, add one.

---

## Rolling back

Vercel keeps every deployment. **Project → Deployments → the one that worked →
Promote to Production.** No rebuild, no git revert, effective in seconds.
