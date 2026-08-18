# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

whatsapp-connect-mcp is a WhatsApp MCP server shipped as a single static Go
binary: pair once via QR code, and any MCP client can read, search, and send
WhatsApp messages through it.

### Added

- **Sixteen MCP tools.** Eleven read-only — `list_chats`, `get_chat`,
  `list_messages`, `search_messages`, `get_message_context`,
  `search_contacts`, `get_last_interaction`, `list_group_participants`,
  `get_call_history`, `download_media`, `doctor` — plus five gated sends:
  `send_message`, `send_media`, `send_voice_note`, `send_reaction`,
  `mark_read`. Every WhatsApp-originated result (messages, names, captions,
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
- **Distribution:** GitHub Releases for six OS/architecture combinations, a
  one-line install script for macOS/Linux (`curl | sh`) and Windows (`irm |
  iex`), an `npx whatsapp-connect-mcp` wrapper, a per-platform MCPB bundle,
  and MCP Registry metadata.

### Known limitations

- This relies on the unofficial WhatsApp Web protocol (via
  [whatsmeow](https://github.com/tulir/whatsmeow)), not an official WhatsApp
  Business API. Meta bans numbers it detects on this protocol, and such bans
  are widely reported as permanent. The send gate and rate limiter cut the
  behavioral half of that risk; they cannot touch the other half, which is
  that this client is identifiable as a third-party client at all. Read the
  README's "Ban risk" section before pairing a number you care about.
- Not for business automation. Support bots, outreach, and auto-responders
  are the use case most consistently reported to get numbers banned, and are
  what the official WhatsApp Business API exists for.
- History depth is phone-decided: like any WhatsApp Web client, how much
  chat history syncs here is controlled by the paired phone.
- `send_voice_note` requires Ogg Opus input; there is no transcoding.
- No outbound calling — call history is readable, placing a call is not.
- One paired WhatsApp number per install.
- `--http` mode has no authentication of its own; bind it to `127.0.0.1`
  unless you're putting your own access control in front of it.
