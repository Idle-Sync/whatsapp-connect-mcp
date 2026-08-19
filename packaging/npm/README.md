# whatsapp-connect-mcp

A WhatsApp MCP server shipped as a single static Go binary: pair once via
QR code, and any MCP client (Claude Desktop, Claude Code, Cursor, Windsurf,
Cline, …) can read, search, and send WhatsApp messages through it, with
every outbound send protected by a server-enforced gate.

This npm package is a thin installer: on first run it downloads the
[GitHub release binary](https://github.com/idle-sync/whatsapp-connect-mcp/releases)
matching this package's version and your platform (macOS, Linux, Windows —
x64 and arm64), caches it inside the package, and re-executes it with your
arguments. No dependencies, no postinstall scripts.

## Usage

```sh
npx whatsapp-connect-mcp setup
```

`setup` pairs via QR scan and injects a `whatsapp` server entry into the
MCP clients you pick. One caveat when installing through npm: `setup`
writes an absolute path to the running binary into each client's config,
and under `npx` that path lives inside npm's cache — clear the cache and
those configs break. A global install keeps the path stable between runs:

```sh
npm install -g whatsapp-connect-mcp
whatsapp-connect-mcp setup
```

For the full picture — the send gate, trust model, ban-risk notes, http
transport, and every command — see the
[project README](https://github.com/idle-sync/whatsapp-connect-mcp#readme).
