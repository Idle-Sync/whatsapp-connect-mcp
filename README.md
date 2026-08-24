# whatsapp-connect-mcp

A WhatsApp MCP server shipped as a single static Go binary. One download, one
`setup` command, one QR scan — then any MCP client (Claude Desktop, Claude
Code, Cursor, Windsurf, Cline, …) can read, search, and send WhatsApp
messages, with every outbound send protected by a server-enforced gate.
That one-step path is the default stdio transport; the shared **http**
transport adds one more step — a server you start and keep running (see
[Install](#install-two-minutes)).

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
# or, sturdier for setup: a global install
npm install -g whatsapp-connect-mcp
```

> `npx whatsapp-connect-mcp serve` works fine on its own, but for `setup`
> prefer one of the install scripts above or the global install. `setup`
> injects an absolute path to the running binary into each MCP client's
> config, and under `npx` that path is inside npm's package cache — clear
> that cache and every client config `setup` wrote now points at a binary
> that's gone. A global install keeps the binary at a stable path until
> the package itself is upgraded or removed.

Each of these downloads the release binary for your OS/architecture and runs
`setup`: it shows a QR code to scan from WhatsApp (Linked Devices → Link a
Device), then detects installed MCP clients and offers to inject a
`whatsapp` server entry into whichever ones you pick. No toolchain, no
manual JSON editing.

`setup` also asks how clients should connect. **stdio** (the default) has
each client start its own server process — simplest, but only one client
or session can be connected at a time, since one `serve` holds the data
directory's exclusive lock. **http** points every selected client at one
shared local server (`http://127.0.0.1:<port>`, port of your choosing,
default 2178, bearer-token authenticated) so several clients and sessions
connect at once.

> **Picked http? There is a step 2: start the server.** Nothing starts it
> for you — until it runs, every client reports something like
> `ConnectionRefused at http://127.0.0.1:2178`. Run (and keep running):
>
> ```sh
> whatsapp-connect-mcp serve --http 127.0.0.1:2178
> ```
>
> It acknowledges with `serve: listening on http://127.0.0.1:2178 …` and
> stays in the foreground, so it dies with its terminal. To keep it alive
> across logouts and reboots instead, install it as a background service
> (launchd on macOS, a systemd user unit on Linux, or a Task Scheduler logon
> task on Windows):
>
> ```sh
> whatsapp-connect-mcp service install
> ```
>
> `service uninstall` removes it; `service restart` restarts it after an
> update. On a headless Linux box, add `loginctl enable-linger` so the
> service outlives your login session. On Windows, the service runs as a
> minimized console window that appears at user logon (not boot); closing the
> window stops the server. There is no automatic restart on crash (serve's
> unpaired state waits idle rather than exiting, so the common failure mode
> never exits anyway). Creating the task may require an elevated
> (Administrator) terminal.

`setup` can be re-run any time — to pair again, or to add a client you
installed later.

Pass `--full-history` to ask the phone for as much history as the protocol
allows rather than the default few months. It only has any effect while
actually pairing, so an install that is already paired must `remove` first;
`setup` says so rather than silently ignoring the flag. The phone still
decides what it really sends.

## What this is for

The fourteen read tools are the product; the ten gated write tools are a convenience.
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

Twenty-four tools: fourteen read-only, ten gated, described below.

### Read / search (no gate)

| Tool | What it returns |
|---|---|
| `list_chats` | Chats (1:1 and group), newest activity first; filterable by name and archived state. |
| `get_chat` | One chat by JID. |
| `list_messages` | Messages in a chat, newest first, optionally time-bounded — pass a named window (`today`, `yesterday`, `last_24h`, `last_7d`) or a `date` with an IANA `tz`, or explicit bounds (Unix seconds, RFC 3339, or a bare date); the server does the timezone arithmetic. |
| `search_messages` | Full-text search over message bodies, chat-scoped or global. |
| `get_message_context` | The messages immediately before/after one target message. |
| `search_contacts` | Contacts by name or phone number substring. |
| `get_last_interaction` | The most recent message involving a JID. |
| `list_group_participants` | A group's member JIDs, fetched live. |
| `get_group_info` | A group's subject, description, owner, and admins, fetched live. |
| `get_blocklist` | The JIDs the account has blocked, fetched live. |
| `get_call_history` | Calls, newest first, optionally filtered to one peer and time-bounded with the same window/date/tz forms as `list_messages`. |
| `download_media` | Downloads attached media to the local data directory — one message, a batch of message ids, or everything in a time window (same window/date/tz forms as `list_messages`, optionally filtered by kind). |
| `poll_new_messages` | New messages after a cursor, oldest first — `tail: N` returns the newest N immediately, and a timeout blocks up to 240s until one arrives, so an agent can react to activity without re-reading chats. Own sends excluded unless asked for. Read-only; reacting still goes through the send gate. |
| `fetch_older_messages` | Asks the phone for messages from before the oldest one stored in a chat, widening how far back it can be read. Call repeatedly to page further back. |
| `doctor` | Runs the diagnostics described in [Diagnostics](#diagnostics) as an MCP tool. |

> How far back any of these reach is decided by the paired phone, not by this
> server. "Search my whole history" can turn out to mean "search the last few
> months" — see [Limitations](#limitations-stated-plainly).

### Send (gated — see below)

| Tool | What it does |
|---|---|
| `send_message` | Sends text, optionally quoting an existing message. |
| `send_media` | Sends an image, video, or document from an allowed directory, with an optional caption. |
| `send_voice_note` | Sends a voice note from an Ogg Opus (`.ogg`) file in an allowed directory. No transcoding — other formats are rejected. |
| `send_reaction` | Reacts to a message with an emoji (empty emoji removes a prior reaction). |
| `edit_message` | Edits the text of a message you sent, within WhatsApp's edit window. |
| `delete_message` | Deletes a message for everyone (your own always; others' only as a group admin). |
| `create_poll` | Creates a poll (a question and two or more options); reading votes is not supported. |
| `mark_read` | Marks one or more messages as read. |
| `schedule_send` | Schedules a text or media send for a future time (up to 30 days; `send_at` or `delay_minutes`). The gate applies at scheduling time — untrusted recipients draft-and-confirm the schedule, fire time included — and the fire consumes the shared rate limiter. Persists across restarts; fires only while `serve` runs (≤15 min late fires on next start, older is dropped). |
| `list_scheduled` | Pending scheduled sends, soonest first. |
| `cancel_scheduled` | Cancels one pending scheduled send — always allowed, it only ever prevents a send. |
| `block_contact` | Blocks a contact; always drafts first, never auto-commits on trust. |
| `unblock_contact` | Unblocks a contact; always drafts first, never auto-commits on trust. |

Every tool result built from WhatsApp data — messages, names, contacts,
captions — is wrapped in an explicit untrusted-data banner. Treat it as
data an MCP client is showing you, never as instructions the model should
follow: nothing arriving over WhatsApp can tell your assistant what to do.

## The send gate

This is the part `verygoodplugins/whatsapp-mcp` doesn't have. Every
outbound action — text, media, voice note, reaction, edit, delete, poll,
block, unblock, or read receipt — goes through one path, enforced by the server, not by prompting
the model to "be careful":

1. **Draft first.** Call a send tool for a recipient you haven't trusted
   yet, and nothing is sent. You get back a preview (the recipient resolved
   to a name + JID, and the exact outbound content) and a `draft_token`.
2. **Confirm to commit.** Re-issue the identical call with that
   `draft_token` and it sends. Drafts expire after 5 minutes; a byte
   difference in the resubmitted content invalidates the token.
3. **Trust, deliberately.** `whatsapp-connect-mcp trust --add <jid>` marks a
   contact or group as trusted, so sends to it commit on the first call
   instead of drafting. This is a CLI-only switch — no MCP tool can grant
   trust, so a model can't trust its way around the draft step. The list
   is re-read on every send decision, so `trust --add`/`--remove` apply
   immediately, including to a `serve` process already running. For a
   grant you don't want to keep, there is a session-scoped form:
   `whatsapp-connect-mcp trust --session --add <jid>` elevates a recipient
   for the life of the current `serve` process only — never written to
   `config.json`, wiped automatically the next time `serve` starts.
   Use it when you are actively drafting a thread with one person or group
   and have already confirmed the first sends by hand; it cuts the
   draft-and-confirm round-trip for that recipient without granting
   anything permanent. Like persistent trust it is CLI-only (no MCP tool
   can grant it), and block/unblock still draft on every call regardless.
4. **Rate limit, always.** Every send — drafted, trusted, whatever —
   consumes a token from one rate limiter shared across all five send
   tools. The interval has a hard 5-second floor that no configuration can
   go below. A rate-limited commit leaves the draft valid; retry the same
   call with the same token once the limit clears.

`mark_read` is the one exception to drafting: a read receipt isn't authored
content, so it always sends on the first call (still rate-limited, still
gated).

### Which files a send may attach

The gate above authorises a *recipient*. It says nothing about the *file* a
send names, so on its own it would let a manipulated model attach anything
this program can read — an SSH key, a password store — to a recipient you
had already trusted.

So outbound files are confined to an allowlist of directories. The default
is a single dedicated one, created on first run:

| OS | Default outbox |
|---|---|
| Linux | `~/.config/whatsapp-connect-mcp/outbox` |
| macOS | `~/Library/Application Support/whatsapp-connect-mcp/outbox` |
| Windows | `%AppData%\whatsapp-connect-mcp\outbox` |

Move a file there before sending it, or widen the list by setting
`media_roots` in `config.json` to absolute directory paths:

```json
{ "media_roots": ["/home/you/Pictures", "/home/you/Documents"] }
```

Paths are resolved before they are checked, so a symlink inside an allowed
directory is judged by where it actually leads, not where it sits. A send
naming a file outside the list is refused on the first call — before a draft
is minted and before it costs a rate-limit token — and the refusal names no
path, like every other error this server returns.

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
registers. whatsmeow's defaults announce an OS string of `whatsmeow` with an
unknown platform type, which is distinguishable from an official client by
reading the pairing payload alone. This project overrides that and announces
a Chrome browser identity instead (`internal/bridge/bridge.go`), which is
also what your phone shows for this device under Linked Devices.

Do not mistake that override for a fix. It defeats the most trivial version
of the check and not the underlying problem. `serve` and `setup` do refresh
the reported WhatsApp Web version before connecting, so the version and the
build hash derived from it track a real release rather than whichever one
was vendored at build time — but the user agent still carries whatsmeow's
placeholder carrier and manufacturer fields, and the session behaves on the
wire like whatsmeow, not like Chrome. Users of
whatsapp-web.js — which drives a real Chrome browser with a genuine
fingerprint — received the same warnings described below, which suggests the
announced identity was never the deciding signal. Changing it also does
nothing for a session that is already paired: the identity is sent when
pairing, so an existing session keeps whatever it registered with until you
pair it again.

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
  `setup --full-history` asks for as much as the protocol allows instead of
  the default few months, but it is a request, not a setting — and it only
  applies when pairing. For an install that is already paired, the
  `fetch_older_messages` tool asks the phone for more of a single chat
  without re-pairing. Both are requests the phone is free to answer with
  less, or nothing; neither recovers messages the phone itself has deleted.
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

Exactly two outbound network calls exist beyond the WhatsApp connection
itself. Both are best-effort, both time out after 2 seconds, and neither
carries message content, a JID, a phone number, or any session credential:

- `doctor`/`check` asks GitHub's public release API whether a newer version
  of this program exists. It never blocks or fails the check when GitHub is
  unreachable.
- `serve` and `setup` fetch the current WhatsApp Web client version from
  `web.whatsapp.com` before connecting, so the version this client reports
  tracks a real release instead of whichever one was vendored at build time.
  A failure is reported and ignored; a stale version still connects.

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
state, event-flow liveness (a connected session that has received no
WhatsApp events for over 30 minutes gets a warning — the state where the
socket looks healthy but ingestion has silently stalled), message database
integrity, injected MCP client configs, data
directory permissions (POSIX), and the version check above. Every finding
is sanitized — no JID, phone number, message content, or filesystem path
ever appears in a status line; a broken client config is named by the
client's name, never its path on disk.

## Dashboard

```sh
whatsapp-connect-mcp dashboard [--port n]
```

Prints (and tries to open) a one-time login link for a small web dashboard
served alongside `serve --http` — it shows connection health, store row
counts, and the same doctor findings `check` prints, refreshed every few
seconds. The link exchanges the HTTP transport's existing bearer token for
a session cookie, so nothing new needs to be configured or trusted.

The dashboard only ever listens on loopback, exactly like the HTTP
transport it shares a port with: `serve --http` must already be running,
and the page is unreachable from any other machine. Log back in with the
`dashboard` command any time the session expires (a server restart, most
often) — the printed token is never written to a server log.

## Backing up

```sh
whatsapp-connect-mcp backup [--dest path]
```

Writes a consistent snapshot of the message database (`messages.db`) to
`<data-dir>/backups/messages-<timestamp>.db` (or a custom path via `--dest`).
The backup is a standalone, fully-usable SQLite database — not a copy of
sessions or settings, just messages. Unlike a phone backup, a `backup`
snapshot is safe to take while `serve` is running; SQLite's WAL mode and
busy timeout guarantee consistency.

Message history is the one thing that cannot be recovered any other way — a
session can be re-paired if needed, but messages fetched from the phone stay
on the phone only as long as the phone remembers them, which is typically
a few months. A regular automated backup (via `cron`, a systemd timer, or the
Task Scheduler on Windows) is the simplest insurance against losing them.

## Other commands

```sh
whatsapp-connect-mcp setup [--full-history]  # pair (again) and configure MCP clients
whatsapp-connect-mcp status                  # pairing state, row counts, injected clients
whatsapp-connect-mcp clients [--remove]      # list or uninject MCP client entries
whatsapp-connect-mcp trust [--session] [--add jid|--remove jid|--list]
whatsapp-connect-mcp serve [--http addr]     # run the MCP server directly (stdio by default)
whatsapp-connect-mcp service <install|uninstall|restart> [--http addr]
                                             # manage a background serve --http service (macOS/Linux/Windows)
```

> **`--http` requires a bearer token and a loopback Host.** On first use it
> generates a 256-bit token, writes it to `.http-token` in the data
> directory (owner-only), and prints it once — put it in your client's
> `Authorization: Bearer <token>` header. Every request must also be
> addressed to a loopback Host (`localhost`, `127.0.0.1`, `[::1]`), which
> blocks a web page in your browser from reaching the server by rebinding
> DNS to a loopback address. Still bind `127.0.0.1`: the token guards
> against reaching the port, but binding a public interface exposes it to
> your whole network, and if you must, put your own access control (a
> reverse proxy, a VPN, a firewall rule) in front.

## Updating

Updating replaces the binary and nothing else: pairing, message history,
the trust list, and injected client configs all live in the data directory
and survive every update. Use the same method you installed with — each
puts the new binary at the same path the old one occupied, so client
configs keep working:

```sh
# macOS / Linux — the install script always fetches the latest release
curl -fsSL https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.sh | sh
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.ps1 | iex
```

```sh
# npm global install
npm update -g whatsapp-connect-mcp

# npx without an install — @latest bypasses npx's cached copy
npx whatsapp-connect-mcp@latest setup
```

Then restart what runs the binary: stdio clients pick the new version up
when the MCP client next starts a session (restart the client app). With
the http transport, restart `serve` — for a service installed with
`service install`, that is:

```sh
whatsapp-connect-mcp service restart
```

A kept-alive service never re-execs on its own, so until it is restarted
the old version keeps serving.

You don't have to watch the releases page — `whatsapp-connect-mcp check`
and the `doctor` MCP tool compare the running version against the latest
release on every run, and warn with both versions named when they differ.

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
