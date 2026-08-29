---
title: What this is
description: A WhatsApp MCP server that runs on your own machine, so an agent can read and search your messages, with every outbound send held at a gate.
---

whatsapp-connect-mcp is a single Go binary. You pair it once with a QR code, and
any MCP client — Claude Desktop, Claude Code, Cursor, Windsurf, Cline — can read,
search and act on your WhatsApp messages through it.

Nothing is uploaded. The binary keeps your messages in a SQLite file next to
itself and serves your client over loopback. There is no account and no server of
ours in the middle.

:::danger[Read this before you pair a number you care about]
This speaks the WhatsApp Web protocol through
[whatsmeow](https://github.com/tulir/whatsmeow). It is **not** the official
WhatsApp Business API. Meta bans numbers it detects on third-party clients, and
those bans are widely reported as permanent. [Ban risk](/docs/ban-risk) lays out
what the public evidence actually shows. Pair a number you can afford to lose.
:::

## What it is good at

- **Searching your own history** with real date filters, and showing you the
  messages either side of a hit rather than the hit alone.
- **Catching up.** Point a model at the four hundred messages a group piled up
  while you were away and ask what happened.
- **Reading your own attachments.** `download_media` pulls the invoices,
  receipts and screenshots people sent you onto disk where a model can read them.
- **Finding loose ends.** `get_last_interaction` answers "who messaged me that I
  never replied to?"
- **Drafting replies.** The model writes it, [the gate](/docs/send-gate) makes
  you confirm it, then it sends.

## What it is not for

Do not build a support bot, an outreach tool, or an auto-responder on this.
Messaging people who never messaged you first, at volume, is the behaviour most
consistently reported to get numbers banned — and it is exactly the use case Meta
sells the Business API for. Point this at customers and you will lose the number.

## Next

1. [Install](/docs/install) — one command.
2. [Pair your phone](/docs/pair) — one QR code.
3. [Connect your client](/docs/clients) — `setup` does it for you.
