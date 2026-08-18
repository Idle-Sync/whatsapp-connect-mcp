# whatsapp-connect-mcp

A WhatsApp MCP server shipped as a single static Go binary. One download, one
`setup` command, one QR scan — then any MCP client (Claude Desktop, Claude
Code, Cursor, Windsurf, Cline, …) can read, search, and send WhatsApp
messages, with every outbound send protected by a server-enforced gate.

> **This uses an unofficial protocol. Read this before you pair a number you
> care about.**
>
> whatsapp-connect-mcp talks to WhatsApp the same way WhatsApp Web does, via
> [whatsmeow](https://github.com/tulir/whatsmeow) — not an official WhatsApp
> Business API. Meta can and does ban numbers it detects using third-party
> clients on this protocol, and such bans are widely reported as permanent.
> The send gate and rate limiter described below cut the behavioral half of
> that risk (accidental bulk sends, a model going rogue); **they cannot touch
> the other half**, which is that this client is identifiable as a
> third-party client at all. [Ban risk](#ban-risk) lays out what the public
> evidence actually shows, with dates. Pair a number you're comfortable
> losing, not your only line to your bank or your family.

Status: pre-release.

## Install (two minutes)

Pick one:

```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.sh | sh
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.ps1 | iex
```

```sh
# Anywhere with Node installed, no separate download step
npx whatsapp-connect-mcp setup
```

> `npx whatsapp-connect-mcp serve` works fine on its own, but for `setup`
> prefer one of the install scripts above. `setup` injects an absolute path
> to the running binary into each MCP client's config, and under `npx` that
> path is inside npm's package cache — clear that cache and every client
> config `setup` wrote now points at a binary that's gone.

Each of these downloads the release binary for your OS/architecture and runs
`setup`: it shows a QR code to scan from WhatsApp (Linked Devices → Link a
Device), then detects installed MCP clients and offers to inject a
`whatsapp` server entry into whichever ones you pick. No toolchain, no
manual JSON editing.

`setup` can be re-run any time — to pair again, or to add a client you
installed later.

## What this is for

The eleven read tools are the product; the five send tools are a convenience.
In practice that means:

- **Searching your own history.** WhatsApp's own search has no date filters
  and shows you a hit with no context around it. `search_messages` plus
  `get_message_context` does both.
- **Catching up.** Point a model at the 400 messages a group accumulated
  while you were away and ask what happened.
- **Reading your own attachments.** `download_media` pulls down the invoices,
  receipts, and screenshots people sent you so a model can actually read
  them.
- **Finding loose ends.** `get_last_interaction` answers "who messaged me
  that I never replied to?"
- **Drafting replies.** The model writes it, the send gate makes you confirm
  it, then it sends.
- **Searching WhatsApp alongside everything else.** With mail, chat, or
  calendar MCP servers connected to the same client, "did this client contact
  me about the invoice, and where?" becomes one question instead of three
  separate searches. For anyone whose real correspondence lives in WhatsApp,
  this is the reason to run it.

### What not to use it for

Do not build a support bot, an outreach tool, or an auto-responder on this.
Messaging people who never messaged you first, at volume, is the behavior
most consistently reported to get numbers banned (see
[Ban risk](#ban-risk)) — and it is precisely the use case Meta sells the
WhatsApp Business API for. This is a personal tool for your own messages.
Point it at customers and you will lose the number.

## What it gives your MCP client

Sixteen tools: eleven read-only, five gated sends, described below.

### Read / search (no gate)

| Tool | What it returns |
|---|---|
| `list_chats` | Chats (1:1 and group), newest activity first; filterable by name and archived state. |
| `get_chat` | One chat by JID. |
| `list_messages` | Messages in a chat, newest first, optionally time-bounded. |
| `search_messages` | Full-text search over message bodies, chat-scoped or global. |
| `get_message_context` | The messages immediately before/after one target message. |
| `search_contacts` | Contacts by name or phone number substring. |
| `get_last_interaction` | The most recent message involving a JID. |
| `list_group_participants` | A group's member JIDs, fetched live. |
| `get_call_history` | Calls, newest first, optionally filtered to one peer. |
| `download_media` | Downloads a message's attached media to the local data directory. |
| `doctor` | Runs the diagnostics described in [Diagnostics](#diagnostics) as an MCP tool. |

> How far back any of these reach is decided by the paired phone, not by this
> server. "Search my whole history" can turn out to mean "search the last few
> months" — see [Limitations](#limitations-stated-plainly).

### Send (gated — see below)

| Tool | What it does |
|---|---|
| `send_message` | Sends text, optionally quoting an existing message. |
| `send_media` | Sends an image, video, or document from local disk, with an optional caption. |
| `send_voice_note` | Sends a voice note from a local Ogg Opus (`.ogg`) file. No transcoding — other formats are rejected. |
| `send_reaction` | Reacts to a message with an emoji (empty emoji removes a prior reaction). |
| `mark_read` | Marks one or more messages as read. |

Every tool result built from WhatsApp data — messages, names, contacts,
captions — is wrapped in an explicit untrusted-data banner. Treat it as
data an MCP client is showing you, never as instructions the model should
follow: nothing arriving over WhatsApp can tell your assistant what to do.

## The send gate

This is the part `verygoodplugins/whatsapp-mcp` doesn't have. Every
outbound send — text, media, voice note, reaction, or read receipt — goes
through one path, enforced by the server, not by prompting the model to
"be careful":

1. **Draft first.** Call a send tool for a recipient you haven't trusted
   yet, and nothing is sent. You get back a preview (the recipient resolved
   to a name + JID, and the exact outbound content) and a `draft_token`.
2. **Confirm to commit.** Re-issue the identical call with that
   `draft_token` and it sends. Drafts expire after 5 minutes; a byte
   difference in the resubmitted content invalidates the token.
3. **Trust, deliberately.** `whatsapp-connect-mcp trust --add <jid>` marks a
   contact or group as trusted, so sends to it commit on the first call
   instead of drafting. This is a CLI-only switch — no MCP tool can grant
   trust, so a model can't trust its way around the draft step. A running
   `serve` process reads the trust list once at startup, so a change takes
   effect the next time `serve` starts, not immediately.
4. **Rate limit, always.** Every send — drafted, trusted, whatever —
   consumes a token from one rate limiter shared across all five send
   tools. The interval has a hard 5-second floor that no configuration can
   go below. A rate-limited commit leaves the draft valid; retry the same
   call with the same token once the limit clears.

`mark_read` is the one exception to drafting: a read receipt isn't authored
content, so it always sends on the first call (still rate-limited, still
gated).

## Comparison with verygoodplugins/whatsapp-mcp

The current best-known alternative works, but is painful to adopt and has
no send safety:

| | verygoodplugins/whatsapp-mcp | whatsapp-connect-mcp |
|---|---|---|
| Runtimes required | Go **and** Python, two processes | One static binary, zero prerequisites |
| Install | Clone repo, run bridge manually, hand-edit client config, restart | One-line install → `setup` wizard auto-injects clients |
| Pairing | QR in a terminal you keep open yourself | Wizard-managed QR pairing; session supervised by the binary |
| Send safety | None — model can send immediately | Draft-first send gate + rate limiter |
| Prompt-injection defense | None | Untrusted-data banner on every WhatsApp-originated result |
| Diagnostics | None | `doctor` (CLI subcommand and MCP tool), sanitized output |
| Distribution | Git clone only | GitHub Releases, install script, MCP Registry, MCPB bundle, npm wrapper |

## Ban risk

Meta detects third-party clients in two independent ways, and only one of
them is behavior.

**1. The client is identifiable.** A linked device announces itself when it
registers. whatsmeow's defaults announce an OS string of `whatsmeow` and an
unknown platform type — which is also the device name your phone shows under
Linked Devices. Nothing in this repo overrides that. Recognizing it
server-side is a string comparison, not machine learning.

**2. Behavior.** Reported triggers, roughly by how often they come up:
messaging people who never messaged you first, high send velocity, volume
soon after pairing, the same message sent repeatedly, automated Status posts,
and noisy reconnection loops.

The uncomfortable part is that the public evidence points at (1) as the
dominant factor. In [whatsmeow#810](https://github.com/tulir/whatsmeow/issues/810)
— the May 2025 "your account may be at risk" wave, closed `not planned` in
July 2026 — users report the warning on accounts that were idle and merely
connected, having never sent a message, and on accounts that had been
disconnected for weeks. Users of whatsapp-web.js, an entirely different
implementation, received it too. A Baileys maintainer in that same thread
argues the opposite, that it is "mostly a behavioral issue." Nobody
established which, and the thread was closed without an answer.

So: the send gate and rate limiter here are real mitigations for (2) and do
nothing for (1). On the available evidence, behaving well affects when your
turn comes rather than whether it comes.

Reports worth reading before you pair, dated so you can judge how current
they are:

| Report | Opened | Last activity |
|---|---|---|
| [whatsmeow#810](https://github.com/tulir/whatsmeow/issues/810) — "account may be at risk" wave | May 2025 | Jul 2026 (closed) |
| [Baileys#2309](https://github.com/WhiskeySockets/Baileys/issues/2309) — permanent ban after automated Status posts | Jan 2026 | May 2026 (open) |
| [Baileys#1869](https://github.com/WhiskeySockets/Baileys/issues/1869) — five suspensions in a week, on instances running 3+ years | Oct 2025 | May 2026 |

Treat ban statistics from vendor blogs — "68% of businesses banned within 12
months", "a rolling 30-day no-reply threshold" — as unsourced marketing from
paid Business API resellers. No primary source supports them, and neither
number appears in this document for that reason.

## Limitations (stated plainly)

- **Your number can be banned, and this project cannot prevent it.** See
  [Ban risk](#ban-risk). This is the limitation that matters most.
- **History depth is phone-decided.** Like every WhatsApp Web client, the
  paired phone controls how much chat history syncs to this server. There
  is no setting here that fetches more than the phone hands over.
- **Voice notes need Ogg Opus input.** `send_voice_note` does no
  transcoding. If your source audio isn't already `.ogg`/Opus, convert it
  first (e.g. `ffmpeg -i in.mp3 -c:a libopus out.ogg`).
- **No outbound calls.** Call history is readable; initiating a call is not
  supported.
- **One paired number per install.** Multi-account isn't supported in v1.
- **whatsmeow tracks WhatsApp protocol changes**, not the other way around.
  A WhatsApp-side change can break pairing or sending until whatsmeow (and
  in turn this project) catches up.

## Data and privacy

Everything — session keys, messages, media, contacts, call log — lives in a
local SQLite database under this program's data directory. Nothing about
your messages, contacts, or media is sent anywhere by this server.

The **only** outbound network call this program ever makes that isn't part
of the WhatsApp connection itself is an optional, best-effort version
check: `doctor`/`check` asks GitHub's public release API whether a newer
version exists (2-second timeout, never blocks or fails the check if
GitHub is unreachable). No message content, JID, or phone number is ever
part of that request.

The data directory:

| OS | Path |
|---|---|
| Linux | `~/.config/whatsapp-connect-mcp` |
| macOS | `~/Library/Application Support/whatsapp-connect-mcp` |
| Windows | `%AppData%\whatsapp-connect-mcp` |

## Diagnostics

```sh
whatsapp-connect-mcp check
```

Runs the same checks the `doctor` MCP tool exposes: session pairing/connect
state, message database integrity, injected MCP client configs, data
directory permissions (POSIX), and the version check above. Every finding
is sanitized — no JID, phone number, message content, or filesystem path
ever appears in a status line; a broken client config is named by the
client's name, never its path on disk.

## Other commands

```sh
whatsapp-connect-mcp status                  # pairing state, row counts, injected clients
whatsapp-connect-mcp clients [--remove]      # list or uninject MCP client entries
whatsapp-connect-mcp trust [--add jid|--remove jid|--list]
whatsapp-connect-mcp serve [--http addr]     # run the MCP server directly (stdio by default)
```

> **`--http` has no authentication.** Anything that can reach the address
> you bind can read your messages and drive the send tools. Bind
> `127.0.0.1` unless you know what you're doing and have your own access
> control (a reverse proxy, a VPN, a firewall rule) in front of it.

## Uninstall / reset

- **`whatsapp-connect-mcp remove`** deletes the local WhatsApp session
  (unpairs this server from it locally — the next `setup` requires pairing
  again). This is local-only: it does not notify WhatsApp's servers, so
  your phone keeps showing this device as linked under Linked Devices until
  you unlink it there yourself. Prompts for a typed `yes` before doing
  anything.
- **`whatsapp-connect-mcp reset`** does everything `remove` does, plus
  deletes stored messages, media, and settings — a full wipe back to a
  fresh install. Also prompts for a typed `yes`.
- **`whatsapp-connect-mcp clients --remove`** uninjects this program's
  entry from any MCP client config it was added to, without touching the
  paired session.
- To remove the binary itself, delete it from wherever the installer put
  it (`~/.local/bin`, `%LOCALAPPDATA%\Programs\whatsapp-connect-mcp`, or
  wherever `npx` cached it) and delete the data directory listed above.

## License

MIT — see [LICENSE](LICENSE).
