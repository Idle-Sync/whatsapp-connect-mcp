// Package bridge wraps a whatsmeow session: pairing, connection, decoding
// inbound events into store-shaped records, outbound sends, and on-demand
// media download. It is the only package that imports whatsmeow.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
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
	UpsertLIDMapping(lid, pn string) error
	MarkRead(chatJID string, ids []string, readAt int64) error
	InsertCall(id, peerJID string, ts int64, direction, status string, isVideo bool) error
}

// Bridge wraps a whatsmeow client bound to a session stored under dataDir
// and an Ingest sink for decoded events. Zero value is not usable;
// construct with Open.
type Bridge struct {
	container *sqlstore.Container
	store     Ingest
	dataDir   string

	// mediaRoots confines which local files an outbound send may read. Its
	// zero value allows nothing, so a Bridge opened without roots (setup,
	// status, and the other non-sending commands) cannot send media at all
	// — the safe direction for a caller that never needed to.
	mediaRoots mediapath.Roots

	// openedAt and lastEventAt are the ingestion-liveness signals behind
	// LastEventAt/OpenedAt: doctor's event-flow check compares them against
	// the connection state to notice a stalled event pipeline (a socket
	// that stays "connected" while no events reach handleEvent).
	openedAt    time.Time
	lastEventAt atomic.Int64

	// Catch-up state for WaitForCatchUp. catchUpSeq orders Connected and
	// OfflineSyncCompleted events (a shared counter, so ordering never
	// depends on clock resolution): the session is caught up when the
	// latest completion came after the latest connect. connectedAtMs
	// anchors the grace deadline for a session whose completion marker
	// never arrives. catchUpGrace is a field (defaulted in Open) so tests
	// can shorten it.
	catchUpSeq     atomic.Int64
	connectedSeq   atomic.Int64
	offlineSyncSeq atomic.Int64
	connectedAtMs  atomic.Int64
	catchUpGrace   time.Duration

	// diag receives operator-facing connection diagnostics (logouts,
	// stream conflicts) — the events that explain why a pairing vanished
	// or ingestion stopped. Local console/journal output only, never tool
	// results.
	diag io.Writer

	// clientMu guards client and handlerRegistrations. The client is
	// swapped exactly twice in a Bridge's life at most: once at Open and
	// once per logout re-initialization; every other access is a read.
	clientMu sync.RWMutex
	client   *whatsmeow.Client
	// handlerRegistrations counts how many times setClient has built a
	// client and registered the event handler on it. It exists so tests can
	// prove every client setClient ever produces gets the handler exactly
	// once, which is what guarantees no call sequence can double-dispatch
	// events.
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

	b := &Bridge{
		container: container, store: st, dataDir: dataDir,
		mediaRoots: roots, openedAt: time.Now(), catchUpGrace: defaultCatchUpGrace,
		diag: os.Stderr,
	}
	b.setClient(device)
	return b, nil
}

// setClient builds a whatsmeow client for device, registers handleEvent on
// it, and makes it the Bridge's current client. It is the only way a
// client comes to exist on a Bridge, which is what guarantees every client
// has the handler registered exactly once — no call sequence of Connect
// and PairQR can ever double-dispatch events.
func (b *Bridge) setClient(device *waStore.Device) {
	c := whatsmeow.NewClient(device, waLog.Noop)
	c.AddEventHandler(b.handleEvent)

	b.clientMu.Lock()
	b.client = c
	b.handlerRegistrations++
	b.clientMu.Unlock()
}

// wa returns the current whatsmeow client. Callers use it for one
// operation and take it fresh next time — retaining it would pin a client
// a logout re-initialization has already replaced.
func (b *Bridge) wa() *whatsmeow.Client {
	b.clientMu.RLock()
	defer b.clientMu.RUnlock()
	return b.client
}

// Close disconnects the client, if connected, and closes the underlying
// session database.
func (b *Bridge) Close() error {
	b.wa().Disconnect()
	if err := b.container.Close(); err != nil {
		return fmt.Errorf("close session store: %w", err)
	}
	return nil
}

// defaultCatchUpGrace bounds how long WaitForCatchUp will hold a read
// after a connect whose offline-sync-completed marker never arrives. The
// offline queue normally drains (and the marker arrives) within a few
// seconds of connecting.
const defaultCatchUpGrace = 15 * time.Second

// WaitForCatchUp blocks until the offline queue WhatsApp redelivers after
// a (re)connect has drained — signalled by OfflineSyncCompleted — so a
// read served afterwards reflects messages that arrived while the server
// was down, rather than a mirror that is knowably behind. It returns
// immediately when the session is already caught up (the steady state:
// two atomic loads), when the bridge has never connected, when ctx is
// cancelled, or at the grace deadline for a session whose completion
// marker never arrives.
func (b *Bridge) WaitForCatchUp(ctx context.Context) {
	const pollInterval = 50 * time.Millisecond
	for {
		cseq := b.connectedSeq.Load()
		if cseq == 0 || b.offlineSyncSeq.Load() > cseq {
			return
		}
		deadline := time.UnixMilli(b.connectedAtMs.Load()).Add(b.catchUpGrace)
		if !time.Now().Before(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// LastEventAt reports when handleEvent last saw any WhatsApp event, zero
// if none since Open. Together with OpenedAt it is the signal doctor's
// event-flow check reads.
func (b *Bridge) LastEventAt() time.Time {
	ts := b.lastEventAt.Load()
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// OpenedAt reports when this Bridge was constructed — the baseline the
// event-flow check falls back to for a session with no events yet.
func (b *Bridge) OpenedAt() time.Time {
	return b.openedAt
}

// NeedsPairing reports whether the session store has no paired device yet,
// i.e. PairQR must be run before Connect will do anything useful.
func (b *Bridge) NeedsPairing() bool {
	return b.wa().Store.ID == nil
}

// LoggedIn reports whether the client is currently connected and
// authenticated with WhatsApp.
func (b *Bridge) LoggedIn() bool {
	return b.wa().IsLoggedIn()
}

// PairQR runs the QR-code pairing flow: it connects the (unpaired) client
// and invokes show with each QR code payload as WhatsApp issues it, until
// the phone scans one and pairing succeeds, an error occurs, or ctx is
// cancelled. Must be called before Connect, and only when NeedsPairing is
// true.
func (b *Bridge) PairQR(ctx context.Context, show func(code string)) error {
	cl := b.wa()

	qrChan, err := cl.GetQRChannel(ctx)
	if err != nil {
		return waErr("start pairing", err)
	}
	if err := cl.ConnectContext(ctx); err != nil {
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
	if err := b.wa().ConnectContext(ctx); err != nil {
		return waErr("connect", err)
	}
	return nil
}

// Blocklist returns the JIDs the paired account has blocked, fetched live.
func (b *Bridge) Blocklist(ctx context.Context) ([]string, error) {
	bl, err := b.wa().GetBlocklist(ctx)
	if err != nil {
		return nil, waErr("fetch block list", err)
	}

	jids := make([]string, len(bl.JIDs))
	for i, j := range bl.JIDs {
		jids[i] = j.String()
	}
	return jids, nil
}

// GroupInfo returns a group's subject, description (topic), owner JID, and
// the JIDs of its admins (regular and super), all fetched live.
//
// The values are returned as scalars and a slice rather than a struct so
// this package stays out of mcpserv's imports: mcpserv's Live interface is
// satisfied structurally, which a named return type would break. Member
// JIDs are deliberately not returned here — list_group_participants already
// covers those.
func (b *Bridge) GroupInfo(ctx context.Context, groupJID string) (subject, description, ownerJID string, admins []string, err error) {
	jid, err := parseRecipient(groupJID)
	if err != nil {
		return "", "", "", nil, err
	}

	info, err := b.wa().GetGroupInfo(ctx, jid)
	if err != nil {
		return "", "", "", nil, waErr("fetch group info", err)
	}

	for _, p := range info.Participants {
		if p.IsAdmin || p.IsSuperAdmin {
			admins = append(admins, p.JID.String())
		}
	}
	return info.Name, info.Topic, info.OwnerJID.String(), admins, nil
}

// GroupParticipants returns the member JIDs of the given group, fetched
// live from WhatsApp.
func (b *Bridge) GroupParticipants(ctx context.Context, groupJID string) ([]string, error) {
	jid, err := parseRecipient(groupJID)
	if err != nil {
		return nil, err
	}

	info, err := b.wa().GetGroupInfo(ctx, jid)
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
	cl := b.wa()
	if _, err := cl.SendPeerMessage(ctx, cl.BuildHistorySyncRequest(info, count)); err != nil {
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
	cl := b.wa()
	switch {
	case msg.GetImageMessage() != nil:
		return cl.Download(ctx, msg.GetImageMessage())
	case msg.GetVideoMessage() != nil:
		return cl.Download(ctx, msg.GetVideoMessage())
	case msg.GetPtvMessage() != nil:
		return cl.Download(ctx, msg.GetPtvMessage())
	case msg.GetAudioMessage() != nil:
		return cl.Download(ctx, msg.GetAudioMessage())
	case msg.GetDocumentMessage() != nil:
		return cl.Download(ctx, msg.GetDocumentMessage())
	case msg.GetStickerMessage() != nil:
		return cl.Download(ctx, msg.GetStickerMessage())
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
