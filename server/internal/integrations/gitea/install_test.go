package gitea

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func mustNewBox(t *testing.T) *secretbox.Box {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	return box
}

// TestInstallService_TokenRoundTrip guards the encrypt-at-rest path: Connect
// seals the token with base64(secretbox ciphertext) (mirroring
// slack/byo_install.go), and DecryptToken must reverse it exactly, including
// tolerating PostgreSQL's MIME-wrapped base64 output.
func TestInstallService_TokenRoundTrip(t *testing.T) {
	box := mustNewBox(t)
	s := NewInstallService(nil, box, "https://gitea.internal.example.com", nil)

	sealed, err := box.Seal([]byte("my-personal-access-token"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(sealed)

	got, err := s.DecryptToken(encoded)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if got != "my-personal-access-token" {
		t.Errorf("DecryptToken() = %q, want %q", got, "my-personal-access-token")
	}

	// PostgreSQL's encode(...,'base64') wraps output every 76 chars with a
	// trailing newline; decryptToken must tolerate that shape identically.
	wrapped := wrapBase64(encoded, 16)
	got2, err := s.DecryptToken(wrapped)
	if err != nil {
		t.Fatalf("DecryptToken(wrapped): %v", err)
	}
	if got2 != "my-personal-access-token" {
		t.Errorf("DecryptToken(wrapped) = %q, want %q", got2, "my-personal-access-token")
	}
}

func TestInstallService_DecryptToken_TamperedRejected(t *testing.T) {
	box := mustNewBox(t)
	s := NewInstallService(nil, box, "https://gitea.internal.example.com", nil)

	sealed, err := box.Seal([]byte("token"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed[len(sealed)-1] ^= 0xFF // flip a byte in the GCM tag
	encoded := base64.StdEncoding.EncodeToString(sealed)

	if _, err := s.DecryptToken(encoded); err == nil {
		t.Error("expected tampered ciphertext to fail authentication")
	}
}

func wrapBase64(s string, width int) string {
	out := ""
	for len(s) > width {
		out += s[:width] + "\n"
		s = s[width:]
	}
	return out + s
}
