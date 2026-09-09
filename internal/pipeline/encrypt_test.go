package pipeline

import (
	"bytes"
	"io"
	"testing"

	"filippo.io/age"
)

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()

	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	return id
}

// encryptTo runs plaintext through Encrypt and returns the ciphertext.
func encryptTo(t *testing.T, recipient age.Recipient, plaintext []byte) []byte {
	t.Helper()

	var out bytes.Buffer

	w, err := Encrypt(&out, recipient)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("writing plaintext: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing encryption writer: %v", err)
	}

	return out.Bytes()
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	id := newIdentity(t)
	plaintext := []byte("the quick brown fox jumps over the lazy dog")

	ciphertext := encryptTo(t, id.Recipient(), plaintext)

	if bytes.Contains(ciphertext, plaintext) {
		t.Error("ciphertext contains the plaintext")
	}

	r, err := Decrypt(bytes.NewReader(ciphertext), id)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading decrypted data: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypted = %q, want %q", got, plaintext)
	}
}

func TestEncrypt_Empty(t *testing.T) {
	id := newIdentity(t)

	ciphertext := encryptTo(t, id.Recipient(), nil)

	r, err := Decrypt(bytes.NewReader(ciphertext), id)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading decrypted data: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("decrypted = %q, want empty", got)
	}
}

// The whole point of encrypting to a key held elsewhere: whoever holds the
// server's key cannot read the backups.
func TestDecrypt_WrongIdentity(t *testing.T) {
	ciphertext := encryptTo(t, newIdentity(t).Recipient(), []byte("secret"))

	if _, err := Decrypt(bytes.NewReader(ciphertext), newIdentity(t)); err == nil {
		t.Error("Decrypt() error = nil, want failure with the wrong identity")
	}
}

// age authenticates its payload, so a single flipped byte must be detected
// rather than yielding corrupt plaintext.
func TestDecrypt_TamperedCiphertext(t *testing.T) {
	id := newIdentity(t)
	ciphertext := encryptTo(t, id.Recipient(), bytes.Repeat([]byte("backup data "), 64))

	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 0x01

	r, err := Decrypt(bytes.NewReader(tampered), id)
	if err != nil {
		return // Detected in the header; also acceptable.
	}

	if _, err := io.ReadAll(r); err == nil {
		t.Error("reading tampered ciphertext succeeded, want an authentication failure")
	}
}
