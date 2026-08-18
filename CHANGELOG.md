# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

whatsapp-connect-mcp is a WhatsApp MCP server shipped as a single static Go
binary: pair once via QR code, and any MCP client can read, search, and send
WhatsApp messages through it.

### Added

- **Nineteen MCP tools.** Twelve read-only — `list_chats`, `get_chat`,
  `list_messages`, `search_messages`, `get_message_context`,
  `search_contacts`, `get_last_interaction`, `list_group_participants`,
  `get_call_history`, `download_media`, `fetch_older_messages`, `doctor` —
  plus seven gated sends:
  `send_message`, `send_media`, `send_voice_note`, `send_reaction`,
  `edit_message`, `delete_message`, `mark_read`. Every WhatsApp-originated result (messages, names, captions,
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
