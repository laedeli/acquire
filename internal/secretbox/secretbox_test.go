package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"
)

func newKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := New(newKey(t), "")
	if err != nil {
		t.Fatal(err)
	}
	ct, kid, err := b.Seal("download_clients", "nzbget", "secret", []byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("hunter2")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(kid) || kid != b.KID() {
		t.Fatalf("kid = %q, want 8 hex chars equal to KID() %q", kid, b.KID())
	}
	pt, err := b.Open("download_clients", "nzbget", "secret", ct, kid)
	if err != nil || string(pt) != "hunter2" {
		t.Fatalf("open = %q, %v", pt, err)
	}
}

// Two seals of the same value must differ: a fixed nonce would let anyone with
// read access to the table see which clients share a password.
func TestSealUsesAFreshNonce(t *testing.T) {
	b, _ := New(newKey(t), "")
	a, _, _ := b.Seal("t", "1", "f", []byte("same"))
	c, _, _ := b.Seal("t", "1", "f", []byte("same"))
	if bytes.Equal(a, c) {
		t.Fatal("two seals of the same value are identical")
	}
}

// A ciphertext is bound to its row and column. Copying one client's secret
// into another row must not hand that row the password.
func TestAdditionalDataBindsTheValueToItsRow(t *testing.T) {
	b, _ := New(newKey(t), "")
	ct, kid, _ := b.Seal("download_clients", "nzbget", "secret", []byte("pw"))
	for _, c := range []struct{ table, id, field string }{
		{"download_clients", "qbittorrent", "secret"},
		{"download_clients", "nzbget", "username"},
		{"indexers", "nzbget", "secret"},
	} {
		if _, err := b.Open(c.table, c.id, c.field, ct, kid); err == nil {
			t.Errorf("value sealed for download_clients|nzbget|secret opened as %s|%s|%s", c.table, c.id, c.field)
		}
	}
}

func TestPreviousKeyStillOpensDuringRotation(t *testing.T) {
	oldKey, newKeyB64 := newKey(t), newKey(t)
	old, _ := New(oldKey, "")
	ct, kid, _ := old.Seal("t", "1", "f", []byte("v"))

	rotated, err := New(newKeyB64, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.KID() == kid {
		t.Fatal("rotation did not change the current key id")
	}
	pt, err := rotated.Open("t", "1", "f", ct, kid)
	if err != nil || string(pt) != "v" {
		t.Fatalf("previous-key value did not open: %q %v", pt, err)
	}
	// New values go under the new key only.
	_, newKID, _ := rotated.Seal("t", "1", "f", []byte("v"))
	if newKID != rotated.KID() {
		t.Fatalf("sealed under %q, want the current key %q", newKID, rotated.KID())
	}

	// Once the previous key is dropped, the old value is honestly unreadable.
	dropped, _ := New(newKeyB64, "")
	if _, err := dropped.Open("t", "1", "f", ct, kid); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
}

func TestNoKeyRefusesInsteadOfStoringPlaintext(t *testing.T) {
	for _, b := range []*Box{nil, {}} {
		if b.Enabled() {
			t.Fatal("a box without a key reports enabled")
		}
		if _, _, err := b.Seal("t", "1", "f", []byte("v")); !errors.Is(err, ErrNoKey) {
			t.Fatalf("seal without a key: %v, want ErrNoKey", err)
		}
		if _, err := b.Open("t", "1", "f", []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"), ""); !errors.Is(err, ErrNoKey) {
			t.Fatalf("open without a key: %v, want ErrNoKey", err)
		}
	}
	b, err := New("", "")
	if err != nil || b.Enabled() {
		t.Fatalf("an unset key is not an error, just no key: %v %v", err, b.Enabled())
	}
}

func TestMalformedKeysAreReportedWithoutKeyMaterial(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("sixteen byte key"))
	for _, in := range []string{"not base64 !!", short} {
		b, err := New(in, "")
		if err == nil {
			t.Fatalf("key %q accepted", in)
		}
		if b == nil || b.Enabled() {
			t.Fatal("a malformed key must leave a usable box without a key")
		}
		if b.Problem() == "" || strings.Contains(b.Problem(), in) {
			t.Fatalf("problem %q must explain without echoing the key", b.Problem())
		}
	}
	if _, err := New("", newKey(t)); err == nil {
		t.Fatal("a previous key without a current one must be reported")
	}
}

func TestURLSafeKeyIsAccepted(t *testing.T) {
	raw := bytes.Repeat([]byte{0xfb}, 32) // encodes with '-' and '_' in URL-safe form
	b, err := New(base64.RawURLEncoding.EncodeToString(raw), "")
	if err != nil || !b.Enabled() {
		t.Fatalf("url-safe key rejected: %v", err)
	}
}
