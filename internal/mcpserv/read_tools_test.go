package mcpserv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

	messagesLimit int
	messagesRet   []store.MessageRow
	messagesErr   error

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

	callsLimit int
	callsRet   []store.CallRow
	callsErr   error

	mediaRef      []byte
	mediaFilename string
	mediaKind     string
	mediaErr      error
}

func (f *fakeStore) Chats(_ string, _ bool, limit int) ([]store.ChatRow, error) {
	_ = limit
	return f.chatsRet, f.chatsErr
}

func (f *fakeStore) Chat(_ string) (store.ChatRow, bool, error) {
	return f.chatRet, f.chatOK, f.chatErr
}

func (f *fakeStore) Messages(_ string, _, _ int64, limit int) ([]store.MessageRow, error) {
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

func (f *fakeStore) Calls(_ string, limit int) ([]store.CallRow, error) {
	f.callsLimit = limit
	return f.callsRet, f.callsErr
}

func (f *fakeStore) MessageMediaRef(_, _ string) ([]byte, string, string, error) {
	return f.mediaRef, f.mediaFilename, f.mediaKind, f.mediaErr
}

// fakeLive records the destDir/filename it was asked to download into.
type fakeLive struct {
	participants    []string
	participantsErr error

	downloadDestDir  string
	downloadFilename string
	downloadPath     string
	downloadErr      error
}

func (f *fakeLive) GroupParticipants(_ context.Context, _ string) ([]string, error) {
	return f.participants, f.participantsErr
}

func (f *fakeLive) DownloadMedia(_ context.Context, _ []byte, destDir, filename string) (string, error) {
	f.downloadDestDir = destDir
	f.downloadFilename = filename
	if f.downloadErr != nil {
		return "", f.downloadErr
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
	wantPath := filepath.Join(wantDestDir, "photo.jpg")

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
	if live.downloadFilename != "photo.jpg" {
		t.Fatalf("DownloadMedia called with filename = %q, want %q", live.downloadFilename, "photo.jpg")
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
	if !strings.Contains(text[:closeIdx], "photo.jpg") {
		t.Fatalf("downloadMedia() WhatsApp-derived filename is not inside the banner: %q", text)
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

func TestRegisterReadToolsBuildsAllTenSchemasWithoutPanicking(t *testing.T) {
	// mcp.AddTool panics if a tool's input type cannot produce a valid JSON
	// schema. Exercising the full registration path (rather than only the
	// handler methods, as the other tests in this file do) is what would
	// catch a struct tag or type mistake in any of the ten tools.
	server := New(&fakeStore{}, &fakeLive{}, nil, t.TempDir())
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
