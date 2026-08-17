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
