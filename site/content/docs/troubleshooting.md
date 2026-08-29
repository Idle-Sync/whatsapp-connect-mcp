---
title: Troubleshooting
description: The failures people actually hit, and what each one means.
---

Start here:

```sh
whatsapp-connect-mcp doctor
```

`doctor` is also available as an MCP tool, so you can ask your agent to run it.

## `ConnectionRefused at http://127.0.0.1:2178`

You picked the http transport and the server is not running. Start it:

```sh
whatsapp-connect-mcp serve --http 127.0.0.1:2178
```

Or install it as a [service](/docs/service) so you stop having to.

## The client says the binary does not exist

You ran `setup` through `npx` and npm's cache has since been cleared. Install
globally and re-run `setup` — see [Install](/docs/install).

## Second client fails to start

stdio gives each client its own process, and only one can hold the data
directory's lock. Switch to [http](/docs/transports).

## It only has a few months of messages

That is your phone's decision, not a bug. See [Limitations](/docs/limitations).
Re-pair with `--full-history`, and use `fetch_older_messages` to page back.

## It stopped receiving messages

The paired session was probably dropped — usually because the phone unlinked the
device, or it was offline too long. Re-run `setup` to pair again.

## A send never arrived

Check whether it is still sitting at [the gate](/docs/send-gate) waiting for you,
and whether the rate limiter is holding it. `list_scheduled` shows anything
queued.

## Media will not send

`send_media` only reads from directories listed in `config.json`. Add the folder,
or move the file into one that is already allowed.
