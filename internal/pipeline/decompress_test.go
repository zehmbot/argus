package pipeline

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestDecompress(t *testing.T) {
	input := bytes.Repeat([]byte("argus backup payload "), 512)

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(input); err != nil {
		t.Fatalf("writing gzip: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}

	r, err := Decompress(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatalf("Decompress() error = %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading decompressed data: %v", err)
	}

	if !bytes.Equal(got, input) {
		t.Error("decompressed data does not match the input")
	}
}

func TestDecompress_NotGzip(t *testing.T) {
	if _, err := Decompress(bytes.NewReader([]byte("this is not gzip"))); err == nil {
		t.Error("Decompress() error = nil, want error")
	}
}

// A truncated stream must fail on read rather than return short data, since
// a truncated artifact is exactly what a killed dump would leave behind.
func TestDecompress_Truncated(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(bytes.Repeat([]byte("payload "), 4096)); err != nil {
		t.Fatalf("writing gzip: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}

	truncated := compressed.Bytes()[:compressed.Len()/2]

	r, err := Decompress(bytes.NewReader(truncated))
	if err != nil {
		return // Detected in the header; also acceptable.
	}
	defer r.Close()

	if _, err := io.ReadAll(r); err == nil {
		t.Error("reading a truncated stream succeeded, want an error")
	}
}
