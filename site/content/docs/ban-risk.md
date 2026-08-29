---
title: Ban risk
description: What the public evidence actually shows about numbers being banned for using third-party WhatsApp clients.
---

<hr class="hazard-rule" />

This is the page to read before you scan the QR code.

## The short version

whatsapp-connect-mcp talks to WhatsApp the way WhatsApp Web does, through
[whatsmeow](https://github.com/tulir/whatsmeow). It is not the official WhatsApp
Business API. **Meta bans numbers it detects using third-party clients on this
protocol, and such bans are widely reported as permanent.**

Pair a number you would be comfortable losing. Not the one your bank and your
family use.

## The two halves of the risk

**Behaviour.** Sending at volume, messaging people who never messaged you first,
automated replies, anything that looks like marketing. This is the half most
consistently linked to bans in public reports, and it is the half
[the send gate](/docs/send-gate) and the rate limiter are designed to remove.

**Identification.** Being a third-party client at all. Nothing in this project
can change that, and no one outside Meta knows how aggressively it is detected or
acted on. Anyone who tells you the risk is zero is guessing.

## How to keep the risk as low as it can go

- Pair a spare number, or one you would not be devastated to lose.
- Use it for **your own** messages. Reading your history is the point; sending is
  a convenience.
- Never reply to people who did not message you first.
- Do not run it as a bot, a support desk, or an auto-responder.
- Leave the rate limiter alone.

## If you need this for a business

Use the official WhatsApp Business API. It exists precisely for the use case this
tool is wrong for, and it will not get your number banned.

<hr class="hazard-rule" />
