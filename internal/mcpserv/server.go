package mcpserv

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/gate"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
	"github.com/idle-sync/whatsapp-connect-mcp/internal/version"
)

// DoctorEnv holds the values the doctor tool needs beyond what the other
// read tools already share (the store and the data directory): the local
// values only a diagnostic check has any use for.
type DoctorEnv struct {
	Home         string
	BinaryPath   string
	NeedsPairing func() bool
	LoggedIn     func() bool
	// LastEventAt and OpenedAt feed doctor's event-flow (ingestion
	// liveness) check; nil skips it.
	LastEventAt func() time.Time
	OpenedAt    func() time.Time
	// IngestErrors reports how many inbound WhatsApp events failed to
	// write to the message store since the server started; nil skips the
	// ingest check. LastDisconnect is the category of the most recent
	// involuntary disconnect ("" if none); checkSession uses it to name a
	// mid-run logout. Both are *bridge.Bridge methods of those names.
	IngestErrors   func() uint64
	LastDisconnect func() string
}

// Store is the read query API this package needs. It mirrors *store.Store's
// method set exactly, so *store.Store satisfies it without adapters.
// QuickCheck is here (rather than a separate doctor-only interface at this
// package's boundary) because it is the same store value every other tool
// already receives — the doctor tool's database check just needs one more
// method exposed from it.
type Store interface {
	Chats(query string, includeArchived bool, limit int) ([]store.ChatRow, error)
	Chat(jid string) (store.ChatRow, bool, error)
	Messages(chatJID string, beforeTS, afterTS int64, limit int) ([]store.MessageRow, error)
	SearchMessages(query, chatJID string, limit int) ([]store.MessageRow, error)
	MessageContext(chatJID, id string, before, after int) ([]store.MessageRow, error)
	SearchContacts(query string, limit int) ([]store.ContactRow, error)
	LastInteraction(jid string) (store.MessageRow, bool, error)
	OldestMessage(chatJID string) (store.MessageRow, bool, error)
	CountMessagesOlderThan(chatJID string, ts int64) (int, error)
	LatestRowID() (int64, error)
	MessagesAfterRowID(chatJID string, afterRowID int64, includeOwn bool, limit int) ([]store.MessageRow, int64, error)
	TailRowID(chatJID string, includeOwn bool, n int) (int64, error)
	Calls(peerJID string, beforeTS, afterTS int64, limit int) ([]store.CallRow, error)
	MessageMediaRef(chatJID, id string) ([]byte, string, string, error)
	MediaMessageIDs(chatJID string, beforeTS, afterTS int64, kind string, limit int) ([]string, error)
	QuickCheck() error
}

// Live is the on-demand WhatsApp network access this package needs.
// Satisfied by *bridge.Bridge.
type Live interface {
	// WaitForCatchUp blocks until the offline queue WhatsApp redelivers
	// after a (re)connect has drained (bounded by a grace deadline), so a
	// store read that follows reflects messages that arrived while the
	// server was down. A no-op in the steady state.
	WaitForCatchUp(ctx context.Context)
	GroupParticipants(ctx context.Context, groupJID string) ([]string, error)
	GroupInfo(ctx context.Context, groupJID string) (subject, description, ownerJID string, admins []string, err error)
	Blocklist(ctx context.Context) ([]string, error)
	DownloadMedia(ctx context.Context, ref []byte, destDir, filename string) (string, error)
	RequestOlderMessages(ctx context.Context, chatJID, msgID string, fromMe bool, ts int64, count int) error
}

// serverName and serverTitle identify this server to MCP clients.
const serverName = "whatsapp-connect-mcp"

// New builds an MCP server with the full tool surface registered: the
// read-only tools against st and live (media downloaded into dataDir), the
// doctor tool against st and doc, and the gated send tools against st and
// g, the sole path any of them has to an outbound WhatsApp send.
func New(st Store, live Live, g *gate.Gate, sched *Scheduler, dataDir string, doc DoctorEnv) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: version.Version,
	}, nil)

	registerReadTools(server, st, live, dataDir, doc)
	registerSendTools(server, st, g)
	if sched != nil {
		registerScheduleTools(server, st, sched)
	}

	return server
}
