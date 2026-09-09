package pipeline

import (
	"fmt"
	"io"

	"filippo.io/age"
)

// Encrypt wraps dst so that everything written to the returned writer is
// encrypted to recipient. The caller must close it: age writes its
// authentication tag on close, and a stream missing that tag will not
// decrypt.
//
// This returns a writer rather than copying, unlike Compress, because
// encryption sits between compression and storage. Wrapping the destination
// lets the whole pipeline run in one pass over the data instead of staging
// the compressed bytes on disk only to read them straight back.
func Encrypt(dst io.Writer, recipient age.Recipient) (io.WriteCloser, error) {
	w, err := age.Encrypt(dst, recipient)
	if err != nil {
		return nil, fmt.Errorf("starting encryption: %w", err)
	}

	return w, nil
}

// Decrypt returns a reader over the decrypted contents of src.
//
// It lives here beside Encrypt on purpose: an encryption path written
// without its matching decryption path is how a project ends up with
// backups nobody can read.
func Decrypt(src io.Reader, identity age.Identity) (io.Reader, error) {
	r, err := age.Decrypt(src, identity)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}

	return r, nil
}
