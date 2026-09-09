package pipeline

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestCompress(t *testing.T) {
	input := []byte("the quick brown fox jumps over the lazy dog")

	var out bytes.Buffer
	if err := Compress(&out, bytes.NewReader(input)); err != nil {
		t.Fatalf("Compress() error = %v", err)
	}

	if got := gunzip(t, out.Bytes()); !bytes.Equal(got, input) {
		t.Errorf("decompressed = %q, want %q", got, input)
	}
}

func TestCompress_Empty(t *testing.T) {
	var out bytes.Buffer
	if err := Compress(&out, bytes.NewReader(nil)); err != nil {
		t.Fatalf("Compress() error = %v", err)
	}

	if got := gunzip(t, out.Bytes()); len(got) != 0 {
		t.Errorf("decompressed = %q, want empty", got)
	}
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gz.Close()

	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("reading decompressed data: %v", err)
	}

	return got
}
