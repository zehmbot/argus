package pipeline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"filippo.io/age"
)

// writeAll returns a producer that writes b and succeeds.
func writeAll(b []byte) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := w.Write(b)

		return err
	}
}

// collect returns a consumer that reads everything into buf.
func collect(buf *bytes.Buffer) func(io.Reader) error {
	return func(r io.Reader) error {
		_, err := io.Copy(buf, r)

		return err
	}
}

func TestStream_Plain(t *testing.T) {
	payload := bytes.Repeat([]byte("argus backup payload "), 1024)

	var got bytes.Buffer
	result, err := Stream(nil, writeAll(payload), collect(&got))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// The checksum and size must describe what the consumer received, so a
	// stored object can be verified without being decompressed.
	if result.Size != int64(got.Len()) {
		t.Errorf("Size = %d, consumer received %d bytes", result.Size, got.Len())
	}

	want := sha256.Sum256(got.Bytes())
	if result.SHA256 != hex.EncodeToString(want[:]) {
		t.Errorf("SHA256 = %s, want the hash of the consumed bytes %s", result.SHA256, hex.EncodeToString(want[:]))
	}

	r, err := Decompress(bytes.NewReader(got.Bytes()))
	if err != nil {
		t.Fatalf("Decompress() error = %v", err)
	}
	defer r.Close()

	back, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading decompressed data: %v", err)
	}
	if !bytes.Equal(back, payload) {
		t.Error("round trip changed the payload")
	}
}

func TestStream_Encrypted(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	payload := bytes.Repeat([]byte("argus backup payload "), 1024)

	var got bytes.Buffer
	result, err := Stream(identity.Recipient(), writeAll(payload), collect(&got))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if !bytes.HasPrefix(got.Bytes(), []byte("age-encryption.org/v1")) {
		t.Fatal("consumer did not receive an age stream")
	}
	if result.Size != int64(got.Len()) {
		t.Errorf("Size = %d, consumer received %d bytes", result.Size, got.Len())
	}

	decrypted, err := Decrypt(bytes.NewReader(got.Bytes()), identity)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	r, err := Decompress(decrypted)
	if err != nil {
		t.Fatalf("Decompress() error = %v", err)
	}
	defer r.Close()

	back, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading decompressed data: %v", err)
	}
	if !bytes.Equal(back, payload) {
		t.Error("round trip changed the payload")
	}
}

var errProducer = errors.New("producer failed")

// The property the whole design rests on: a producer that dies part way
// through must not leave the consumer thinking it received a whole artifact.
func TestStream_ProducerFailsMidStream(t *testing.T) {
	produce := func(w io.Writer) error {
		if _, err := w.Write(bytes.Repeat([]byte("partial "), 4096)); err != nil {
			return err
		}

		return errProducer
	}

	var consumerErr error
	consume := func(r io.Reader) error {
		_, consumerErr = io.Copy(io.Discard, r)

		// Report success, so the test proves Stream reports the failure even
		// when the consumer is happy to accept a truncated stream.
		return nil
	}

	_, err := Stream(nil, produce, consume)
	if !errors.Is(err, errProducer) {
		t.Fatalf("Stream() error = %v, want %v", err, errProducer)
	}

	if consumerErr == nil {
		t.Error("consumer saw a clean end of stream, want a read error")
	}
	if !errors.Is(consumerErr, errProducer) {
		t.Errorf("consumer error = %v, want it to carry %v", consumerErr, errProducer)
	}
}

var errConsumer = errors.New("consumer failed")

// A consumer that gives up must not leave the producer blocked on a pipe
// nobody is reading.
func TestStream_ConsumerFails(t *testing.T) {
	produce := func(w io.Writer) error {
		for range 1024 {
			if _, err := w.Write(bytes.Repeat([]byte("x"), 4096)); err != nil {
				return err
			}
		}

		return nil
	}

	consume := func(r io.Reader) error {
		if _, err := io.CopyN(io.Discard, r, 16); err != nil {
			return err
		}

		return errConsumer
	}

	if _, err := Stream(nil, produce, consume); !errors.Is(err, errConsumer) {
		t.Fatalf("Stream() error = %v, want %v", err, errConsumer)
	}
}

// An empty database still produces a valid, checksummable artifact.
func TestStream_Empty(t *testing.T) {
	var got bytes.Buffer

	result, err := Stream(nil, writeAll(nil), collect(&got))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if result.Size == 0 || result.Size != int64(got.Len()) {
		t.Errorf("Size = %d, consumer received %d bytes", result.Size, got.Len())
	}
	if result.SHA256 == "" {
		t.Error("SHA256 is empty")
	}
}
