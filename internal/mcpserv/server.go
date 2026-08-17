package mcpserv

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/version"
)

// Store is the read query API this package needs. It mirrors *store.Store's
// method set exactly, so *store.Store satisfies it without adapters.
type Store interface {
	Chats(query string, includeArchived bool, limit int) ([]store.ChatRow, error)
	Chat(jid string) (store.ChatRow, bool, error)
	Messages(chatJID string, beforeTS, afterTS int64, limit int) ([]store.MessageRow, error)
	SearchMessages(query, chatJID string, limit int) ([]store.MessageRow, error)
	MessageContext(chatJID, id string, before, after int) ([]store.MessageRow, error)
	SearchContacts(query string, limit int) ([]store.ContactRow, error)
	LastInteraction(jid string) (store.MessageRow, bool, error)
	Calls(peerJID string, limit int) ([]store.CallRow, error)
	MessageMediaRef(chatJID, id string) ([]byte, string, string, error)
}

// Live is the on-demand WhatsApp network access this package needs.
// Satisfied by *bridge.Bridge.
type Live interface {
	GroupParticipants(ctx context.Context, groupJID string) ([]string, error)
	DownloadMedia(ctx context.Context, ref []byte, destDir, filename string) (string, error)
}

// serverName and serverTitle identify this server to MCP clients.
const serverName = "whatsapp-connect-mcp"

// New builds an MCP server with the full tool surface registered: the
// read-only tools against st and live (media downloaded into dataDir), and
// the gated send tools against st and g, the sole path any of them has to
// an outbound WhatsApp send.
func New(st Store, live Live, g *gate.Gate, dataDir string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: version.Version,
	}, nil)

	registerReadTools(server, st, live, dataDir)
	registerSendTools(server, st, g)

	return server
}
