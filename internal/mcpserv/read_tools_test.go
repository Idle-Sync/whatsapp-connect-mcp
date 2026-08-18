package mcpserv

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/store"
)

// fakeStore records the arguments it was last called with (so tests can
// assert on what toolDeps handed it, e.g. a clamped limit) and returns
// canned fixtures.
type fakeStore struct {
	chatsRet []store.ChatRow
	chatsErr error

	chatRet store.ChatRow
	chatOK  bool
	chatErr error

	messagesLimit                 int
	messagesBefore, messagesAfter int64
	messagesRet                   []store.MessageRow
	messagesErr                   error

	searchMessagesLimit int
	searchMessagesRet   []store.MessageRow
	searchMessagesErr   error

	messageContextBefore, messageContextAfter int
	messageContextRet                         []store.MessageRow
	messageContextErr                         error

	searchContactsLimit int
	searchContactsRet   []store.ContactRow
	searchContactsErr   error

	lastInteractionRet store.MessageRow
	lastInteractionOK  bool
	lastInteractionErr error

	callsLimit              int
	callsBefore, callsAfter int64
	callsRet                []store.CallRow
	callsErr                error

	mediaRef      []byte
	mediaFilename string
	mediaKind     string
	mediaErr      error

	mediaIDsBefore, mediaIDsAfter int64
	mediaIDsKind                  string
	mediaIDsLimit                 int
	mediaIDsRet                   []string
	mediaIDsErr                   error

	latestRowIDRet int64
	latestRowIDErr error

	afterRowIDCalls      int
	afterRowIDArg        int64
	afterRowIDChat       string
	afterRowIDIncludeOwn bool
	afterRowsEmptyCalls  int
	afterRowsRet         []store.MessageRow
	afterRowsNext        int64
	afterRowsErr         error

	oldestRet store.MessageRow
	oldestOK  bool
	oldestErr error

	quickCheckErr error
}

func (f *fakeStore) Chats(_ string, _ bool, limit int) ([]store.ChatRow, error) {
	_ = limit
	return f.chatsRet, f.chatsErr
}

func (f *fakeStore) Chat(_ string) (store.ChatRow, bool, error) {
	return f.chatRet, f.chatOK, f.chatErr
}

func (f *fakeStore) Messages(_ string, before, after int64, limit int) ([]store.MessageRow, error) {
	f.messagesBefore, f.messagesAfter = before, after
	f.messagesLimit = limit
	return f.messagesRet, f.messagesErr
}

func (f *fakeStore) SearchMessages(_, _ string, limit int) ([]store.MessageRow, error) {
	f.searchMessagesLimit = limit
	return f.searchMessagesRet, f.searchMessagesErr
}

func (f *fakeStore) MessageContext(_, _ string, before, after int) ([]store.MessageRow, error) {
	f.messageContextBefore, f.messageContextAfter = before, after
	return f.messageContextRet, f.messageContextErr
}

func (f *fakeStore) SearchContacts(_ string, limit int) ([]store.ContactRow, error) {
	f.searchContactsLimit = limit
	return f.searchContactsRet, f.searchContactsErr
}

func (f *fakeStore) LastInteraction(_ string) (store.MessageRow, bool, error) {
	return f.lastInteractionRet, f.lastInteractionOK, f.lastInteractionErr
}

func (f *fakeStore) Calls(_ string, before, after int64, limit int) ([]store.CallRow, error) {
	f.callsBefore, f.callsAfter = before, after
	f.callsLimit = limit
	return f.callsRet, f.callsErr
}

func (f *fakeStore) MessageMediaRef(_, _ string) ([]byte, string, string, error) {
	return f.mediaRef, f.mediaFilename, f.mediaKind, f.mediaErr
}

func (f *fakeStore) MediaMessageIDs(_ string, before, after int64, kind string, limit int) ([]string, error) {
	f.mediaIDsBefore, f.mediaIDsAfter = before, after
	f.mediaIDsKind, f.mediaIDsLimit = kind, limit
	return f.mediaIDsRet, f.mediaIDsErr
}

func (f *fakeStore) LatestRowID() (int64, error) {
	return f.latestRowIDRet, f.latestRowIDErr
}

func (f *fakeStore) MessagesAfterRowID(chatJID string, afterRowID int64, includeOwn bool, _ int) ([]store.MessageRow, int64, error) {
	f.afterRowIDCalls++
	f.afterRowIDArg = afterRowID
	f.afterRowIDChat = chatJID
	f.afterRowIDIncludeOwn = includeOwn
	if f.afterRowsErr != nil {
		return nil, 0, f.afterRowsErr
	}
	if f.afterRowsEmptyCalls > 0 {
		f.afterRowsEmptyCalls--
		return nil, afterRowID, nil
	}
	if len(f.afterRowsRet) == 0 {
		return nil, afterRowID, nil
	}
	return f.afterRowsRet, f.afterRowsNext, nil
}

func (f *fakeStore) OldestMessage(_ string) (store.MessageRow, bool, error) {
	return f.oldestRet, f.oldestOK, f.oldestErr
}

func (f *fakeStore) QuickCheck() error {
	return f.quickCheckErr
}

// fakeLive records the destDir/filename it was asked to download into.
type fakeLive struct {
	participants    []string
	participantsErr error

	downloadDestDir  string
	downloadFilename string
	downloadPath     string
	downloadErr      error
	// downloadFilenames records every filename across calls (downloadFilename
	// only keeps the last), and downloadErrByName fails just the named files,
	// for the batch tests.
	downloadFilenames []string
	downloadErrByName map[string]error

	historyCalls  int
	historyChat   string
	historyMsgID  string
	historyFromMe bool
	historyTS     int64
	historyCount  int
	historyErr    error

	groupSubject     string
	groupDescription string
	groupOwner       string
	groupAdmins      []string
	groupInfoErr     error

	blocklist    []string
	blocklistErr error

	catchUpWaits int
}

func (f *fakeLive) WaitForCatchUp(_ context.Context) {
	f.catchUpWaits++
}

func (f *fakeLive) RequestOlderMessages(_ context.Context, chatJID, msgID string, fromMe bool, ts int64, count int) error {
	f.historyCalls++
	f.historyChat, f.historyMsgID, f.historyFromMe = chatJID, msgID, fromMe
	f.historyTS, f.historyCount = ts, count
	return f.historyErr
}

func (f *fakeLive) GroupParticipants(_ context.Context, _ string) ([]string, error) {
	return f.participants, f.participantsErr
}

func (f *fakeLive) GroupInfo(_ context.Context, _ string) (string, string, string, []string, error) {
	return f.groupSubject, f.groupDescription, f.groupOwner, f.groupAdmins, f.groupInfoErr
}

func (f *fakeLive) Blocklist(_ context.Context) ([]string, error) {
	return f.blocklist, f.blocklistErr
}

func (f *fakeLive) DownloadMedia(_ context.Context, _ []byte, destDir, filename string) (string, error) {
	f.downloadDestDir = destDir
	f.downloadFilename = filename
	f.downloadFilenames = append(f.downloadFilenames, filename)
	if err, ok := f.downloadErrByName[filename]; ok {
		return "", err
	}
	if f.downloadErr != nil {
		return "", f.downloadErr
	}
	if f.downloadPath == "" {
		return filepath.Join(destDir, filename), nil
	}
	return f.downloadPath, nil
}

func TestListChatsResultIsBannerWrappedRowPerLine(t *testing.T) {
	st := &fakeStore{chatsRet: []store.ChatRow{
		{JID: "111@s.whatsapp.net", Name: "Alice", IsGroup: false, Archived: false, LastMessageAt: 1700000000},
		{JID: "222@g.us", Name: "Team", IsGroup: true, Archived: true, LastMessageAt: 1700000100},
	}}
	d := &toolDeps{st: st, live: &fakeLive{}}

	result, _, err := d.listChats(context.Background(), nil, listChatsInput{})
	if err != nil {
		t.Fatalf("listChats() error = %v", err)
	}

	text := resultText(t, result)
	wantRow1 := renderChatRow(st.chatsRet[0])
	wantRow2 := renderChatRow(st.chatsRet[1])
	if !strings.Contains(text, wantRow1) || !strings.Contains(text, wantRow2) {
		t.Fatalf("listChats() text = %q, want both rows %q and %q", text, wantRow1, wantRow2)
	}
	if !strings.HasPrefix(text, bannerWarning) {
		t.Fatalf("listChats() text does not start with the banner warning: %q", text)
	}
	if !strings.Contains(text, bannerOpen) || !strings.HasSuffix(text, bannerClose) {
		t.Fatalf("listChats() text is not banner-wrapped: %q", text)
	}
	// Row-per-line: exactly one line between the markers per chat.
	between := strings.TrimSuffix(strings.SplitN(text, bannerOpen+"\n", 2)[1], "\n"+bannerClose)
	lines := strings.Split(between, "\n")
	if len(lines) != 2 {
		t.Fatalf("listChats() body has %d lines, want 2 (one per chat): %q", len(lines), between)
	}
}

func TestListMessagesClampsLimitTo100(t *testing.T) {
	st := &fakeStore{}
	d := &toolDeps{st: st, live: &fakeLive{}}

	_, _, err := d.listMessages(context.Background(), nil, listMessagesInput{ChatJID: "x@s.whatsapp.net", Limit: 500})
	if err != nil {
		t.Fatalf("listMessages() error = %v", err)
	}

	if st.messagesLimit != 100 {
		t.Fatalf("Store.Messages called with limit = %d, want 100 (clamped from 500)", st.messagesLimit)
	}
}

func TestListMessagesZeroLimitDefaultsTo20(t *testing.T) {
	st := &fakeStore{}
	d := &toolDeps{st: st, live: &fakeLive{}}

	_, _, err := d.listMessages(context.Background(), nil, listMessagesInput{ChatJID: "x@s.whatsapp.net"})
	if err != nil {
		t.Fatalf("listMessages() error = %v", err)
	}

	if st.messagesLimit != 20 {
		t.Fatalf("Store.Messages called with limit = %d, want 20 (default)", st.messagesLimit)
	}
}

// Fixed clock for the time-window tests: 2026-08-18T12:34:56Z. The IST
// epoch expectations match internal/timewin's own externally-computed
// fixtures; what these tests pin is the wiring — that the tool resolves the
// window itself and hands the store the resolved bounds.
func fixedNow() time.Time {
	return time.Unix(1787056496, 0).UTC()
}

func TestListMessagesResolvesNamedWindowServerSide(t *testing.T) {
	st := &fakeStore{}
	d := &toolDeps{st: st, live: &fakeLive{}, now: fixedNow}

	_, _, err := d.listMessages(context.Background(), nil, listMessagesInput{
		ChatJID: "x@s.whatsapp.net", Window: "yesterday", TZ: "Asia/Kolkata",
	})
	if err != nil {
		t.Fatalf("listMessages(window) error = %v", err)
	}

	const istAug17, istAug18 = 1786905000, 1786991400
	if st.messagesAfter != istAug17-1 || st.messagesBefore != istAug18 {
		t.Fatalf("Store.Messages bounds = (after=%d, before=%d), want (%d, %d) — yesterday in IST resolved server-side",
			st.messagesAfter, st.messagesBefore, istAug17-1, istAug18)
	}
}

func TestListMessagesUnixSecondsStillAccepted(t *testing.T) {
	st := &fakeStore{}
	d := &toolDeps{st: st, live: &fakeLive{}}

	_, _, err := d.listMessages(context.Background(), nil, listMessagesInput{
		ChatJID: "x@s.whatsapp.net", After: "100", Before: "200",
	})
	if err != nil {
		t.Fatalf("listMessages(unix bounds) error = %v", err)
	}
	if st.messagesAfter != 100 || st.messagesBefore != 200 {
		t.Fatalf("Store.Messages bounds = (after=%d, before=%d), want (100, 200)", st.messagesAfter, st.messagesBefore)
	}
}

func TestListMessagesBadTimeBoundErrors(t *testing.T) {
	d := &toolDeps{st: &fakeStore{}, live: &fakeLive{}}

	_, _, err := d.listMessages(context.Background(), nil, listMessagesInput{
		ChatJID: "x@s.whatsapp.net", After: "banana",
	})
	if err == nil {
		t.Fatal("listMessages(after=banana): want error, got nil")
	}
}

func TestGetCallHistoryAcceptsTimeWindow(t *testing.T) {
	st := &fakeStore{}
	d := &toolDeps{st: st, live: &fakeLive{}, now: fixedNow}

	_, _, err := d.getCallHistory(context.Background(), nil, getCallHistoryInput{
		Window: "yesterday", TZ: "Asia/Kolkata",
	})
	if err != nil {
		t.Fatalf("getCallHistory(window) error = %v", err)
	}

	const istAug17, istAug18 = 1786905000, 1786991400
	if st.callsAfter != istAug17-1 || st.callsBefore != istAug18 {
		t.Fatalf("Store.Calls bounds = (after=%d, before=%d), want (%d, %d)",
			st.callsAfter, st.callsBefore, istAug17-1, istAug18)
	}
}

func TestGetMessageContextClampsBeforeAndAfter(t *testing.T) {
	st := &fakeStore{}
	d := &toolDeps{st: st, live: &fakeLive{}}

	_, _, err := d.getMessageContext(context.Background(), nil, getMessageContextInput{
		ChatJID: "x@s.whatsapp.net", MessageID: "m1", Before: 500, After: -3,
	})
	if err != nil {
		t.Fatalf("getMessageContext() error = %v", err)
	}

	if st.messageContextBefore != 100 {
		t.Fatalf("MessageContext called with before = %d, want 100 (clamped from 500)", st.messageContextBefore)
	}
	if st.messageContextAfter != 0 {
		t.Fatalf("MessageContext called with after = %d, want 0 (clamped from -3)", st.messageContextAfter)
	}
}

func TestGetChatNotFoundReturnsError(t *testing.T) {
	st := &fakeStore{chatOK: false}
	d := &toolDeps{st: st, live: &fakeLive{}}

	_, _, err := d.getChat(context.Background(), nil, getChatInput{ChatJID: "missing@s.whatsapp.net"})
	if err == nil {
		t.Fatal("getChat() error = nil, want an error for a missing chat")
	}
	if strings.Contains(err.Error(), "missing@s.whatsapp.net") {
		t.Fatalf("getChat() error embeds the JID, want a category-only message: %v", err)
	}
}

// TestFetchOlderMessagesAnchorsOnOldestStored proves the request is built
// from the oldest stored message rather than any other row: anchoring on
// anything else would ask the phone for messages we already hold, so the
// call would appear to succeed while widening nothing.
func TestFetchOlderMessagesAnchorsOnOldestStored(t *testing.T) {
	oldest := store.MessageRow{
		ChatJID: "chat@s.whatsapp.net", ID: "OLDEST1", FromMe: true, TS: 1700000000,
	}
	st := &fakeStore{oldestRet: oldest, oldestOK: true}
	live := &fakeLive{}
	d := &toolDeps{st: st, live: live}

	_, _, err := d.fetchOlderMessages(context.Background(), nil,
		fetchOlderMessagesInput{ChatJID: oldest.ChatJID, Count: 25})
	if err != nil {
		t.Fatalf("fetchOlderMessages() error = %v", err)
	}

	if live.historyCalls != 1 {
		t.Fatalf("RequestOlderMessages called %d times, want 1", live.historyCalls)
	}
	if live.historyChat != oldest.ChatJID || live.historyMsgID != oldest.ID {
		t.Errorf("anchored on (%q, %q), want (%q, %q)",
			live.historyChat, live.historyMsgID, oldest.ChatJID, oldest.ID)
	}
	if live.historyFromMe != oldest.FromMe || live.historyTS != oldest.TS {
		t.Errorf("anchor fromMe/ts = %v/%d, want %v/%d",
			live.historyFromMe, live.historyTS, oldest.FromMe, oldest.TS)
	}
	if live.historyCount != 25 {
		t.Errorf("requested count = %d, want 25", live.historyCount)
	}
}

// TestFetchOlderMessagesClampsCount covers both ends of the range plus the
// unset case, since an out-of-range count would otherwise reach the phone.
func TestFetchOlderMessagesClampsCount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want int
	}{
		{"unset defaults", 0, defaultHistoryRequestCount},
		{"negative defaults", -5, defaultHistoryRequestCount},
		{"over max clamps", maxHistoryRequestCount + 1, maxHistoryRequestCount},
		{"in range passes", 120, 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{oldestRet: store.MessageRow{ChatJID: "c@s.whatsapp.net", ID: "X"}, oldestOK: true}
			live := &fakeLive{}
			d := &toolDeps{st: st, live: live}

			if _, _, err := d.fetchOlderMessages(context.Background(), nil,
				fetchOlderMessagesInput{ChatJID: "c@s.whatsapp.net", Count: tc.in}); err != nil {
				t.Fatalf("fetchOlderMessages() error = %v", err)
			}
			if live.historyCount != tc.want {
				t.Errorf("count %d became %d, want %d", tc.in, live.historyCount, tc.want)
			}
		})
	}
}

// TestFetchOlderMessagesWithNothingStoredSendsNothing proves an empty chat
// short-circuits. There is no message to anchor on, so a request could never
// be answered; sending one anyway would burn a round trip and report success.
func TestFetchOlderMessagesWithNothingStoredSendsNothing(t *testing.T) {
	live := &fakeLive{}
	d := &toolDeps{st: &fakeStore{oldestOK: false}, live: live}

	result, _, err := d.fetchOlderMessages(context.Background(), nil,
		fetchOlderMessagesInput{ChatJID: "empty@s.whatsapp.net"})
	if err != nil {
		t.Fatalf("fetchOlderMessages() error = %v", err)
	}
	if live.historyCalls != 0 {
		t.Fatalf("RequestOlderMessages called %d times, want 0 for a chat with nothing stored", live.historyCalls)
	}
	if text := resultText(t, result); !strings.Contains(text, "nothing to anchor") {
		t.Errorf("result = %q, want an explanation that there is nothing to anchor on", text)
	}
}

// TestFetchOlderMessagesIsNotBannerWrapped guards the boundary the banner
// exists to police: this tool reports our own status, never WhatsApp
// content, so wrapping it would train callers to discount the banner where
// it does matter.
func TestFetchOlderMessagesIsNotBannerWrapped(t *testing.T) {
	st := &fakeStore{oldestRet: store.MessageRow{ChatJID: "c@s.whatsapp.net", ID: "X"}, oldestOK: true}
	d := &toolDeps{st: st, live: &fakeLive{}}

	result, _, err := d.fetchOlderMessages(context.Background(), nil,
		fetchOlderMessagesInput{ChatJID: "c@s.whatsapp.net"})
	if err != nil {
		t.Fatalf("fetchOlderMessages() error = %v", err)
	}
	text := resultText(t, result)
	if strings.HasPrefix(text, bannerWarning) || strings.Contains(text, bannerOpen) {
		t.Errorf("result is banner-wrapped, want a plain status line: %q", text)
	}
}

func TestGetBlocklistRendersJIDs(t *testing.T) {
	live := &fakeLive{blocklist: []string{"spam1@s.whatsapp.net", "spam2@s.whatsapp.net"}}
	d := &toolDeps{st: &fakeStore{}, live: live}

	result, _, err := d.getBlocklist(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("getBlocklist() error = %v", err)
	}
	text := resultText(t, result)
	for _, j := range live.blocklist {
		if !strings.Contains(text, j) {
			t.Errorf("getBlocklist() text = %q, missing %q", text, j)
		}
	}
	if !strings.HasPrefix(text, bannerWarning) {
		t.Errorf("getBlocklist() result is not banner-wrapped: %q", text)
	}
}

func TestGetBlocklistEmptyReportsSo(t *testing.T) {
	d := &toolDeps{st: &fakeStore{}, live: &fakeLive{blocklist: nil}}

	result, _, err := d.getBlocklist(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("getBlocklist() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "no blocked contacts") {
		t.Errorf("getBlocklist() text = %q, want the empty-list message", text)
	}
	// A plain status line, not WhatsApp content: no banner.
	if strings.HasPrefix(text, bannerWarning) {
		t.Errorf("getBlocklist() empty result should not be banner-wrapped: %q", text)
	}
}

func TestGetGroupInfoRendersFieldsAndAdmins(t *testing.T) {
	live := &fakeLive{
		groupSubject:     "Project Team",
		groupDescription: "Planning channel",
		groupOwner:       "owner@s.whatsapp.net",
		groupAdmins:      []string{"a1@s.whatsapp.net", "a2@s.whatsapp.net"},
	}
	d := &toolDeps{st: &fakeStore{}, live: live}

	result, _, err := d.getGroupInfo(context.Background(), nil, getGroupInfoInput{GroupJID: "g@g.us"})
	if err != nil {
		t.Fatalf("getGroupInfo() error = %v", err)
	}

	text := resultText(t, result)
	for _, want := range []string{
		"subject\tProject Team",
		"description\tPlanning channel",
		"owner\towner@s.whatsapp.net",
		"admin\ta1@s.whatsapp.net",
		"admin\ta2@s.whatsapp.net",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("getGroupInfo() text = %q, missing %q", text, want)
		}
	}
	if !strings.HasPrefix(text, bannerWarning) {
		t.Errorf("getGroupInfo() result is not banner-wrapped: %q", text)
	}
}

func TestGetGroupInfoWithNoAdminsStillRendersFields(t *testing.T) {
	live := &fakeLive{groupSubject: "Solo", groupOwner: "o@s.whatsapp.net"}
	d := &toolDeps{st: &fakeStore{}, live: live}

	result, _, err := d.getGroupInfo(context.Background(), nil, getGroupInfoInput{GroupJID: "g@g.us"})
	if err != nil {
		t.Fatalf("getGroupInfo() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "subject\tSolo") {
		t.Errorf("getGroupInfo() text = %q, want the subject line even with no admins", text)
	}
	if strings.Contains(text, "admin\t") {
		t.Errorf("getGroupInfo() text = %q, want no admin lines when there are none", text)
	}
}

func TestListGroupParticipantsIsBannerWrapped(t *testing.T) {
	live := &fakeLive{participants: []string{"111@s.whatsapp.net", "222@s.whatsapp.net"}}
	d := &toolDeps{st: &fakeStore{}, live: live}

	result, _, err := d.listGroupParticipants(context.Background(), nil, listGroupParticipantsInput{GroupJID: "g@g.us"})
	if err != nil {
		t.Fatalf("listGroupParticipants() error = %v", err)
	}

	text := resultText(t, result)
	for _, p := range live.participants {
		if !strings.Contains(text, p) {
			t.Fatalf("listGroupParticipants() text = %q, missing participant %q", text, p)
		}
	}
	if !strings.HasPrefix(text, bannerWarning) {
		t.Fatalf("listGroupParticipants() text is not banner-wrapped: %q", text)
	}
}

func TestDownloadMediaWritesUnderDataDirAndPathIsOutsideBanner(t *testing.T) {
	dataDir := t.TempDir()
	wantDestDir := filepath.Join(dataDir, "media", "chat123@s.whatsapp.net")
	wantPath := filepath.Join(wantDestDir, "m1.jpg")

	st := &fakeStore{mediaRef: []byte("ref-bytes"), mediaFilename: "photo.jpg", mediaKind: "image"}
	live := &fakeLive{downloadPath: wantPath}
	d := &toolDeps{st: st, live: live, dataDir: dataDir}

	result, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat123@s.whatsapp.net", MessageID: "m1",
	})
	if err != nil {
		t.Fatalf("downloadMedia() error = %v", err)
	}

	if live.downloadDestDir != wantDestDir {
		t.Fatalf("DownloadMedia called with destDir = %q, want %q", live.downloadDestDir, wantDestDir)
	}
	// The SAVED file is named from the message id, never the sender-declared
	// "photo.jpg": see savedMediaFilename.
	if live.downloadFilename != "m1.jpg" {
		t.Fatalf("DownloadMedia called with filename = %q, want %q (message-id-based, not the sender's name)", live.downloadFilename, "m1.jpg")
	}

	text := resultText(t, result)
	closeIdx := strings.Index(text, bannerClose)
	pathIdx := strings.Index(text, "saved_path: "+wantPath)
	if closeIdx == -1 || pathIdx == -1 {
		t.Fatalf("downloadMedia() text missing banner close or saved_path: %q", text)
	}
	if pathIdx < closeIdx {
		t.Fatalf("downloadMedia() saved_path appears before the banner closes, want it outside (after) the banner: %q", text)
	}
	// The sender-declared name is still shown as display data, inside the banner.
	if !strings.Contains(text[:closeIdx], "photo.jpg") {
		t.Fatalf("downloadMedia() WhatsApp-derived filename is not inside the banner: %q", text)
	}
	if strings.Contains(text[closeIdx:], "photo.jpg") {
		t.Fatalf("downloadMedia() sender-declared filename leaked outside the banner (into saved_path): %q", text)
	}
}

// TestDownloadMediaEmptyMediaFilenameStillDownloads proves the fix for
// image/video/audio/voice/sticker media: the store never carries a
// sender-declared filename for these kinds (only documents get one from
// WhatsApp), and download_media must not reject that empty string the way
// sanitizeMediaFilename alone would — the saved name comes from the message
// id and kind instead, regardless of whether a display filename exists.
func TestDownloadMediaEmptyMediaFilenameStillDownloads(t *testing.T) {
	dataDir := t.TempDir()
	wantDestDir := filepath.Join(dataDir, "media", "chat123@s.whatsapp.net")
	wantPath := filepath.Join(wantDestDir, "m1.jpg")

	st := &fakeStore{mediaRef: []byte("ref-bytes"), mediaFilename: "", mediaKind: "image"}
	live := &fakeLive{downloadPath: wantPath}
	d := &toolDeps{st: st, live: live, dataDir: dataDir}

	result, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat123@s.whatsapp.net", MessageID: "m1",
	})
	if err != nil {
		t.Fatalf("downloadMedia() error = %v, want nil (empty media_filename must not block a download)", err)
	}
	if live.downloadFilename != "m1.jpg" {
		t.Fatalf("DownloadMedia called with filename = %q, want %q", live.downloadFilename, "m1.jpg")
	}
	if !strings.Contains(resultText(t, result), "saved_path: "+wantPath) {
		t.Fatalf("downloadMedia() text missing saved_path %q", wantPath)
	}
}

// TestDownloadMediaTwoMessagesInSameChatDoNotCollide proves two media
// messages in one chat — each with the same (or no) sender-declared
// filename, as two photos named "IMG_0001.jpg" by different phones would
// be — write to distinct saved files because the name is derived from each
// message's own id.
func TestDownloadMediaTwoMessagesInSameChatDoNotCollide(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{mediaRef: []byte("ref"), mediaFilename: "IMG_0001.jpg", mediaKind: "image"}
	live := &fakeLive{}
	d := &toolDeps{st: st, live: live, dataDir: dataDir}

	if _, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat@s.whatsapp.net", MessageID: "m1",
	}); err != nil {
		t.Fatalf("downloadMedia() m1 error = %v", err)
	}
	firstName := live.downloadFilename

	if _, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat@s.whatsapp.net", MessageID: "m2",
	}); err != nil {
		t.Fatalf("downloadMedia() m2 error = %v", err)
	}
	secondName := live.downloadFilename

	if firstName == secondName {
		t.Fatalf("both messages saved as %q, want distinct filenames despite the identical sender-declared name", firstName)
	}
	if firstName != "m1.jpg" || secondName != "m2.jpg" {
		t.Fatalf("saved filenames = (%q, %q), want (m1.jpg, m2.jpg)", firstName, secondName)
	}
}

func TestSavedMediaFilename(t *testing.T) {
	cases := []struct {
		name           string
		messageID      string
		kind           string
		senderFilename string
		want           string
		wantErr        bool
	}{
		{"image ignores sender name", "m1", "image", "photo.png", "m1.jpg", false},
		{"video", "m1", "video", "", "m1.mp4", false},
		{"video note", "m1", "video_note", "", "m1.mp4", false},
		{"audio", "m1", "audio", "", "m1.ogg", false},
		{"voice", "m1", "voice", "", "m1.ogg", false},
		{"sticker", "m1", "sticker", "", "m1.webp", false},
		{"unknown kind falls back to bin", "m1", "other", "", "m1.bin", false},
		{"document keeps its own extension", "m1", "document", "report.pdf", "m1.pdf", false},
		{"document with no extension falls back to bin", "m1", "document", "README", "m1.bin", false},
		{"document traversal name only contributes its extension", "m1", "document", "../../../../evil.sh", "m1.sh", false},
		{"traversal message id is rejected", "../../etc/passwd", "image", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := savedMediaFilename(c.messageID, c.kind, c.senderFilename)
			if c.wantErr {
				if err == nil {
					t.Fatalf("savedMediaFilename(%q, %q, %q) error = nil, want error", c.messageID, c.kind, c.senderFilename)
				}
				return
			}
			if err != nil {
				t.Fatalf("savedMediaFilename(%q, %q, %q) error = %v, want nil", c.messageID, c.kind, c.senderFilename, err)
			}
			if got != c.want {
				t.Fatalf("savedMediaFilename(%q, %q, %q) = %q, want %q", c.messageID, c.kind, c.senderFilename, got, c.want)
			}
		})
	}
}

func TestDownloadMediaSanitizesJIDForFilesystemUse(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{mediaRef: []byte("ref"), mediaFilename: "note.opus", mediaKind: "voice"}
	live := &fakeLive{downloadPath: filepath.Join(dataDir, "media", "5511999_12@s.whatsapp.net", "note.opus")}
	d := &toolDeps{st: st, live: live, dataDir: dataDir}

	_, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "5511999:12@s.whatsapp.net", MessageID: "m1",
	})
	if err != nil {
		t.Fatalf("downloadMedia() error = %v", err)
	}

	// Only the chat_jid-derived path component matters here; dataDir itself
	// (e.g. a Windows "C:\...") legitimately contains a colon.
	if strings.Contains(filepath.Base(live.downloadDestDir), ":") {
		t.Fatalf("downloadMedia() destDir = %q has a ':' in its chat_jid component, which is invalid in a Windows path component", live.downloadDestDir)
	}
}

func TestDownloadMediaRejectsPathTraversalInChatJID(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{mediaRef: []byte("ref"), mediaFilename: "f.jpg", mediaKind: "image"}
	d := &toolDeps{st: st, live: &fakeLive{}, dataDir: dataDir}

	_, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "../../etc", MessageID: "m1",
	})
	if err == nil {
		t.Fatal("downloadMedia() error = nil, want an error for a chat_jid containing '..'")
	}
}

// TestDownloadMediaNeutralizesTraversalFilename proves saved_path never
// contains a sender-controlled component: a document's sender-declared
// filename is remote-supplied data, so even a traversal payload like
// "../../../../evil.sh" must not reach the saved file's path — only its
// extension (".sh") is ever borrowed from it, appended to the message id.
func TestDownloadMediaNeutralizesTraversalFilename(t *testing.T) {
	dataDir := t.TempDir()
	wantDestDir := filepath.Join(dataDir, "media", "chat@s.whatsapp.net")
	wantPath := filepath.Join(wantDestDir, "m1.sh")

	st := &fakeStore{mediaRef: []byte("ref"), mediaFilename: "../../../../evil.sh", mediaKind: "document"}
	live := &fakeLive{downloadPath: wantPath}
	d := &toolDeps{st: st, live: live, dataDir: dataDir}

	result, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat@s.whatsapp.net", MessageID: "m1",
	})
	if err != nil {
		t.Fatalf("downloadMedia() error = %v, want nil (the traversal name must be neutralized, not rejected)", err)
	}
	if live.downloadFilename != "m1.sh" {
		t.Fatalf("DownloadMedia called with filename = %q, want %q", live.downloadFilename, "m1.sh")
	}
	if live.downloadDestDir != wantDestDir {
		t.Fatalf("DownloadMedia called with destDir = %q, want %q", live.downloadDestDir, wantDestDir)
	}

	text := resultText(t, result)
	pathIdx := strings.Index(text, "saved_path: ")
	if pathIdx == -1 {
		t.Fatalf("downloadMedia() text missing saved_path: %q", text)
	}
	savedPath := text[pathIdx+len("saved_path: "):]
	if strings.Contains(savedPath, "..") || strings.Contains(savedPath, "evil") {
		t.Fatalf("saved_path = %q, contains a sender-controlled component", savedPath)
	}
}

func TestDownloadMediaBatchIDs(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{mediaRef: []byte("ref"), mediaFilename: "", mediaKind: "image"}
	live := &fakeLive{}
	d := &toolDeps{st: st, live: live, dataDir: dataDir}

	result, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat@s.whatsapp.net", MessageIDs: []string{"m1", "m2"},
	})
	if err != nil {
		t.Fatalf("downloadMedia(batch) error = %v", err)
	}

	if len(live.downloadFilenames) != 2 || live.downloadFilenames[0] != "m1.jpg" || live.downloadFilenames[1] != "m2.jpg" {
		t.Fatalf("DownloadMedia calls = %v, want [m1.jpg m2.jpg]", live.downloadFilenames)
	}
	text := resultText(t, result)
	if strings.Count(text, "saved_path: ") != 2 {
		t.Fatalf("downloadMedia(batch) text has %d saved_path lines, want 2: %q", strings.Count(text, "saved_path: "), text)
	}
}

// One bad file must not abort the batch: the remaining downloads still run
// and the failure is reported per file rather than as a tool error.
func TestDownloadMediaBatchContinuesPastFailure(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{mediaRef: []byte("ref"), mediaFilename: "", mediaKind: "image"}
	live := &fakeLive{downloadErrByName: map[string]error{"m1.jpg": errors.New("download media: WhatsApp request failed")}}
	d := &toolDeps{st: st, live: live, dataDir: dataDir}

	result, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat@s.whatsapp.net", MessageIDs: []string{"m1", "m2"},
	})
	if err != nil {
		t.Fatalf("downloadMedia(batch) error = %v, want per-file failure reporting instead", err)
	}

	if len(live.downloadFilenames) != 2 {
		t.Fatalf("DownloadMedia calls = %v, want both files attempted despite the first failing", live.downloadFilenames)
	}
	text := resultText(t, result)
	if strings.Count(text, "saved_path: ") != 1 {
		t.Fatalf("want exactly 1 saved_path line (m2), got: %q", text)
	}
	if !strings.Contains(text, "failed: m1") {
		t.Fatalf("want a per-file failed line naming m1, got: %q", text)
	}
}

// The window form resolves time bounds server-side (same forms as
// list_messages) and downloads whatever media the store reports in it.
func TestDownloadMediaWindowForm(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{mediaRef: []byte("ref"), mediaFilename: "", mediaKind: "image", mediaIDsRet: []string{"m9", "m8"}}
	live := &fakeLive{}
	d := &toolDeps{st: st, live: live, dataDir: dataDir, now: fixedNow}

	result, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat@s.whatsapp.net", Window: "yesterday", TZ: "Asia/Kolkata", Kind: "image",
	})
	if err != nil {
		t.Fatalf("downloadMedia(window) error = %v", err)
	}

	const istAug17, istAug18 = 1786905000, 1786991400
	if st.mediaIDsAfter != istAug17-1 || st.mediaIDsBefore != istAug18 {
		t.Fatalf("MediaMessageIDs bounds = (after=%d, before=%d), want (%d, %d)",
			st.mediaIDsAfter, st.mediaIDsBefore, istAug17-1, istAug18)
	}
	if st.mediaIDsKind != "image" {
		t.Fatalf("MediaMessageIDs kind = %q, want image", st.mediaIDsKind)
	}
	if len(live.downloadFilenames) != 2 {
		t.Fatalf("DownloadMedia calls = %v, want both window matches downloaded", live.downloadFilenames)
	}
	if strings.Count(resultText(t, result), "saved_path: ") != 2 {
		t.Fatalf("want 2 saved_path lines, got: %q", resultText(t, result))
	}
}

func TestDownloadMediaWindowFormNoMatches(t *testing.T) {
	st := &fakeStore{mediaIDsRet: nil}
	d := &toolDeps{st: st, live: &fakeLive{}, dataDir: t.TempDir(), now: fixedNow}

	result, _, err := d.downloadMedia(context.Background(), nil, downloadMediaInput{
		ChatJID: "chat@s.whatsapp.net", Window: "yesterday",
	})
	if err != nil {
		t.Fatalf("downloadMedia(empty window) error = %v, want a no-matches result instead", err)
	}
	if !strings.Contains(resultText(t, result), "no media messages") {
		t.Fatalf("want a no-media-messages notice, got: %q", resultText(t, result))
	}
}

func TestDownloadMediaSelectorValidation(t *testing.T) {
	d := &toolDeps{st: &fakeStore{}, live: &fakeLive{}, dataDir: t.TempDir()}

	for name, in := range map[string]downloadMediaInput{
		"id and ids":    {ChatJID: "c@s.whatsapp.net", MessageID: "m1", MessageIDs: []string{"m2"}},
		"id and window": {ChatJID: "c@s.whatsapp.net", MessageID: "m1", Window: "today"},
		"no selector":   {ChatJID: "c@s.whatsapp.net"},
	} {
		if _, _, err := d.downloadMedia(context.Background(), nil, in); err == nil {
			t.Errorf("downloadMedia(%s): want an error, got nil", name)
		}
	}
}

func TestDoctorResultIsNotBannerWrapped(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{}
	env := DoctorEnv{Home: dataDir, BinaryPath: filepath.Join(dataDir, "bin"), LoggedIn: func() bool { return false }}
	d := &toolDeps{st: st, live: &fakeLive{}, dataDir: dataDir, doctorEnv: env}

	result, _, err := d.doctor(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("doctor() error = %v", err)
	}

	text := resultText(t, result)
	if strings.Contains(text, bannerOpen) || strings.Contains(text, bannerWarning) {
		t.Fatalf("doctor() result is banner-wrapped, want plain diagnostic output: %q", text)
	}
	if !strings.Contains(text, "session") || !strings.Contains(text, "database") {
		t.Fatalf("doctor() result missing expected check names: %q", text)
	}
}

func TestDoctorPropagatesDatabaseIntegrityFailure(t *testing.T) {
	dataDir := t.TempDir()
	st := &fakeStore{quickCheckErr: errors.New("corrupt")}
	d := &toolDeps{st: st, live: &fakeLive{}, dataDir: dataDir, doctorEnv: DoctorEnv{Home: dataDir, BinaryPath: filepath.Join(dataDir, "bin")}}

	result, _, err := d.doctor(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("doctor() error = %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "fail") || !strings.Contains(text, "database") {
		t.Fatalf("doctor() result does not report the database failure: %q", text)
	}
	if strings.Contains(text, "corrupt") {
		t.Fatalf("doctor() result leaks the underlying error text instead of a category message: %q", text)
	}
}

func TestSanitizeMediaFilenameRejectsTraversalAndAcceptsPlainNames(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
		want    string
	}{
		{"plain", "photo.jpg", false, "photo.jpg"},
		{"traversal", "../../../../evil.txt", true, ""},
		{"bare dotdot", "..", true, ""},
		{"bare dot", ".", true, ""},
		{"empty", "", true, ""},
		{"embedded slash", "sub/dir/evil.txt", true, ""},
		{"embedded backslash", `sub\dir\evil.txt`, true, ""},
		{"leading slash", "/etc/passwd", true, ""},
		{"dots but no separator", "file..txt", false, "file..txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sanitizeMediaFilename(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("sanitizeMediaFilename(%q) error = nil, want an error", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeMediaFilename(%q) error = %v, want nil", c.in, err)
			}
			if got != c.want {
				t.Fatalf("sanitizeMediaFilename(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRegisterReadToolsBuildsAllElevenSchemasWithoutPanicking(t *testing.T) {
	// mcp.AddTool panics if a tool's input type cannot produce a valid JSON
	// schema. Exercising the full registration path (rather than only the
	// handler methods, as the other tests in this file do) is what would
	// catch a struct tag or type mistake in any of the eleven tools.
	server := New(&fakeStore{}, &fakeLive{}, nil, nil, t.TempDir(), DoctorEnv{})
	if server == nil {
		t.Fatal("New() returned a nil server")
	}
}

// resultText extracts the sole text content from a tool result.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("result has %d content items, want 1", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("result content is %T, want *mcp.TextContent", result.Content[0])
	}
	return tc.Text
}

// Every store-backed read must wait out the post-reconnect catch-up window
// before querying: WhatsApp redelivers offline-queued messages in the
// first seconds after connecting, and a read served before that finishes
// would present a mirror that is knowably behind as if it were current.
func TestStoreBackedReadsWaitForCatchUp(t *testing.T) {
	st := &fakeStore{chatOK: true, lastInteractionOK: true, mediaRef: []byte("r"), mediaKind: "image"}
	live := &fakeLive{}
	d := &toolDeps{st: st, live: live, dataDir: t.TempDir()}
	ctx := context.Background()

	handlers := map[string]func(){
		"list_chats":           func() { _, _, _ = d.listChats(ctx, nil, listChatsInput{}) },
		"get_chat":             func() { _, _, _ = d.getChat(ctx, nil, getChatInput{ChatJID: "x@s.whatsapp.net"}) },
		"list_messages":        func() { _, _, _ = d.listMessages(ctx, nil, listMessagesInput{ChatJID: "x@s.whatsapp.net"}) },
		"search_messages":      func() { _, _, _ = d.searchMessages(ctx, nil, searchMessagesInput{Query: "q"}) },
		"get_message_context":  func() { _, _, _ = d.getMessageContext(ctx, nil, getMessageContextInput{ChatJID: "x", MessageID: "m"}) },
		"search_contacts":      func() { _, _, _ = d.searchContacts(ctx, nil, searchContactsInput{}) },
		"get_last_interaction": func() { _, _, _ = d.getLastInteraction(ctx, nil, getLastInteractionInput{JID: "x"}) },
		"get_call_history":     func() { _, _, _ = d.getCallHistory(ctx, nil, getCallHistoryInput{}) },
		"download_media":       func() { _, _, _ = d.downloadMedia(ctx, nil, downloadMediaInput{ChatJID: "x@s.whatsapp.net", MessageID: "m1"}) },
	}
	for name, call := range handlers {
		before := live.catchUpWaits
		call()
		if live.catchUpWaits != before+1 {
			t.Errorf("%s did not wait for catch-up before reading the store", name)
		}
	}
}

// poll_new_messages: the cursor long-poll that lets an agent learn a new
// message arrived without re-reading chats.

func TestPollNewMessagesBootstrapReturnsWatermarkOnly(t *testing.T) {
	st := &fakeStore{latestRowIDRet: 41}
	live := &fakeLive{}
	d := &toolDeps{st: st, live: live}

	result, _, err := d.pollNewMessages(context.Background(), nil, pollNewMessagesInput{})
	if err != nil {
		t.Fatalf("pollNewMessages() error = %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "next_cursor: 41") {
		t.Fatalf("bootstrap result = %q, want the current watermark as next_cursor", text)
	}
	if st.afterRowIDCalls != 0 {
		t.Fatal("bootstrap with no cursor must not query messages — it starts from now")
	}
	if live.catchUpWaits != 1 {
		t.Fatal("pollNewMessages must wait for the reconnect catch-up window")
	}
}

func TestPollNewMessagesReturnsRowsAfterCursor(t *testing.T) {
	st := &fakeStore{
		afterRowsRet: []store.MessageRow{
			{ChatJID: "c@g.us", ID: "N1", SenderName: "Alice", TS: 900, Kind: "text", Text: "hello"},
		},
		afterRowsNext: 57,
	}
	d := &toolDeps{st: st, live: &fakeLive{}}

	result, _, err := d.pollNewMessages(context.Background(), nil, pollNewMessagesInput{Cursor: "41"})
	if err != nil {
		t.Fatalf("pollNewMessages() error = %v", err)
	}

	if st.afterRowIDArg != 41 {
		t.Fatalf("MessagesAfterRowID called with cursor %d, want 41", st.afterRowIDArg)
	}
	text := resultText(t, result)
	closeIdx := strings.Index(text, bannerClose)
	if closeIdx == -1 || !strings.Contains(text[:closeIdx], "hello") {
		t.Fatalf("result = %q, want the message row inside the banner", text)
	}
	nextIdx := strings.Index(text, "next_cursor: 57")
	if nextIdx == -1 || nextIdx < closeIdx {
		t.Fatalf("result = %q, want next_cursor 57 outside (after) the banner", text)
	}
}

func TestPollNewMessagesRejectsBadCursor(t *testing.T) {
	d := &toolDeps{st: &fakeStore{}, live: &fakeLive{}}

	_, _, err := d.pollNewMessages(context.Background(), nil, pollNewMessagesInput{Cursor: "banana"})
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("error = %v, want a cursor validation error", err)
	}
}

// With a timeout, the handler keeps checking until rows appear.
func TestPollNewMessagesLongPollReleasesOnArrival(t *testing.T) {
	st := &fakeStore{
		afterRowsEmptyCalls: 2, // empty twice, then the message "arrives"
		afterRowsRet:        []store.MessageRow{{ChatJID: "c@g.us", ID: "N2", SenderName: "Bob", TS: 901, Kind: "text", Text: "finally"}},
		afterRowsNext:       88,
	}
	d := &toolDeps{st: st, live: &fakeLive{}, pollEvery: 10 * time.Millisecond}

	result, _, err := d.pollNewMessages(context.Background(), nil, pollNewMessagesInput{Cursor: "41", TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("pollNewMessages() error = %v", err)
	}
	if st.afterRowIDCalls < 3 {
		t.Fatalf("store queried %d times, want at least 3 (two empty checks, then the arrival)", st.afterRowIDCalls)
	}
	if !strings.Contains(resultText(t, result), "next_cursor: 88") {
		t.Fatalf("result = %q, want the advanced cursor", resultText(t, result))
	}
}

func TestPollNewMessagesLongPollTimesOutEmpty(t *testing.T) {
	st := &fakeStore{afterRowsEmptyCalls: 1 << 30}
	d := &toolDeps{st: st, live: &fakeLive{}, pollEvery: 10 * time.Millisecond}

	start := time.Now()
	result, _, err := d.pollNewMessages(context.Background(), nil, pollNewMessagesInput{Cursor: "41", TimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("pollNewMessages() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("long poll returned after %v, want ~1s", elapsed)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "no new messages") || !strings.Contains(text, "next_cursor: 41") {
		t.Fatalf("timeout result = %q, want a no-new-messages notice with the unchanged cursor", text)
	}
}

func TestPollNewMessagesPassesFiltersThrough(t *testing.T) {
	st := &fakeStore{}
	d := &toolDeps{st: st, live: &fakeLive{}}

	_, _, err := d.pollNewMessages(context.Background(), nil, pollNewMessagesInput{
		Cursor: "41", ChatJID: "team@g.us", IncludeOwn: true,
	})
	if err != nil {
		t.Fatalf("pollNewMessages() error = %v", err)
	}
	if st.afterRowIDChat != "team@g.us" || !st.afterRowIDIncludeOwn {
		t.Fatalf("store received chat=%q includeOwn=%v, want the inputs passed through", st.afterRowIDChat, st.afterRowIDIncludeOwn)
	}
}

func TestClampPollTimeout(t *testing.T) {
	cases := map[int]int{-5: 0, 0: 0, 60: 60, 240: 240, 100000: 240}
	for in, want := range cases {
		if got := clampPollTimeout(in); got != want {
			t.Errorf("clampPollTimeout(%d) = %d, want %d", in, got, want)
		}
	}
}
