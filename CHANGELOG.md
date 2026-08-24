# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- New `backup` command: writes a consistent snapshot of the message
  database to `<data-dir>/backups/` (or `--dest`), safe to run while the
  server is up. Message history is the one thing re-pairing cannot
  recover.
- `service install|uninstall|restart` now works on Windows, using a Task
  Scheduler logon task. The server runs as a minimized console window;
  closing it stops the server. Creating the task may require an
  Administrator terminal.
- Connection-health diagnostics: dropped connections, keepalive timeouts,
  refused connects, temporary bans, and an outdated client now each print
  one clear line to the server log instead of passing silently.
- Failed writes of incoming events to the message store are now counted
  and reported (previously silent).
- `check` (and the doctor tool) gains an `ingest` finding that fails when
  incoming events are not reaching the message store, and the session
  finding now says when the server was logged out by WhatsApp mid-run.
- `status` now shows connection state, last event time, reconnect count,
  and ingest-failure count.

### Changed

- `serve --http` now starts its listener before waiting for pairing, so
  the server is reachable while unpaired: store-backed read tools serve
  local data, and live/send tools report "no longer paired" until pairing
  completes. stdio serve still waits for pairing before starting.

### Fixed

- A WhatsApp-side logout no longer leaves a running server permanently
  broken: the server now says clearly that it was logged out, waits, and
  reconnects on its own as soon as the device is paired again — no restart
  needed. Tool calls while unpaired fail with "no longer paired — run
  setup" instead of a generic connection error.
- A session-store failure while waiting for pairing (at startup, or after
  a logout) is no longer mistaken for a clean shutdown: it now prints a
  diagnostic and exits non-zero at startup, so a service manager restarts
  the server, and after a logout it says the re-check failed instead of
  going silent.

## [0.2.0] - 2026-08-19

The shared http server becomes a first-class way to run this: one command
installs it as a background service that survives reboots and updates, and
the doctor tells you precisely when you are behind.

### Added

- **`service install|uninstall|restart`** (#15). One command installs a
  background service for the shared http server — a launchd user agent
  on macOS, a systemd user unit on Linux — starts it, and keeps it
  across reboots; `restart` picks up an updated binary, `uninstall`
  removes it cleanly. The definition is rendered from the resolved
  binary path with an explicit PATH (node's directory included for npm
  installs), which kills the `env: node: No such file or directory`
  crash-loop launchd's minimal default PATH causes, and for npm
  installs the service execs the stable shim rather than the
  version-suffixed cached binary that changes on every update. The
  static templates under `packaging/` are gone — the subcommand is the
  single source of the unit content.

### Changed

- **The version check names the mismatch.** When a newer release exists,
  `check` and the `doctor` MCP tool now say which version is running and
  which is latest — instead of a bare "a newer version has been
  released" — and the fix line lists the actual update commands. The
  README gained an Updating section: every install method updates in
  place, and pairing, history, trust, and client configs are never
  touched by an update.

## [0.1.3] - 2026-08-19

### Fixed

- **npm publish from the release workflow works.** 0.1.2's `publish-npm`
  job failed: the npm-side trusted publisher connection was not yet
  saved, and the npm package's `repository.url` spelled the GitHub org
  in lowercase where trusted publishing's provenance check requires the
  exact `Idle-Sync` casing. 0.1.2 therefore never reached npm; this
  release is otherwise identical to it.

## [0.1.2] - 2026-08-19

### Added

- **npm distribution.** `npx whatsapp-connect-mcp setup` (or
  `npm install -g`) now works: the `whatsapp-connect-mcp` package on npm
  is a dependency-free installer that fetches the matching release
  binary on first run. 0.1.1 was published by hand; every release from
  here is published by the release workflow via npm trusted publishing
  (OIDC, no stored token), the same way the MCP Registry publish
  already works.

## [0.1.1] - 2026-08-19

Day-one polish from field reports of real agent-driven use: `serve` now
says when it is up, the http transport's second step is documented as an
actual step (with keep-alive units for Linux and macOS), trust-list edits
apply to a running server, and an unpaired `serve` waits instead of
crash-looping under a service manager.

### Added

- **Field-report round (#12).** From an evening of real agent-driven
  use:
  - `serve` no longer exits when unpaired: it waits idle (re-checking
    every 15s) so a service manager with `Restart=always` cannot
    crash-loop it, and pairing via `setup` is picked up without a
    restart. A sample systemd user unit ships in `packaging/systemd/`.
  - A server-side logout is now loud: whatsmeow deletes the device
    store the moment it happens (that is how a pairing silently
    vanishes), so the bridge reports the logout, its reason, and the
    re-pair instruction to stderr, along with stream-replaced
    conflicts (another process using the same session).
  - `fetch_older_messages` reports the previous request's fate on the
    next call: how many older messages actually arrived, or that
    nothing did.
  - `poll_new_messages` gained `tail: N` — the newest N messages
    immediately, plus a cursor to continue from.
  - `list_messages` renders row timestamps in the requested `tz`
    (still RFC 3339), not just UTC.
  - Voice notes, audio, and video notes carry `duration=Ns` in the row
    text; albums decode as kind `album`; the view-once re-encryption
    marker decodes as `view_once` with a readable-only-on-phone hint.

### Fixed

- **The http transport's second step is now an explicit step** (#13).
  Picking http in `setup` leaves clients pointed at a server nothing
  starts for them, but the README framed the whole product as one step
  and mentioned `serve --http` only mid-paragraph — so the first thing
  an http user saw was `ConnectionRefused`. The Install section now
  carries a "step 2: start the server" callout with the exact command,
  the headline says which path is one-step, and keeping the server
  alive is documented for both platforms: the existing systemd user
  unit is finally referenced, and a launchd agent for macOS ships in
  `packaging/launchd/` with install commands in its comments.

- **`serve` acknowledges a successful start** (#14). A start that went
  well printed nothing — with http especially, a blank terminal was
  indistinguishable from a hang, and people killed and re-ran a server
  that was fine. The HTTP listener now binds synchronously and only
  then announces `serve: listening on http://<addr>` (so the line is
  truthful, and a port-in-use error can no longer race the shutdown
  path and be swallowed); stdio prints `serve: ready on stdio`. Both
  lines go to stderr, so stdio framing on stdout is untouched.

- **`trust --add`/`--remove` apply to a running serve** (#11). The
  persistent trust list was read once at startup, so changing it
  required a restart — inconsistent with `trust --session`, which has
  always applied live. The gate now re-reads config.json's list on
  every send decision (the file is tiny and sends are rate-limited); a
  transiently broken file neither grants nor revokes — the last good
  read stays in force. Trust remains CLI-only: no MCP tool writes the
  file.

## [0.1.0] - 2026-08-18

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
