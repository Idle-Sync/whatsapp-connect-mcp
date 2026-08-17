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
> clients on this protocol. The send gate and rate limiter described below
> reduce that risk (accidental bulk sends, a model going rogue) but **do not
> remove it**. Pair a number you're comfortable losing, not your only line
> to your bank or your family.

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

Each of these downloads the release binary for your OS/architecture and runs
`setup`: it shows a QR code to scan from WhatsApp (Linked Devices → Link a
Device), then detects installed MCP clients and offers to inject a
`whatsapp` server entry into whichever ones you pick. No toolchain, no
manual JSON editing.

`setup` can be re-run any time — to pair again, or to add a client you
installed later.

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
   trust, so a model can't trust its way around the draft step.
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

## Limitations (stated plainly)

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

## Uninstall / reset

- **`whatsapp-connect-mcp remove`** deletes the local WhatsApp session
  (unpairs). Your paired phone will show the device as removed. Prompts for
  a typed `yes` before doing anything.
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
