package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// testKey returns a deterministic 32-byte key.
func testKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func newKeyring(t *testing.T) *AESGCMKeyring {
	t.Helper()
	ring, err := NewAESGCMKeyring(KeyringOptions{
		ActiveReference: "kms://storvia/prod/drive",
		ActiveKey:       hex.EncodeToString(testKey(1)),
	})
	if err != nil {
		t.Fatalf("NewAESGCMKeyring: %v", err)
	}
	return ring
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	ring := newKeyring(t)
	plaintext := []byte("ya29.refresh-token-value")

	ciphertext, err := ring.Encrypt(ring.ActiveReference(), plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("the ciphertext contains the plaintext")
	}

	got, err := ring.Decrypt(ring.ActiveReference(), ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}
}

// TestEncryptionIsNotDeterministic matters because storing an identical token
// twice should not produce identical ciphertext: equal ciphertexts would leak that
// two users hold the same value.
func TestEncryptionIsNotDeterministic(t *testing.T) {
	ring := newKeyring(t)
	plaintext := []byte("same-token")

	first, err := ring.Encrypt(ring.ActiveReference(), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ring.Encrypt(ring.ActiveReference(), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Error("encrypting the same plaintext twice produced identical ciphertext; the nonce is not random")
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	encryptor := newKeyring(t)
	ciphertext, err := encryptor.Encrypt(encryptor.ActiveReference(), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	decryptor, err := NewAESGCMKeyring(KeyringOptions{
		ActiveReference: "kms://storvia/prod/drive",
		ActiveKey:       hex.EncodeToString(testKey(99)),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := decryptor.Decrypt(decryptor.ActiveReference(), ciphertext); !errors.Is(err, ErrDecrypt) {
		t.Errorf("Decrypt with the wrong key = %v, want ErrDecrypt", err)
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	ring := newKeyring(t)
	ciphertext, err := ring.Encrypt(ring.ActiveReference(), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	// Flip a byte in the body, past the nonce.
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0xff

	if _, err := ring.Decrypt(ring.ActiveReference(), tampered); !errors.Is(err, ErrDecrypt) {
		t.Errorf("Decrypt of tampered ciphertext = %v, want ErrDecrypt", err)
	}
}

func TestDecryptRejectsTruncatedCiphertext(t *testing.T) {
	ring := newKeyring(t)
	ciphertext, err := ring.Encrypt(ring.ActiveReference(), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ring.Decrypt(ring.ActiveReference(), ciphertext[:4]); !errors.Is(err, ErrDecrypt) {
		t.Errorf("Decrypt of a truncated value = %v, want ErrDecrypt", err)
	}
}

func TestEncryptRejectsUnknownReference(t *testing.T) {
	ring := newKeyring(t)
	if _, err := ring.Encrypt("kms://unknown", []byte("x")); !errors.Is(err, ErrNoKey) {
		t.Errorf("Encrypt with an unknown reference = %v, want ErrNoKey", err)
	}
}

func TestKeyFormatsAccepted(t *testing.T) {
	key := testKey(3)
	formats := map[string]string{
		"hex":    hex.EncodeToString(key),
		"base64": base64.StdEncoding.EncodeToString(key),
	}
	for name, encoded := range formats {
		t.Run(name, func(t *testing.T) {
			ring, err := NewAESGCMKeyring(KeyringOptions{ActiveReference: "ref", ActiveKey: encoded})
			if err != nil {
				t.Fatalf("NewAESGCMKeyring: %v", err)
			}
			ciphertext, err := ring.Encrypt(ring.ActiveReference(), []byte("value"))
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			got, err := ring.Decrypt(ring.ActiveReference(), ciphertext)
			if err != nil || string(got) != "value" {
				t.Errorf("round trip failed: %v / %q", err, got)
			}
		})
	}
}

func TestInvalidKeysAreRejectedAtStartup(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"too short hex", "abcd"},
		{"not a key at all", "this is definitely not a key"},
		{"valid hex wrong length", strings.Repeat("ab", 16)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewAESGCMKeyring(KeyringOptions{ActiveReference: "ref", ActiveKey: tc.key}); err == nil {
				t.Fatalf("key %q must be rejected at startup", tc.key)
			}
		})
	}
}

func TestMissingReferenceIsRejected(t *testing.T) {
	_, err := NewAESGCMKeyring(KeyringOptions{ActiveKey: hex.EncodeToString(testKey(1))})
	if err == nil {
		t.Fatal("a keyring without an active reference must be rejected")
	}
}

// TestRotationKeepsExistingCiphertextReadable is the property that makes a key
// rotation possible without signing every user out.
func TestRotationKeepsExistingCiphertextReadable(t *testing.T) {
	oldKey := testKey(1)
	newKey := testKey(200)

	ring, err := NewAESGCMKeyring(KeyringOptions{
		ActiveReference: "v1",
		ActiveKey:       hex.EncodeToString(oldKey),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Data written under v1.
	oldCiphertext, err := ring.Encrypt("v1", []byte("token-from-v1"))
	if err != nil {
		t.Fatal(err)
	}

	// Rotate: v2 becomes active, v1 becomes the predecessor.
	if err := ring.Rotate("v2", hex.EncodeToString(newKey)); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if ring.ActiveReference() != "v2" {
		t.Errorf("ActiveReference = %q, want v2", ring.ActiveReference())
	}

	// The old value must still decrypt, or every user would need to reconnect.
	got, err := ring.Decrypt("v1", oldCiphertext)
	if err != nil {
		t.Fatalf("pre-rotation ciphertext must still decrypt: %v", err)
	}
	if string(got) != "token-from-v1" {
		t.Errorf("Decrypt = %q, want token-from-v1", got)
	}

	// New writes use v2.
	newCiphertext, err := ring.Encrypt(ring.ActiveReference(), []byte("token-from-v2"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ring.Decrypt("v2", newCiphertext); err != nil || string(got) != "token-from-v2" {
		t.Errorf("post-rotation round trip failed: %v / %q", err, got)
	}
}

// TestPreviousKeyOverlap covers the configuration form of rotation.
func TestPreviousKeyOverlap(t *testing.T) {
	oldCiphertext, err := newKeyring(t).Encrypt("kms://storvia/prod/drive", []byte("old-token"))
	if err != nil {
		t.Fatal(err)
	}

	ring, err := NewAESGCMKeyring(KeyringOptions{
		ActiveReference:   "v2",
		ActiveKey:         hex.EncodeToString(testKey(2)),
		PreviousReference: "v1",
		PreviousKey:       hex.EncodeToString(testKey(1)),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, err := ring.Decrypt("v1", oldCiphertext); err != nil || string(got) != "old-token" {
		t.Errorf("decrypt under the previous key failed: %v / %q", err, got)
	}
}

func TestPreviousKeyWithoutReferenceIsRejected(t *testing.T) {
	_, err := NewAESGCMKeyring(KeyringOptions{
		ActiveReference: "v2",
		ActiveKey:       hex.EncodeToString(testKey(2)),
		PreviousKey:     hex.EncodeToString(testKey(1)),
	})
	if err == nil {
		t.Fatal("a previous key without a reference must be rejected")
	}
}

func TestReferencesListsConfiguredKeys(t *testing.T) {
	ring, err := NewAESGCMKeyring(KeyringOptions{
		ActiveReference:   "v2",
		ActiveKey:         hex.EncodeToString(testKey(2)),
		PreviousReference: "v1",
		PreviousKey:       hex.EncodeToString(testKey(1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	refs := ring.References()
	if len(refs) != 2 {
		t.Errorf("References = %v, want two entries", refs)
	}
	// References are names, never key material.
	for _, ref := range refs {
		if len(ref) > 64 {
			t.Errorf("reference %q looks like key material", ref)
		}
	}
}

func TestStringRefBuildsNamespacedReferences(t *testing.T) {
	if got := StringRef("kms://storvia", "prod"); got != "kms://storvia/prod" {
		t.Errorf("StringRef = %q, want kms://storvia/prod", got)
	}
	if got := StringRef("", "prod"); got != "prod" {
		t.Errorf("StringRef with an empty prefix = %q, want prod", got)
	}
}

// interface conformance.
var _ Keyring = (*AESGCMKeyring)(nil)
