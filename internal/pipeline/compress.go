package pipeline

import (
	"compress/gzip"
	"fmt"
	"io"
)

// Compress gzips src into dst. It does not close dst, which belongs to the
// caller.
//
// The artifact's checksum is deliberately not computed here. It has to
// describe the bytes that reach storage, and those may have passed through
// encryption after this stage; it is taken where the bytes meet storage
// instead.
func Compress(dst io.Writer, src io.Reader) error {
	gz := gzip.NewWriter(dst)

	if _, err := io.Copy(gz, src); err != nil {
		return fmt.Errorf("compressing: %w", err)
	}

	// Closing flushes the gzip trailer. Without it the stream is truncated
	// and will not decompress.
	if err := gz.Close(); err != nil {
		return fmt.Errorf("closing gzip writer: %w", err)
	}

	return nil
}
