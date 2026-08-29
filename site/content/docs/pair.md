---
title: Pair your phone
description: Scan one QR code from Linked Devices. Your phone stays the source of truth.
---

`setup` prints a QR code in your terminal. On your phone:

1. Open WhatsApp.
2. **Settings → Linked Devices → Link a Device.**
3. Point it at the QR code in your terminal.

That is the whole pairing step. The binary now holds a session the same way
WhatsApp Web does, and starts syncing your history into a local SQLite file.

## How much history you get

Your phone decides, not this server. "Search my whole history" can turn out to
mean "search the last few months". Pass `--full-history` while pairing to ask for
as much as the protocol allows, and use `fetch_older_messages` afterwards to page
further back one chat at a time.

See [Limitations](/docs/limitations) for what that means in practice.

## Pairing again

```sh
whatsapp-connect-mcp setup
```

Re-running `setup` re-pairs. To start completely clean:

```sh
whatsapp-connect-mcp remove
```

:::caution
`remove` deletes the paired session and the local database. Your messages on the
phone are untouched, but everything this server had synced is gone and has to be
fetched again — subject to whatever the phone is willing to send this time.
:::
