package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// TestDownloadMediaRejectsTraversalFilename proves the fix for a path
// traversal: MessageMediaRef's filename comes from the remote sender (the
// WhatsApp message's own file name), so DownloadMedia must reject one that
// tries to escape destDir instead of joining it in blindly. The message
// carries no downloadable attachment, so if DownloadMedia reached
// b.downloadMessage at all it would fail there instead (with a different,
// unrelated error) — asserting the specific "invalid media filename"
// category is what proves the filename check runs, and runs first, rather
// than merely that DownloadMedia failed for some reason. Sanitization
// happens before any network call, so this never touches whatsmeow (the
// client here is never connected, same as newTestBridge's other callers).
func TestDownloadMediaRejectsTraversalFilename(t *testing.T) {
	b, _ := newTestBridge(t)

	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "media", "chat@s.whatsapp.net")

	ref, err := proto.Marshal(&waE2E.Message{})
	if err != nil {
		t.Fatalf("marshal empty message: %v", err)
	}

	_, err = b.DownloadMedia(context.Background(), ref, destDir, "../../../../evil.txt")
	if err == nil {
		t.Fatal("DownloadMedia() error = nil, want an error for a traversal filename")
	}
	if !strings.Contains(err.Error(), "invalid media filename") {
		t.Fatalf("DownloadMedia() error = %q, want it to identify an invalid filename (proves the check ran, not some other failure)", err.Error())
	}

	if _, statErr := os.Stat(filepath.Join(tmp, "evil.txt")); !os.IsNotExist(statErr) {
		t.Fatal("DownloadMedia() wrote a file outside destDir despite rejecting the traversal filename")
	}
	if _, statErr := os.Stat(destDir); !os.IsNotExist(statErr) {
		t.Fatal("DownloadMedia() created destDir despite rejecting the filename before any write")
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
