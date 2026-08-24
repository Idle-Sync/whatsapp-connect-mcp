package medianame

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedFilename(t *testing.T) {
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
		{"empty message id is rejected", "", "document", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SavedFilename(c.messageID, c.kind, c.senderFilename)
			if c.wantErr {
				if err == nil {
					t.Fatalf("SavedFilename(%q, %q, %q) error = nil, want error", c.messageID, c.kind, c.senderFilename)
				}
				return
			}
			if err != nil {
				t.Fatalf("SavedFilename(%q, %q, %q) error = %v, want nil", c.messageID, c.kind, c.senderFilename, err)
			}
			if got != c.want {
				t.Fatalf("SavedFilename(%q, %q, %q) = %q, want %q", c.messageID, c.kind, c.senderFilename, got, c.want)
			}
		})
	}
}

func TestChatDir(t *testing.T) {
	cases := []struct {
		name    string
		chatJID string
		wantDir string // final path component under <dataDir>/media
		wantErr bool
	}{
		{"plain jid", "chat123@s.whatsapp.net", "chat123@s.whatsapp.net", false},
		{"linked-device colon is replaced", "123:45@s.whatsapp.net", "123_45@s.whatsapp.net", false},
		{"separators are neutralized", `a/b\c@g.us`, "a_b_c@g.us", false},
		{"empty jid is rejected", "  ", "", true},
		{"traversal jid is rejected", "../../x@s.whatsapp.net", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ChatDir("data", c.chatJID)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ChatDir(%q) error = nil, want error", c.chatJID)
				}
				return
			}
			if err != nil {
				t.Fatalf("ChatDir(%q) error = %v, want nil", c.chatJID, err)
			}
			want := filepath.Join("data", "media", c.wantDir)
			if got != want {
				t.Fatalf("ChatDir(%q) = %q, want %q", c.chatJID, got, want)
			}
			if strings.Contains(got, "..") {
				t.Fatalf("ChatDir(%q) = %q contains a traversal component", c.chatJID, got)
			}
		})
	}
}
