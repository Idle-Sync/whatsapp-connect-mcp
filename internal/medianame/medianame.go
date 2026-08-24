// Package medianame decides where downloaded WhatsApp media lives on
// disk: the per-chat directory under the data dir and the message-id-
// derived filename inside it. The MCP download_media tool and the
// dashboard's media endpoint share these so the same message always
// resolves to the same local file — one download serves both.
package medianame

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// chatDirReplacer maps JID characters that are invalid in a Windows path
// component (':' shows up in linked-device JIDs like
// "1234567890:12@s.whatsapp.net") to a safe substitute, and neutralizes
// any path separator so a chat JID can never redirect the write outside
// dataDir/media.
var chatDirReplacer = strings.NewReplacer(":", "_", "/", "_", `\`, "_")

// ChatDir returns the directory media for chatJID is saved into, per
// ARCHITECTURE.md §2: <dataDir>/media/<chat>/.
func ChatDir(dataDir, chatJID string) (string, error) {
	if strings.TrimSpace(chatJID) == "" {
		return "", errors.New("chat_jid is required")
	}
	if strings.Contains(chatJID, "..") {
		return "", errors.New("invalid chat_jid")
	}
	return filepath.Join(dataDir, "media", chatDirReplacer.Replace(chatJID)), nil
}

// SavedFilename derives the filename downloaded media is written under:
// always <messageID><ext>, never the sender-declared filename. Using the
// message id — a value that must already match a row this server stored,
// not free text from the remote sender — means the sender can never
// choose any part of the saved path, and two media messages in the same
// chat can never collide on the file they write to, the way two
// sender-chosen "IMG_0001.jpg" names could. The extension is fixed per
// kind, except for documents, whose type is meaningful (.pdf vs .docx):
// those keep the extension off the sender-declared name (safe to read
// even from an untrusted string — filepath.Ext only looks at the suffix
// after the final dot in the final path element) and fall back to ".bin"
// when it has none. The derived name is still validated as a single path
// component: WhatsApp message ids originate with the sending client too,
// so the same check that guards a filename guards this.
func SavedFilename(messageID, kind, senderFilename string) (string, error) {
	var ext string
	switch kind {
	case "image":
		ext = ".jpg"
	case "video", "video_note":
		ext = ".mp4"
	case "audio", "voice":
		ext = ".ogg"
	case "sticker":
		ext = ".webp"
	case "document":
		ext = filepath.Ext(senderFilename)
		if ext == "" {
			ext = ".bin"
		}
	default:
		ext = ".bin"
	}

	name := messageID + ext
	base := filepath.Base(name)
	if base != name || messageID == "" || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("derive saved media filename: %w", errors.New("invalid media filename"))
	}
	return name, nil
}
