---
title: Connect your client
description: setup detects your MCP clients and writes the config for you. Here is what it writes, in case you would rather do it by hand.
---

`setup` detects the MCP clients you have installed and offers to add a `whatsapp`
entry to whichever ones you pick. It currently knows about Claude Desktop, Claude
Code, Cursor, Windsurf and Cline.

If your client was not detected, or you would rather do it yourself, here is what
each one wants.

## Claude Code

```sh
claude mcp add whatsapp /path/to/whatsapp-connect-mcp serve
```

Or, if you chose the [http transport](/docs/transports):

```sh
claude mcp add --transport http whatsapp http://127.0.0.1:2178 \
  --header "Authorization: Bearer $WCM_TOKEN"
```

## Claude Desktop, Cursor, Windsurf, Cline

All four take the same shape, in their own config file, under `mcpServers`:

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "/path/to/whatsapp-connect-mcp",
      "args": ["serve"]
    }
  }
}
```

For http, point at the running server instead:

```json
{
  "mcpServers": {
    "whatsapp": {
      "type": "streamableHttp",
      "url": "http://127.0.0.1:2178",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

The bearer token lives at `~/.config/whatsapp-connect-mcp/.http-token`.

:::caution[Use an absolute path]
Whatever writes the config, the `command` must be an absolute path to a binary
that will still be there tomorrow. See the warning about `npx` in
[Install](/docs/install).
:::

## Checking it worked

Ask your client to run the `doctor` tool, or from a terminal:

```sh
whatsapp-connect-mcp doctor
```

If the client reports `ConnectionRefused at http://127.0.0.1:2178`, you picked
the http transport and have not started the server. See
[stdio and http](/docs/transports).
