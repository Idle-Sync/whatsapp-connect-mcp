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
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/idle-sync/whatsapp-connect-mcp/internal/mediapath"
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

	// mediaRoots confines which local files an outbound send may read. Its
	// zero value allows nothing, so a Bridge opened without roots (setup,
	// status, and the other non-sending commands) cannot send media at all
	// — the safe direction for a caller that never needed to.
	mediaRoots mediapath.Roots

	handlerOnce sync.Once
	// handlerRegistrations counts how many times ensureHandlerRegistered
	// actually registered the event handler (as opposed to how many times
	// it was called). It exists so tests can prove the registration is a
	// true no-op on repeat calls, which is what guarantees a second
	// Connect/PairQR can never double-dispatch events.
	handlerRegistrations int
}

// errInvalidRecipient is returned whenever a caller-supplied JID string
// cannot be parsed. It is deliberately generic per the invariant that
// errors never carry the JID that failed.
var errInvalidRecipient = errors.New("invalid recipient")

// Open opens (creating if necessary) the whatsmeow session store at
// <dataDir>/session.db and constructs a Bridge that decodes inbound events
// into st. It does not connect to WhatsApp; call Connect or PairQR for
// that.
//
// roots confines which local files outbound media sends may read. Callers
// that never send (setup, status, doctor) should pass the zero value, which
// denies everything.
func Open(ctx context.Context, dataDir string, st Ingest, roots mediapath.Roots) (*Bridge, error) {
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
	b := &Bridge{client: client, container: container, store: st, dataDir: dataDir, mediaRoots: roots}
	// Registered here, once, rather than in Connect: PairQR also needs
	// inbound events flowing (history sync can start arriving mid-pairing),
	// and registering in exactly one place removes any chance of a second
	// Connect call adding a duplicate handler.
	b.ensureHandlerRegistered()
	return b, nil
}

// ensureHandlerRegistered registers handleEvent with the whatsmeow client.
// It is safe to call more than once (from Open, Connect, and PairQR): only
// the first call actually registers anything, so the client's event
// dispatch can never end up with two copies of the same handler.
func (b *Bridge) ensureHandlerRegistered() {
	b.handlerOnce.Do(func() {
		b.client.AddEventHandler(b.handleEvent)
		b.handlerRegistrations++
	})
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
	b.ensureHandlerRegistered()

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

// Connect brings an already-paired client online. The inbound event handler
// is already registered (Open does that once, up front), so calling
// Connect more than once cannot cause events to be dispatched twice.
func (b *Bridge) Connect(ctx context.Context) error {
	b.ensureHandlerRegistered()
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

// RequestOlderMessages asks the paired phone to send up to count messages
// from before the message identified by (chatJID, msgID, fromMe, ts), which
// must be one this store already holds — the phone anchors its reply on it.
//
// This is a peer message to the user's own account, not a message to any
// contact: nothing is delivered to anyone, and no send gate applies. It
// requests our own history from our own phone.
//
// The call returns as soon as the request is accepted. Whatever the phone
// decides to send arrives later as an ordinary history-sync event and is
// ingested through the same path as pair-time sync, so a caller learns the
// result by reading the store again rather than from a return value here.
// The phone may answer with fewer messages than asked for, or none at all.
//
// The anchor is passed as separate values rather than a struct so that this
// package stays absent from mcpserv's imports: mcpserv's Live interface is
// satisfied structurally, and naming a type here would end that.
func (b *Bridge) RequestOlderMessages(
	ctx context.Context, chatJID, msgID string, fromMe bool, ts int64, count int,
) error {
	chat, err := parseRecipient(chatJID)
	if err != nil {
		return err
	}

	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: fromMe},
		ID:            msgID,
		Timestamp:     time.Unix(ts, 0),
	}
	if _, err := b.client.SendPeerMessage(ctx, b.client.BuildHistorySyncRequest(info, count)); err != nil {
		return waErr("request older messages", err)
	}
	return nil
}

// DownloadMedia fetches the media referenced by ref (as produced by
// decodeMessage's MediaRef, a marshaled waE2E.Message) and writes it to
// destDir/filename, returning the written path. filename is untrusted: it
// originates from the remote sender (the WhatsApp message's own file name
// field), not this program, so it is validated to a single safe path
// component — see sanitizeMediaFilename — before any network call and
// before it is ever joined onto destDir. A caller that has already
// sanitized filename (e.g. mcpserv's tool boundary) still passes through
// this same check: DownloadMedia must never trust that a caller did.
func (b *Bridge) DownloadMedia(ctx context.Context, ref []byte, destDir, filename string) (string, error) {
	var msg waE2E.Message
	if err := proto.Unmarshal(ref, &msg); err != nil {
		return "", errors.New("invalid media reference")
	}

	safeName, err := sanitizeMediaFilename(filename)
	if err != nil {
		return "", err
	}

	data, err := b.downloadMessage(ctx, &msg)
	if err != nil {
		return "", waErr("download media", err)
	}

	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", fileErr("create media directory", err)
	}

	path := filepath.Join(destDir, safeName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fileErr("write media file", err)
	}
	return path, nil
}

// sanitizeMediaFilename validates filename before it becomes a path
// component. filename must already be a single path component: if
// filepath.Base(filename) differs from filename at all (any directory
// prefix, "..", a trailing separator) or reduces to empty, ".", or "..",
// it is rejected outright rather than silently normalized to its base
// name — a crafted filename (e.g. "../../../../evil.txt") is a category
// error here, not a filename quietly rewritten to "evil.txt". Only a bare
// filename passes; nothing about it lets the caller address any path
// outside the directory it is joined onto.
func sanitizeMediaFilename(filename string) (string, error) {
	base := filepath.Base(filename)
	if base != filename || base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", errors.New("invalid media filename")
	}
	return base, nil
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
