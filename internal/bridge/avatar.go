package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow"
)

// ErrNoProfilePicture reports that a JID definitively has no profile
// picture visible to this account — either none is set or it is hidden by
// the owner's privacy settings. Callers can cache this result long-term;
// any other error from ProfilePicture is transient.
var ErrNoProfilePicture = errors.New("no profile picture")

// avatarFetchTimeout bounds the HTTP fetch of the resolved picture URL.
// Preview thumbnails are a few kilobytes; anything slower than this is a
// stall, not a download.
const avatarFetchTimeout = 15 * time.Second

// avatarMaxBytes caps how much of a picture response is read. Preview
// thumbnails are far smaller; the cap only exists so a misbehaving server
// cannot grow this process without bound.
const avatarMaxBytes = 5 << 20

// ProfilePicture fetches the preview-size profile picture for jid (a user
// or a group), live from WhatsApp. It returns ErrNoProfilePicture when
// the JID has none visible to this account. The caller is expected to
// cache and rate-limit: every call is live WhatsApp traffic.
func (b *Bridge) ProfilePicture(ctx context.Context, jid string) ([]byte, error) {
	parsed, err := parseRecipient(jid)
	if err != nil {
		return nil, err
	}
	info, err := b.wa().GetProfilePictureInfo(ctx, parsed, &whatsmeow.GetProfilePictureParams{Preview: true})
	if err != nil {
		return nil, b.profilePictureErr(err)
	}
	if info == nil || info.URL == "" {
		return nil, ErrNoProfilePicture
	}
	return b.fetchAvatar(ctx, info.URL)
}

// profilePictureErr maps whatsmeow's definitive not-set and hidden
// sentinels to ErrNoProfilePicture and classifies everything else through
// the usual category-only path, so no underlying protocol detail escapes.
func (b *Bridge) profilePictureErr(err error) error {
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) || errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) {
		return ErrNoProfilePicture
	}
	return b.waErr("fetch profile picture", err)
}

// fetchAvatar downloads the resolved picture URL. Errors are category-only
// — the URL identifies whose picture was requested and never leaves here.
func (b *Bridge) fetchAvatar(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.New("fetch profile picture: invalid picture URL")
	}
	resp, err := b.avatarHTTP.Do(req)
	if err != nil {
		return nil, errors.New("fetch profile picture: request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch profile picture: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, avatarMaxBytes))
	if err != nil {
		return nil, errors.New("fetch profile picture: read failed")
	}
	return data, nil
}
