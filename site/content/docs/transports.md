---
title: stdio and http
description: stdio is the default and simplest. http lets several clients connect at once, but you have to start the server yourself.
---

`setup` asks how clients should connect.

## stdio — the default

Each client starts its own copy of the server. Nothing to run, nothing to keep
alive.

The catch: **only one client or session at a time.** One `serve` process holds an
exclusive lock on the data directory, so a second client trying to start its own
copy will fail.

## http — several clients at once

Every selected client points at one shared local server on
`http://127.0.0.1:<port>` (default `2178`), authenticated with a bearer token.
Several clients and sessions can connect at the same time.

:::caution[There is a step 2: start the server]
Nothing starts it for you. Until it runs, every client reports something like
`ConnectionRefused at http://127.0.0.1:2178`.
:::

```sh
whatsapp-connect-mcp serve --http 127.0.0.1:2178
```

It acknowledges with `serve: listening on http://127.0.0.1:2178 …` and stays in
the foreground, so it dies with its terminal. To keep it alive across logouts and
reboots, see [Run as a service](/docs/service).

The token lives at `~/.config/whatsapp-connect-mcp/.http-token`.

## Which should you pick

Use **stdio** unless you actually need two clients at once. It has fewer moving
parts and nothing to forget to start.
