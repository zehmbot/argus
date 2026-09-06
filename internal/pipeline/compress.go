package pipeline

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

func Compress(dst io.Writer, src io.Reader) (sha256Hex string, err error) {
	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(dst, hash))

	if _, err := io.Copy(gz, src); err != nil {
		return "", fmt.Errorf("compressing: %w", err)
	}

	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("closing gzip writer: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
