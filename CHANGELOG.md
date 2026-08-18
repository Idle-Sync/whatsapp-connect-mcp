# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

whatsapp-connect-mcp is a WhatsApp MCP server shipped as a single static Go
binary: pair once via QR code, and any MCP client can read, search, and send
WhatsApp messages through it.

### Added

- **Scheduled sends.** `schedule_send` queues a text or media message
  for a future time (up to 30 days ahead, via `send_at` or
  `delay_minutes`), with `list_scheduled` and `cancel_scheduled`
  alongside. The send gate applies **at scheduling time**: an untrusted
  recipient gets the usual draft-then-confirm, with the fire time part
  of both the preview and the draft token, and the human's commit
  stores the schedule; a trusted recipient schedules on the first call.
  At fire time the delivery consumes the same shared rate limiter as
  every live send. Schedules persist across restarts in the data
  directory, but fire only while `serve` runs: one that came due while
  the server was down fires on the next start only if it is under 15
  minutes late, and is dropped (and reported) otherwise.
- **`poll_new_messages`: agents can wait for new messages.** A
  cursor-based long poll in the shape Telegram's `getUpdates` and
  Matrix's `/sync` converged on: the first call (no cursor) anchors a
  `next_cursor` at now; later calls return what arrived after it,
  oldest first, optionally scoped to one chat, with a fresh cursor —
  replaying a cursor can neither skip nor duplicate a message. With
  `timeout_seconds` (max 240) the call blocks until a matching message
  arrives, emitting MCP progress notifications so client idle timers
  keep resetting — in clients that background long tool calls this
  behaves like push. Own sends are excluded by default so an agent is
  never woken by its own messages; results carry the untrusted-data
  banner; the tool is read-only and reacting still goes through the
  send gate unchanged.
- **Mentions in message text resolve to names.** WhatsApp renders a
  mention into the body as `@<digits>` (a phone number or privacy LID
  local part). Message rows now rewrite standalone mention tokens
  through the same chain sender names use — LID contact, mapped phone
  contact, phone-JID contact, then the mapped phone digits — so
  `@99566015803422` reads as `@Bhassker Ghosh`. Unresolvable tokens,
  email addresses, and mid-word `@` are left untouched.
- **Reads wait out the reconnect catch-up window.** WhatsApp redelivers
  messages that arrived while the server was down during the first
  seconds after connecting. Store-backed reads (`list_messages`,
  `search_messages`, and the rest) now wait for that offline queue to
  finish draining — signalled by the protocol, bounded by a 15-second
  grace deadline — before answering, so a read right after a restart
  reflects what arrived during the downtime instead of a mirror that is
  knowably behind. In the steady state the check is two atomic loads;
  no read is ever delayed once the session is caught up.
- **Transport choice in `setup`.** The wizard now asks how MCP clients
  should connect: **stdio** (default — each client starts its own server;
  one client/session at a time, since one `serve` holds the data
  directory's lock) or **http** (one shared local server on a port of
  your choosing, default 2178). The http choice injects a
  `type: http` entry with the server URL and the bearer token from
  `.http-token` — minted by setup if needed, so the header and
  `serve --http` agree without hand-copying — and ends by printing the
  `serve --http` command, since no client starts the shared server for
  you. `doctor` validates http entries as configuration (a URL names no
  binary to check).
- **Single-instance guard.** `serve` takes an OS-level exclusive lock on
  the data directory (flock on Unix, LockFileEx on Windows) before opening
  anything, and refuses to start with a clear error while another serve
  holds it — an MCP client reconnect can no longer leave two servers
  double-attached to the same SQLite files. The OS releases the lock on
  process exit, however the process dies, so there is no stale-lock case.
- **Ingestion-liveness check in doctor.** The bridge records when the last
  WhatsApp event reached the ingestion pipeline, and `doctor` warns when a
  connected session has seen no events for over 30 minutes — the
  silent-stall state (socket healthy, pipeline dead, messages lost) that
  previously passed every check.
- **Session-scoped trust (`trust --session`).** A recipient the human
  elevates mid-session auto-commits for the life of the current `serve`
  process, then evaporates: `trust --session --add <jid>` takes effect
  immediately in the running server, is never written to `config.json`,
  and is wiped automatically on the next `serve` start. It cuts the
  draft-then-confirm round-trip for a thread the human is actively,
  repeatedly approving, while keeping every gate invariant: the grant is
  CLI-only (no MCP tool can make one), every send stays rate-limited, and
  block/unblock still draft on every call regardless of any trust.
- **Message kinds beyond the basics.** Contact cards, locations (with name
  and address as the row text), polls (question as text), poll votes,
  group invites, round video notes (downloadable like any video), calendar
  events, and protocol bookkeeping now decode as `contact`, `location`,
  `poll`, `poll_update`, `group_invite`, `video_note`, `event`, and
  `system` instead of collapsing to `other`. Disappearing-chat,
  view-once, and document-with-caption wrappers are peeled first, so the
  content inside reads as what it is — and its media downloads. Anything
  still unrecognized reports `other:<subtype>` (e.g. `other:buttons`)
  from the message's own type name, so a reader always knows what the
  item is.
- **LID senders resolve to real names.** Group messages often identify the
  sender only by a privacy LID (`…@lid`). Message events that carry both of
  the sender's addresses now teach the store the LID→phone pairing (a new
  `lid_map` table, migrated in place on first open), and message rows
  resolve a LID sender through it: the LID's own contact name if one is
  known, else the phone-number contact's name, else at least the phone JID
  — the raw LID is the last resort, not the default. The sender's push
  name is recorded against the phone-number identity too, so
  `search_contacts` can find them.
- **Batch media download.** `download_media` now takes exactly one of: a
  single `message_id` (unchanged), a `message_ids` batch (max 100 per
  call), or a time window — the same before/after/date/window/tz forms as
  `list_messages`, optionally narrowed by media `kind` — so "pull every
  attachment from yesterday" is one call instead of one per file. In a
  batch, a failure on one file is reported on its own line and the rest
  still download.
- **Outbound sends land in the local store.** A message sent through this
  server (text, media, voice note, reaction, poll) is recorded exactly as an
  inbound copy would be, so it appears in `list_messages`,
  `search_messages`, and `get_message_context` immediately — the outbound
  half of a conversation is no longer invisible to reads — and
  `download_media` works on your own sent attachments. whatsmeow does not
  echo this client's own sends back as events, so the send path records
  them itself, only after WhatsApp accepts the send.
- **Server-side time windows.** `list_messages` and `get_call_history`
  resolve human-shaped time bounds themselves: a named `window` (`today`,
  `yesterday`, `last_24h`, `last_7d`) or a whole `date` (YYYY-MM-DD),
  interpreted in an IANA `tz`, against the server's own clock — no client
  ever has to compute epoch seconds or do timezone arithmetic. Explicit
  `before`/`after` bounds accept Unix seconds, RFC 3339 timestamps, or bare
  dates. The IANA timezone database is embedded, so `tz` works on every OS
  including Windows.
- **Twenty-eight MCP tools.** Fifteen read-only — `list_chats`, `get_chat`,
  `list_messages`, `search_messages`, `get_message_context`,
  `search_contacts`, `get_last_interaction`, `list_group_participants`,
  `get_group_info`, `get_blocklist`, `get_call_history`, `download_media`,
  `fetch_older_messages`, `poll_new_messages`, `doctor` —
  plus eleven gated writes:
  `send_message`, `send_media`, `send_voice_note`, `send_reaction`,
  `edit_message`, `delete_message`, `create_poll`, `block_contact`,
  `unblock_contact`, `mark_read`, `schedule_send` — and two schedule
  management tools, `list_scheduled` and `cancel_scheduled`. Every WhatsApp-originated result (messages, names, captions,
  contacts) is wrapped in an explicit untrusted-data banner, so nothing
  arriving over WhatsApp can be mistaken for instructions to the model.
- **A server-enforced send gate.** Every outbound action goes through one
  path: an untrusted recipient gets a draft-then-confirm preview before
  anything sends; `whatsapp-connect-mcp trust --add <jid>` marks a recipient
  as trusted so its sends commit on the first call (a CLI-only switch — no
  MCP tool can grant trust); and every send, trusted or not, is rate-limited
  with a hard 5-second-per-send floor no configuration can lower.
- **`setup`**, an interactive wizard: QR pairing, then detection of
  installed MCP clients (Claude Desktop, Claude Code, Cursor, Windsurf,
  Cline) with a numbered multi-select of which ones to configure. Nothing
  is written to any client config until you explicitly confirm.
- **`doctor`** (as the `check` CLI subcommand and as an MCP tool):
  diagnostics for session pairing/connection state, message database
  integrity, injected client configs, data directory permissions, and
  whether a newer release is available — every finding sanitized, no JID,
  phone number, message content, or filesystem path in its output.
- **Management commands:** `status` (pairing state, row counts, injected
  clients), `clients [--remove]` (list or uninject client entries), `trust
  [--add|--remove|--list]`, `remove` (deletes the local session), and
  `reset` (`remove` plus stored messages, media, and settings) — `remove`
  and `reset` both require a typed `yes`.
- **A browser client identity.** The paired device announces itself as
  Chrome rather than whatsmeow's default OS string of `whatsmeow` with an
  unknown platform type, which is also what the phone shows under Linked
  Devices. This only changes what is announced at pairing time: an
  already-paired session keeps its original identity until re-paired, and
  the user agent placeholders and on-wire behaviour are unchanged.
- **A WhatsApp Web version refresh.** `serve` and `setup` look up the
  current WhatsApp Web client version before connecting and apply it across
  every field that carries one, so the reported version and the build hash
  derived from it track a real release rather than whichever was vendored at
  build time. Best-effort on a 2-second timeout: a failure is reported and
  ignored, and a stale version still connects.
- **`get_blocklist`, `block_contact`, `unblock_contact`.** Reading, adding
  to, and removing from the account's block list. Blocking and unblocking go
  through the send gate but always draft, on every call, regardless of the
  trust list: trusting a recipient authorises auto-sending messages to them,
  which must not silently authorise changing their block status. Reading a
  contact's presence is not included.
- **`create_poll`.** A gated send that posts a poll — a question and two or
  more options, single- or multiple-choice — through the same draft-then-commit
  path as every other send. Reading who voted is not included: poll votes
  arrive as separate encrypted update messages that would need their own
  decryption and tallying, a feature in its own right.
- **`edit_message` and `delete_message`.** Two more gated sends. `edit_message`
  replaces the text of a message you sent, within WhatsApp's edit window;
  `delete_message` deletes a message for everyone (your own always, someone
  else's only as a group admin). Both take the same draft-then-commit path
  as every other send, and WhatsApp enforces the window and admin rules,
  reporting a failure as a send error.
- **A parent-process watchdog.** The stdio server also exits when its
  parent process goes away, not only when stdin closes, so an MCP client
  that crashes without cleanly closing the pipe does not leave an orphaned
  server holding the WhatsApp connection open. Where the parent id never
  changes after a parent exits (Windows), the watch is a safe no-op and
  stdin-close remains the signal.
- **Authentication for `--http`.** The streamable-HTTP transport now
  requires a bearer token, generated on first use, persisted owner-only to
  `.http-token`, and printed once for the operator to copy. Every request
  must additionally be addressed to a loopback Host, which blocks a browser
  from reaching the server by rebinding DNS to a loopback address. The stdio
  transport, which speaks only to its parent process, is unaffected.
- **An outbound file allowlist.** `send_media` and `send_voice_note` may
  only read files inside configured directories, defaulting to a dedicated
  `outbox` under the data directory and widened with `media_roots` in
  `config.json`. The send gate authorises a recipient and says nothing about
  the file a send names, so without this a manipulated model could attach
  any readable file to a recipient the user had already trusted. Paths are
  resolved before being checked, so a symlink is judged by its target; a
  refusal happens before a draft is minted or a rate-limit token spent, and
  names no path.
- **`fetch_older_messages`.** Asks the phone for messages from before the
  oldest one already stored in a chat, widening how far back that chat can
  be read and searched without re-pairing. Anchored on the oldest stored
  message, so calling it repeatedly pages further back. It messages nobody —
  it requests the user's own history from their own phone — so no send gate
  applies. Results arrive asynchronously through the same ingest path as
  pair-time sync, so the tool reports only that the request was accepted.
- **`setup --full-history`.** Asks the phone for up to ten years of history
  at pair time instead of whatsmeow's default of "recent" only, which the
  phone typically answers with about three months. The phone still decides
  what it actually sends. Only meaningful while pairing, and `setup` says so
  explicitly rather than ignoring the flag on an already-paired install.
- **Distribution:** GitHub Releases for six OS/architecture combinations, a
  one-line install script for macOS/Linux (`curl | sh`) and Windows (`irm |
  iex`), an `npx whatsapp-connect-mcp` wrapper, a per-platform MCPB bundle,
  and MCP Registry metadata.

### Known limitations

- This relies on the unofficial WhatsApp Web protocol (via
  [whatsmeow](https://github.com/tulir/whatsmeow)), not an official WhatsApp
  Business API. Meta bans numbers it detects on this protocol, and such bans
  are widely reported as permanent. The send gate and rate limiter cut the
  behavioral half of that risk. They cannot touch the other half, which is
  that the session is distinguishable from an official client; announcing a
  browser identity (below) defeats the most trivial form of that check and
  no more. Read the README's "Ban risk" section before pairing a number you
  care about.
- Not for business automation. Support bots, outreach, and auto-responders
  are the use case most consistently reported to get numbers banned, and are
  what the official WhatsApp Business API exists for.
- History depth is phone-decided: like any WhatsApp Web client, how much
  chat history syncs here is controlled by the paired phone.
- `send_voice_note` requires Ogg Opus input; there is no transcoding.
- No outbound calling — call history is readable, placing a call is not.
- One paired WhatsApp number per install.
- `--http` mode authenticates with a bearer token and accepts only loopback
  Host headers, but still bind it to `127.0.0.1` unless you are putting your
  own access control in front of it.
