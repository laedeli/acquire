// Package secretbox encrypts the credentials acquire stores for the systems it
// configures: download client passwords and tokens, search source API keys, the
// full release links that carry them.
//
// The rules are deliberately narrow:
//
//   - AES-256-GCM with a random nonce per value. The ciphertext is nonce||sealed.
//   - The key comes from ACQUIRE_CONFIG_KEY (base64, exactly 32 bytes). An old
//     key can be kept in ACQUIRE_CONFIG_KEY_PREVIOUS so values written under it
//     still open during a rotation; new values are always sealed under the
//     current key.
//   - Every value is bound to where it lives with the additional data
//     "table|id|field", so a ciphertext copied into another row or column fails
//     to open instead of quietly becoming someone else's password.
//   - There is no plaintext fallback. Without a key, Seal refuses.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNoKey means no usable ACQUIRE_CONFIG_KEY is configured. Writes that carry a
// secret must be refused; reads of stored secrets cannot succeed.
var ErrNoKey = errors.New("no ACQUIRE_CONFIG_KEY configured: credentials cannot be stored")

// ErrUnknownKey means a value was sealed under a key that is neither the current
// nor the previous one.
var ErrUnknownKey = errors.New("secret was sealed under a key that is no longer configured")

type key struct {
	aead cipher.AEAD
	kid  string
}

// Box seals and opens values. The zero value and a nil *Box have no key.
type Box struct {
	current  *key
	previous *key
	// problem explains why there is no usable key when one was supplied but
	// could not be used. It never contains key material.
	problem string
}

// New builds a box from base64 key material. An empty current key yields a box
// without a key (Enabled false) and no error. A malformed key is an error; the
// returned box is still usable as "no key" so a caller can keep serving.
func New(currentB64, previousB64 string) (*Box, error) {
	b := &Box{}
	currentB64 = strings.TrimSpace(currentB64)
	previousB64 = strings.TrimSpace(previousB64)
	if currentB64 == "" {
		if previousB64 != "" {
			b.problem = "ACQUIRE_CONFIG_KEY_PREVIOUS is set without ACQUIRE_CONFIG_KEY"
			return b, errors.New(b.problem)
		}
		return b, nil
	}
	cur, err := parseKey(currentB64)
	if err != nil {
		b.problem = "ACQUIRE_CONFIG_KEY is malformed: " + err.Error()
		return b, errors.New(b.problem)
	}
	b.current = cur
	if previousB64 != "" {
		prev, err := parseKey(previousB64)
		if err != nil {
			// The current key still works; values under the previous one will
			// fail to open with ErrUnknownKey, which is reported per value.
			b.problem = "ACQUIRE_CONFIG_KEY_PREVIOUS is malformed: " + err.Error()
			return b, errors.New(b.problem)
		}
		if prev.kid != cur.kid {
			b.previous = prev
		}
	}
	return b, nil
}

// FromEnv reads ACQUIRE_CONFIG_KEY and ACQUIRE_CONFIG_KEY_PREVIOUS.
func FromEnv() (*Box, error) {
	return New(os.Getenv("ACQUIRE_CONFIG_KEY"), os.Getenv("ACQUIRE_CONFIG_KEY_PREVIOUS"))
}

func parseKey(b64 string) (*key, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// Accept the URL-safe alphabet too; `openssl rand -base64 32` and a
		// hand-rolled generator disagree on it more often than anyone expects.
		if raw2, err2 := base64.RawURLEncoding.DecodeString(strings.TrimRight(b64, "=")); err2 == nil {
			raw, err = raw2, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("not base64")
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("decodes to %d bytes, want 32", len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return &key{aead: aead, kid: hex.EncodeToString(sum[:])[:8]}, nil
}

// Enabled reports whether values can be sealed.
func (b *Box) Enabled() bool { return b != nil && b.current != nil }

// KID is the current key's id (first 8 hex characters of its SHA-256), or "".
func (b *Box) KID() string {
	if !b.Enabled() {
		return ""
	}
	return b.current.kid
}

// Problem explains a configured-but-unusable key, or "" when there is none.
func (b *Box) Problem() string {
	if b == nil {
		return ""
	}
	return b.problem
}

func aad(table, id, field string) []byte {
	return []byte(table + "|" + id + "|" + field)
}

// Seal encrypts plaintext for table/id/field under the current key and returns
// the ciphertext with the id of the key that sealed it.
func (b *Box) Seal(table, id, field string, plaintext []byte) (ct []byte, kid string, err error) {
	if !b.Enabled() {
		return nil, "", ErrNoKey
	}
	k := b.current
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	out := k.aead.Seal(nonce, nonce, plaintext, aad(table, id, field))
	return out, k.kid, nil
}

// Open decrypts a value sealed by Seal for the same table/id/field. kid selects
// the key; an empty kid tries the current key.
func (b *Box) Open(table, id, field string, ct []byte, kid string) ([]byte, error) {
	if b == nil || (b.current == nil && b.previous == nil) {
		return nil, ErrNoKey
	}
	var k *key
	switch {
	case b.current != nil && (kid == "" || kid == b.current.kid):
		k = b.current
	case b.previous != nil && kid == b.previous.kid:
		k = b.previous
	default:
		return nil, ErrUnknownKey
	}
	n := k.aead.NonceSize()
	if len(ct) < n+k.aead.Overhead() {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	pt, err := k.aead.Open(nil, ct[:n], ct[n:], aad(table, id, field))
	if err != nil {
		// Deliberately vague: the reason is either tampering or a value moved
		// to a different row, and neither deserves more detail in a log.
		return nil, errors.New("secretbox: value does not open for this row")
	}
	return pt, nil
}
