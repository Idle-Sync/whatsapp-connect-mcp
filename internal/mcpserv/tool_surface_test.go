package mcpserv

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idle-sync/whatsapp-connect-mcp/internal/schedule"
)

// connectTestClient builds the full tool surface (as serve does) and
// returns a client session talking to it over an in-memory transport.
// configure runs on the server before the client connects.
func connectTestClient(t *testing.T, configure func(*mcp.Server)) *mcp.ClientSession {
	t.Helper()
	sched, _, err := schedule.Load(t.TempDir(), time.Now())
	if err != nil {
		t.Fatalf("schedule.Load: %v", err)
	}
	server := New(&fakeStore{}, nil, &fakeLive{}, nil, &Scheduler{Store: sched}, t.TempDir(), DoctorEnv{})
	if configure != nil {
		configure(server)
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listTools(t *testing.T, cs *mcp.ClientSession) []*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	return res.Tools
}

// hints is one tool's annotations as plain values. destructive is only
// meaningful (and only compared) when readOnly is false.
type hints struct {
	readOnly, destructive, idempotent, openWorld bool
}

// wantHints is the annotation every tool must carry. "Local" tools touch
// only this machine; the rest reach WhatsApp's servers (or, for doctor,
// GitHub's release API). Changing a row here should mean the tool's
// behaviour changed.
var wantHints = map[string]hints{
	// Reads of the local message and schedule stores.
	"list_chats":           {readOnly: true, idempotent: true},
	"get_chat":             {readOnly: true, idempotent: true},
	"list_messages":        {readOnly: true, idempotent: true},
	"search_messages":      {readOnly: true, idempotent: true},
	"get_message_context":  {readOnly: true, idempotent: true},
	"search_contacts":      {readOnly: true, idempotent: true},
	"get_last_interaction": {readOnly: true, idempotent: true},
	"get_call_history":     {readOnly: true, idempotent: true},
	"poll_new_messages":    {readOnly: true, idempotent: true},
	"list_scheduled":       {readOnly: true, idempotent: true},
	// Reads fetched live.
	"list_group_participants": {readOnly: true, idempotent: true, openWorld: true},
	"get_group_info":          {readOnly: true, idempotent: true, openWorld: true},
	"get_blocklist":           {readOnly: true, idempotent: true, openWorld: true},
	"doctor":                  {readOnly: true, idempotent: true, openWorld: true},
	// Fetch and write locally. download_media saves under a name derived
	// from the message id, so a repeat overwrites the same file;
	// fetch_older_messages anchors each call on the oldest stored message,
	// so a repeat pages further back.
	"download_media":       {idempotent: true, openWorld: true},
	"fetch_older_messages": {openWorld: true},
	// Each call puts something new in a chat.
	"send_message":    {openWorld: true},
	"send_media":      {openWorld: true},
	"send_voice_note": {openWorld: true},
	"create_poll":     {openWorld: true},
	"schedule_send":   {openWorld: true},
	// Set a state that repeating leaves unchanged.
	"send_reaction":   {idempotent: true, openWorld: true},
	"edit_message":    {idempotent: true, openWorld: true},
	"mark_read":       {idempotent: true, openWorld: true},
	"block_contact":   {idempotent: true, openWorld: true},
	"unblock_contact": {idempotent: true, openWorld: true},
	// Remove something for good.
	"delete_message":   {destructive: true, idempotent: true, openWorld: true},
	"cancel_scheduled": {destructive: true, idempotent: true},
}

func TestToolAnnotations(t *testing.T) {
	tools := listTools(t, connectTestClient(t, nil))
	if len(tools) != len(wantHints) {
		t.Errorf("tools/list returned %d tools, the table has %d", len(tools), len(wantHints))
	}
	for _, tool := range tools {
		want, ok := wantHints[tool.Name]
		if !ok {
			t.Errorf("%s: not in wantHints; add a row for it", tool.Name)
			continue
		}
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s: no annotations", tool.Name)
			continue
		}
		// Both pointer hints default to true when absent, so leaving one
		// out is a claim, not a gap: every tool states openWorldHint, and
		// every tool that writes states destructiveHint.
		if a.OpenWorldHint == nil || (!a.ReadOnlyHint && a.DestructiveHint == nil) {
			t.Errorf("%s: openWorldHint or destructiveHint left unset", tool.Name)
			continue
		}
		got := hints{readOnly: a.ReadOnlyHint, idempotent: a.IdempotentHint, openWorld: *a.OpenWorldHint}
		if !want.readOnly {
			got.destructive = *a.DestructiveHint
		}
		if got != want {
			t.Errorf("%s: annotations %+v, want %+v", tool.Name, got, want)
		}
	}
}

// TestEveryParameterHasDescription: a parameter without a description is
// one an agent has to guess at. Every input property needs one.
func TestEveryParameterHasDescription(t *testing.T) {
	for _, tool := range listTools(t, connectTestClient(t, nil)) {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal input schema: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: decode input schema: %v", tool.Name, err)
		}
		for name, prop := range schema.Properties {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s.%s: no description", tool.Name, name)
			}
		}
	}
}

// TestUnpairedServerListsToolsAndRefusesCalls: before pairing, the server
// still answers tools/list with every tool, every call but doctor returns
// the pairing error, and the guard lifts as soon as pairing lands.
func TestUnpairedServerListsToolsAndRefusesCalls(t *testing.T) {
	var paired atomic.Bool
	cs := connectTestClient(t, func(s *mcp.Server) {
		RequirePairing(s, func() bool { return !paired.Load() })
	})

	if got := len(listTools(t, cs)); got != len(wantHints) {
		t.Fatalf("unpaired tools/list returned %d tools, want %d", got, len(wantHints))
	}

	call := func() *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "get_chat", Arguments: map[string]any{"chat_jid": "x@s.whatsapp.net"},
		})
		if err != nil {
			t.Fatalf("tools/call: %v", err)
		}
		return res
	}

	res := call()
	if !res.IsError {
		t.Fatal("an unpaired get_chat succeeded, want the pairing error")
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != NotPairedMessage {
		t.Fatalf("unpaired get_chat said %q, want NotPairedMessage", text)
	}

	paired.Store(true)
	if res := call(); res.IsError && res.Content[0].(*mcp.TextContent).Text == NotPairedMessage {
		t.Fatal("get_chat still returns the pairing error after pairing")
	}
}
