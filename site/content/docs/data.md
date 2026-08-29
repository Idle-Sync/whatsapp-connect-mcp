---
title: Your data on disk
description: Where your messages live, what is in each file, and how to back it up or delete it.
---

Everything lives in one directory:

| Platform | Path |
|---|---|
| Linux | `~/.config/whatsapp-connect-mcp/` |
| macOS | `~/Library/Application Support/whatsapp-connect-mcp/` |
| Windows | `%APPDATA%\whatsapp-connect-mcp\` |

Inside it:

| File | What it is |
|---|---|
| `messages.db` | Your synced messages, in SQLite. The big one. |
| `session.db` | The paired device session. Treat this like a password. |
| `media/` | Anything `download_media` has pulled down. |
| `config.json` | Port, allowed media directories, gate settings. |
| `.http-token` | Bearer token for the [http transport](/docs/transports). |
| `outbox/`, `schedules.json` | Pending and scheduled sends. |

## Backing up

Stop the server first, then copy the whole directory. `messages.db` is SQLite in
WAL mode, so copying it while the server is running can give you a torn file.

## Deleting everything

```sh
whatsapp-connect-mcp remove
```

This unpairs the device and deletes the local database. Messages on your phone
are untouched.

:::tip[It is all local]
There is no cloud copy to delete, because there was never one made. Deleting this
directory is the complete deletion.
:::
