---
title: Install
description: One command downloads the release binary for your platform and runs setup.
---

Pick one. Each of these downloads the release binary for your OS and
architecture, then runs `setup`.

## macOS and Linux

```sh
curl -fsSL https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.sh | sh
```

## Windows

```powershell
irm https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.ps1 | iex
```

## With Node already installed

```sh
npm install -g whatsapp-connect-mcp
whatsapp-connect-mcp setup
```

:::caution[Prefer a global install over `npx` for `setup`]
`npx whatsapp-connect-mcp serve` is fine on its own. `setup` is different: it
writes an **absolute path to the running binary** into each MCP client's config.
Under `npx` that path lives inside npm's package cache. Clear the cache and every
config `setup` wrote now points at a binary that is gone. A global install keeps
the path stable until the package is upgraded or removed.
:::

## What `setup` does

1. Shows a QR code to scan — see [Pair your phone](/docs/pair).
2. Detects the MCP clients you already have installed and offers to add a
   `whatsapp` entry to whichever ones you tick.
3. Asks whether clients should connect over
   [stdio or http](/docs/transports).

You can re-run `setup` any time: to pair again, or to add a client you installed
later.

## Asking for more history

```sh
whatsapp-connect-mcp setup --full-history
```

This asks the phone for as much history as the protocol allows rather than the
default few months. It only has an effect **while actually pairing**, so an
install that is already paired must `remove` first. `setup` will say so rather
than silently ignoring the flag. The phone still decides what it really sends.
