// Package bridge wraps a whatsmeow session: pairing, connection, decoding
// inbound events into store-shaped records, outbound sends, and on-demand
// media download. It is the only package that imports whatsmeow.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// Ingest is the subset of store.Store's write API the bridge needs to
// persist decoded WhatsApp events and outbound read receipts. Satisfied by
// *store.Store.
type Ingest interface {
	UpsertChat(jid, name string, isGroup bool, lastMessageAt int64) error
	UpsertMessage(m store.Message) error
	UpsertContact(jid, phone, pushName, fullName, businessName string) error
	MarkRead(chatJID string, ids []string, readAt int64) error
	InsertCall(id, peerJID string, ts int64, direction, status string, isVideo bool) error
}

// Bridge wraps a whatsmeow client bound to a session stored under dataDir
// and an Ingest sink for decoded events. Zero value is not usable;
// construct with Open.
type Bridge struct {
	client    *whatsmeow.Client
	container *sqlstore.Container
	store     Ingest
	dataDir   string
}

// errInvalidRecipient is returned whenever a caller-supplied JID string
// cannot be parsed. It is deliberately generic per the invariant that
// errors never carry the JID that failed.
var errInvalidRecipient = errors.New("invalid recipient")

// Open opens (creating if necessary) the whatsmeow session store at
// <dataDir>/session.db and constructs a Bridge that decodes inbound events
// into st. It does not connect to WhatsApp; call Connect or PairQR for
// that.
func Open(ctx context.Context, dataDir string, st Ingest) (*Bridge, error) {
	dbPath := filepath.ToSlash(filepath.Join(dataDir, "session.db"))
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
		dbPath,
	)

	container, err := sqlstore.New(ctx, "sqlite", dsn, waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}

	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}

	client := whatsmeow.NewClient(device, waLog.Noop)
	return &Bridge{client: client, container: container, store: st, dataDir: dataDir}, nil
}

// DataDir returns the directory Open was called with.
func (b *Bridge) DataDir() string {
	return b.dataDir
}

// Close disconnects the client, if connected, and closes the underlying
// session database.
func (b *Bridge) Close() error {
	b.client.Disconnect()
	if err := b.container.Close(); err != nil {
		return fmt.Errorf("close session store: %w", err)
	}
	return nil
}

// NeedsPairing reports whether the session store has no paired device yet,
// i.e. PairQR must be run before Connect will do anything useful.
func (b *Bridge) NeedsPairing() bool {
	return b.client.Store.ID == nil
}

// LoggedIn reports whether the client is currently connected and
// authenticated with WhatsApp.
func (b *Bridge) LoggedIn() bool {
	return b.client.IsLoggedIn()
}

// PairQR runs the QR-code pairing flow: it connects the (unpaired) client
// and invokes show with each QR code payload as WhatsApp issues it, until
// the phone scans one and pairing succeeds, an error occurs, or ctx is
// cancelled. Must be called before Connect, and only when NeedsPairing is
// true.
func (b *Bridge) PairQR(ctx context.Context, show func(code string)) error {
	qrChan, err := b.client.GetQRChannel(ctx)
	if err != nil {
		return waErr("start pairing", err)
	}
	if err := b.client.ConnectContext(ctx); err != nil {
		return waErr("connect", err)
	}

	for item := range qrChan {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			show(item.Code)
		case "success":
			return nil
		default:
			return fmt.Errorf("pairing failed: %s", item.Event)
		}
	}
	return errors.New("pairing channel closed unexpectedly")
}

// Connect registers the inbound event handler and brings an already-paired
// client online.
func (b *Bridge) Connect(ctx context.Context) error {
	b.client.AddEventHandler(b.handleEvent)
	if err := b.client.ConnectContext(ctx); err != nil {
		return waErr("connect", err)
	}
	return nil
}

// GroupParticipants returns the member JIDs of the given group, fetched
// live from WhatsApp.
func (b *Bridge) GroupParticipants(ctx context.Context, groupJID string) ([]string, error) {
	jid, err := parseRecipient(groupJID)
	if err != nil {
		return nil, err
	}

	info, err := b.client.GetGroupInfo(ctx, jid)
	if err != nil {
		return nil, waErr("fetch group participants", err)
	}

	participants := make([]string, len(info.Participants))
	for i, p := range info.Participants {
		participants[i] = p.JID.String()
	}
	return participants, nil
}

// DownloadMedia fetches the media referenced by ref (as produced by
// decodeMessage's MediaRef, a marshaled waE2E.Message) and writes it to
// destDir/filename, returning the written path.
func (b *Bridge) DownloadMedia(ctx context.Context, ref []byte, destDir, filename string) (string, error) {
	var msg waE2E.Message
	if err := proto.Unmarshal(ref, &msg); err != nil {
		return "", errors.New("invalid media reference")
	}

	data, err := b.downloadMessage(ctx, &msg)
	if err != nil {
		return "", waErr("download media", err)
	}

	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", fileErr("create media directory", err)
	}

	path := filepath.Join(destDir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G304 -- destDir/filename are caller-controlled within the app data dir, not network input
		return "", fileErr("write media file", err)
	}
	return path, nil
}

// downloadMessage fetches the media attachment carried by msg. Client.Download
// requires the specific attachment type rather than the whole message
// (its deprecated DownloadAny wrapper does this same dispatch internally).
func (b *Bridge) downloadMessage(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
	switch {
	case msg.GetImageMessage() != nil:
		return b.client.Download(ctx, msg.GetImageMessage())
	case msg.GetVideoMessage() != nil:
		return b.client.Download(ctx, msg.GetVideoMessage())
	case msg.GetAudioMessage() != nil:
		return b.client.Download(ctx, msg.GetAudioMessage())
	case msg.GetDocumentMessage() != nil:
		return b.client.Download(ctx, msg.GetDocumentMessage())
	case msg.GetStickerMessage() != nil:
		return b.client.Download(ctx, msg.GetStickerMessage())
	default:
		return nil, whatsmeow.ErrNothingDownloadableFound
	}
}

// parseRecipient parses s as a JID, rejecting the empty JID. The returned
// error never includes s.
func parseRecipient(s string) (types.JID, error) {
	jid, err := types.ParseJID(s)
	if err != nil || jid.User == "" {
		return types.JID{}, errInvalidRecipient
	}
	return jid, nil
}

// waErr classifies an error from a whatsmeow client call into a
// category-only message safe to surface through gate and MCP tool results.
// The underlying error text is never included: whatsmeow's own error
// types can embed raw protocol nodes carrying JIDs or message content.
func waErr(op string, err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: timed out", op)
	case errors.Is(err, whatsmeow.ErrNotConnected), errors.Is(err, whatsmeow.ErrNotLoggedIn):
		return fmt.Errorf("%s: not connected to WhatsApp", op)
	case errors.Is(err, whatsmeow.ErrIQNotFound), errors.Is(err, whatsmeow.ErrIQNotAuthorized), errors.Is(err, whatsmeow.ErrIQForbidden):
		return fmt.Errorf("%s: recipient not found or not reachable", op)
	default:
		return fmt.Errorf("%s: WhatsApp request failed", op)
	}
}

// fileErr classifies a local filesystem error into a category-only message
// that never includes the path involved.
func fileErr(op string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s: file not found", op)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%s: permission denied", op)
	default:
		return fmt.Errorf("%s: file error", op)
	}
}
