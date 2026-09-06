package pipeline

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
)

func TestCompress(t *testing.T) {
	input := []byte("the quick brown fox jumps over the lazy dog")

	var out bytes.Buffer
	gotHash, err := Compress(&out, bytes.NewReader(input))
	if err != nil {
		t.Fatalf("Compress() error = %v", err)
	}

	wantHash := sha256.Sum256(out.Bytes())
	if gotHash != hex.EncodeToString(wantHash[:]) {
		t.Errorf("Compress() hash = %s, want sha256 of the written bytes = %s", gotHash, hex.EncodeToString(wantHash[:]))
	}

	gz, err := gzip.NewReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gz.Close()

	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("reading decompressed data: %v", err)
	}

	if !bytes.Equal(got, input) {
		t.Errorf("decompressed = %q, want %q", got, input)
	}
}

func TestCompress_Empty(t *testing.T) {
	var out bytes.Buffer
	if _, err := Compress(&out, bytes.NewReader(nil)); err != nil {
		t.Fatalf("Compress() error = %v", err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gz.Close()

	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("reading decompressed data: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("decompressed = %q, want empty", got)
	}
}
