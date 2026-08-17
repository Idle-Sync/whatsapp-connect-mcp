# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project scaffold: Go module, lint config, CI workflow, version package, minimal CLI entry point.
- Config package: data directory resolution and atomic `config.json` load/save with trusted-JID lookup.
- Store package: SQLite open with WAL journaling and numbered migrations, schema 1 (chats, messages, contacts, calls, FTS5 message search kept in sync by triggers).
- Store package: idempotent ingest upserts for chats, messages, contacts, and calls, plus mark-read, safe for redelivery of the same WhatsApp event.
- Store package: bounded, newest-first queries for chats, messages, contacts, and calls; FTS5 message search (chat-scoped or global); message context windows; last-interaction lookup; media reference retrieval.
- Gate package: the sole path to an outbound send — draft-first preview with a 5 minute TTL and a 32-draft cap, auto-commit for trusted JIDs, and a non-blocking rate limiter with a hard 5 second floor ahead of every delivery.
- Bridge package: whatsmeow session wrapper — QR pairing, connect, decoding inbound messages/receipts/calls/history-sync/contact/group-rename events into the store, gated outbound delivery (text, media, voice notes, reactions, read receipts), group participant lookup, and on-demand media download.
- Store package: `UpsertChat` now keeps a chat's existing name when an event supplies an empty one, instead of blanking it.
- Gate package: `Delivery` gained an `Author` field (the target message's sender) so reactions and read receipts identify the right message in group chats.
- Store package: schema 1's tables (chats, messages, contacts, calls, schema_version) are now `STRICT`, so a wrong-typed value is rejected by the database instead of silently coerced; every deliberate SQLite-specific construct (FTS5, PRAGMA DSN options, rowid use inside the FTS trigger wiring) is now marked with its Postgres equivalent.
- MCP server package: server construction on the official `github.com/modelcontextprotocol/go-sdk`, the untrusted-data banner every WhatsApp-originated tool result is wrapped in, and the ten read-only tools (`list_chats`, `get_chat`, `list_messages`, `search_messages`, `get_message_context`, `search_contacts`, `get_last_interaction`, `list_group_participants`, `get_call_history`, `download_media`) with a documented default/max row limit and unknown-parameter rejection on every tool's input schema.
- Bridge and MCP server packages: `download_media`'s filename comes from the remote WhatsApp sender, not this program; both the bridge's `DownloadMedia` and the `download_media` tool now reject one that isn't a bare, single-component name (e.g. `../../../../evil.txt`) with a category error instead of joining it onto the destination directory unsanitized.
- Store package: `DefaultLimit`, `MaxLimit`, `ClampLimit`, and `ClampContext` are now exported, so the MCP server's tool-boundary limit clamp shares Store's single set of numbers and logic instead of a second copy.
- MCP server package: the five gated send tools (`send_message`, `send_media`, `send_voice_note`, `send_reaction`, `mark_read`), each mapping its input to a `gate.Delivery` and returning a banner-wrapped preview plus either a `draft_token` and confirmation instruction (unsent) or a `message_id` (sent); `send_media` rejects a local file that doesn't exist before drafting, `send_voice_note` additionally rejects a non-`.ogg` extension at the tool boundary; `send_reaction` and `mark_read` resolve the target message's author via `MessageContext`, with `mark_read` grouping message ids by sender since WhatsApp read receipts are sent per-sender; preview names resolve primarily through `Chat` (the display name of the recipient's own chat), falling back to `SearchContacts` only when the chat carries no name; every draftable tool's description covers retrying a rate-limited commit with the same `draft_token`.
