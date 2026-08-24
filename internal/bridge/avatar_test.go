package bridge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestProfilePictureErrMapsDefinitiveAbsenceToSentinel(t *testing.T) {
	b, _ := newTestBridge(t)
	cases := []struct {
		name string
		in   error
		want error // nil means: any category error that is NOT ErrNoProfilePicture
	}{
		{"not set", whatsmeow.ErrProfilePictureNotSet, ErrNoProfilePicture},
		{"hidden", whatsmeow.ErrProfilePictureUnauthorized, ErrNoProfilePicture},
		{"anything else stays a category error", errors.New("iq error 500: raw node with jid"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := b.profilePictureErr(c.in)
			if c.want != nil {
				if !errors.Is(got, c.want) {
					t.Fatalf("profilePictureErr(%v) = %v, want ErrNoProfilePicture", c.in, got)
				}
				return
			}
			if errors.Is(got, ErrNoProfilePicture) {
				t.Fatalf("profilePictureErr(%v) = ErrNoProfilePicture, want a plain category error", c.in)
			}
			if strings.Contains(got.Error(), "raw node") {
				t.Fatalf("profilePictureErr leaked the underlying error text: %q", got.Error())
			}
		})
	}
}

func TestProfilePictureRejectsInvalidJID(t *testing.T) {
	b, _ := newTestBridge(t)
	if _, err := b.ProfilePicture(context.Background(), ""); err == nil {
		t.Fatal("ProfilePicture(\"\") error = nil, want an error")
	}
}

func TestFetchAvatarReturnsBodyAndHidesURLOnFailure(t *testing.T) {
	b, _ := newTestBridge(t)

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer ok.Close()
	data, err := b.fetchAvatar(context.Background(), ok.URL)
	if err != nil {
		t.Fatalf("fetchAvatar() error = %v", err)
	}
	if string(data) != "jpeg-bytes" {
		t.Fatalf("fetchAvatar() = %q, want the response body", data)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := b.fetchAvatar(context.Background(), bad.URL); err == nil {
		t.Fatal("fetchAvatar() error = nil on a 500, want an error")
	} else if strings.Contains(err.Error(), bad.URL) {
		t.Fatalf("fetchAvatar() error leaked the URL: %q", err.Error())
	}
}
