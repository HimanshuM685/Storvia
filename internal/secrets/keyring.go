// Package secrets is the boundary between Storvia and key material.
//
// Refresh tokens are encrypted before they reach the database and decrypted only
// inside the Drive adapter. Application rows hold ciphertext plus a key
// reference, never a usable token. The key itself comes from the environment and
// is never written to a config file, a log, or an API response.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrNoKey reports that no active key is configured for a reference.
var ErrNoKey = errors.New("secrets: no key configured for the reference")

// ErrDecrypt reports that ciphertext could not be decrypted, which usually means
// the wrong key or a rotated key whose predecessor was dropped.
var ErrDecrypt = errors.New("secrets: ciphertext could not be decrypted")

// Keyring encrypts and decrypts secret material.
type Keyring interface {
	// Encrypt seals plaintext under the active key for the given reference.
	Encrypt(reference string, plaintext []byte) ([]byte, error)
	// Decrypt opens ciphertext produced by Encrypt for the same reference.
	Decrypt(reference string, ciphertext []byte) ([]byte, error)
	// ActiveReference names the key used for new encryptions.
	ActiveReference() string
}

// AESGCMKeyring is the production Keyring.
//
// It supports a previous key so a rotation can be rolled out without invalidating
// every stored refresh token at once: new writes use the active key, and reads
// transparently accept the predecessor until it is retired.
type AESGCMKeyring struct {
	active   string
	previous string
	keys     map[string][]byte
}

// KeyringOptions configures AESGCMKeyring.
type KeyringOptions struct {
	// ActiveReference is the identifier stored alongside new ciphertext.
	ActiveReference string
	// ActiveKey is the 32-byte key, hex or base64 encoded, from the environment.
	ActiveKey string
	// PreviousReference and PreviousKey enable rotation overlap.
	PreviousReference string
	PreviousKey       string
}

// NewAESGCMKeyring builds a keyring and validates every key up front.
//
// Failing at startup is deliberate: a process that cannot decrypt its stored
// tokens should not accept uploads it can never complete.
func NewAESGCMKeyring(opts KeyringOptions) (*AESGCMKeyring, error) {
	if opts.ActiveReference == "" {
		return nil, errors.New("secrets: an active key reference is required")
	}
	active, err := decodeKey(opts.ActiveKey)
	if err != nil {
		return nil, fmt.Errorf("secrets: active key: %w", err)
	}

	keyring := &AESGCMKeyring{
		active:   opts.ActiveReference,
		previous: opts.PreviousReference,
		keys:     map[string][]byte{opts.ActiveReference: active},
	}

	if opts.PreviousKey != "" {
		if opts.PreviousReference == "" {
			return nil, errors.New("secrets: a previous key was given without a reference")
		}
		previous, err := decodeKey(opts.PreviousKey)
		if err != nil {
			return nil, fmt.Errorf("secrets: previous key: %w", err)
		}
		keyring.keys[opts.PreviousReference] = previous
	}
	return keyring, nil
}

// decodeKey accepts a 32-byte key encoded as hex or base64.
func decodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("key is empty")
	}
	if len(value) == 64 {
		if decoded, err := hexDecode(value); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	if decoded, err := base64Decode(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, errors.New("key must decode to exactly 32 bytes")
}

// Encrypt seals plaintext under the active key.
//
// The nonce is random per call and prefixed to the ciphertext, so encrypting the
// same token twice never produces the same output.
func (k *AESGCMKeyring) Encrypt(reference string, plaintext []byte) ([]byte, error) {
	key, ok := k.keys[reference]
	if !ok {
		// Fall back to the active key when the caller passes no reference, so the
		// common path does not have to thread one through.
		if reference != "" {
			return nil, ErrNoKey
		}
		key = k.keys[k.active]
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens ciphertext, trying the referenced key and then the active one.
//
// The second attempt is what makes rotation work: ciphertext written under the
// previous key carries the previous reference, but if the reference column was
// lost the active key still gives a chance to recover the value rather than
// forcing a reconnect for every user.
func (k *AESGCMKeyring) Decrypt(reference string, ciphertext []byte) ([]byte, error) {
	attempts := make([]string, 0, 3)
	if reference != "" {
		attempts = append(attempts, reference)
	}
	for name := range k.keys {
		if name != reference {
			attempts = append(attempts, name)
		}
	}

	for _, name := range attempts {
		key, ok := k.keys[name]
		if !ok {
			continue
		}
		gcm, err := newGCM(key)
		if err != nil {
			continue
		}
		if len(ciphertext) < gcm.NonceSize() {
			continue
		}
		nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
		plaintext, err := gcm.Open(nil, nonce, body, nil)
		if err == nil {
			return plaintext, nil
		}
	}
	return nil, ErrDecrypt
}

// ActiveReference names the key used for new encryptions.
func (k *AESGCMKeyring) ActiveReference() string { return k.active }

// Rotate retires the active key in favour of the previous one, which is how an
// operator completes a rotation once every row has been re-encrypted.
func (k *AESGCMKeyring) Rotate(newActive, newKey string) error {
	key, err := decodeKey(newKey)
	if err != nil {
		return fmt.Errorf("secrets: new active key: %w", err)
	}
	if old, ok := k.keys[k.active]; ok {
		k.keys[newActive] = old
		k.previous = k.active
	}
	k.keys[newActive] = key
	k.active = newActive
	return nil
}

// References lists configured key references, for diagnostics.
func (k *AESGCMKeyring) References() []string {
	out := make([]string, 0, len(k.keys))
	for name := range k.keys {
		out = append(out, name)
	}
	return out
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	return gcm, nil
}
