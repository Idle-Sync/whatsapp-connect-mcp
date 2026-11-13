package mcpserv

import "github.com/modelcontextprotocol/go-sdk/mcp"

// The tool annotations below are hints MCP clients use to decide how much
// care a call needs. Each tool gets one of these shapes, chosen from what
// its handler actually does; TestToolAnnotations pins the choice per tool.
//
// "Local" means the tool touches only this machine (the message store, the
// schedule file); "open world" means it talks to WhatsApp's servers or
// another outside service.

// readLocal: reads the local message or schedule store and changes nothing.
func readLocal() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}
}

// readLive: fetches from WhatsApp's servers and changes nothing.
func readLive() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)}
}

// fetchToDisk: fetches from WhatsApp and writes what it got to this machine
// (a file, or rows in the local store) without removing anything.
func fetchToDisk(idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: idempotent, OpenWorldHint: new(true)}
}

// sendNew: puts something new into a chat; calling it again sends again.
func sendNew() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(true)}
}

// setState: sets a state on WhatsApp (a reaction, an edit, read receipts,
// a block) that repeating the same call leaves unchanged.
func setState() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(true)}
}

// removes: takes something away that cannot be brought back.
func removes(openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(openWorld)}
}
