package pipeline

import (
	"compress/gzip"
	"fmt"
	"io"
)

// Decompress returns a reader over the gunzipped contents of src. The caller
// must close it.
//
// There is no matching Compress: compression happens inside Stream, which
// needs a writer to hand to pg_dump rather than a reader to copy from.
//
// It exists for the same reason Decrypt sits beside Encrypt: a path that
// writes backups without the path that reads them back is how a project ends
// up with a bucket full of artifacts nobody can open.
func Decompress(src io.Reader) (io.ReadCloser, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("decompressing: %w", err)
	}

	return gz, nil
}
