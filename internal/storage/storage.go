// Package storage defines the Backend abstraction used by every command
// that reads or writes backup artifacts and manifests.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Get, Delete, and Stat when key does not exist.
var ErrNotFound = errors.New("object not found")

// ObjectInfo describes a stored object without its contents.
type ObjectInfo struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// Backend is the only storage abstraction in this project. It has two real
// implementations, local and s3, which is what justifies it existing.
type Backend interface {
	// Put writes the contents of r to key, replacing it if it already exists.
	Put(ctx context.Context, key string, r io.Reader) error

	// Get returns a reader for key. The caller must close it.
	// It returns ErrNotFound if key does not exist.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// List returns the keys that start with prefix.
	List(ctx context.Context, prefix string) ([]string, error)

	// Delete removes key. It returns ErrNotFound if key does not exist.
	Delete(ctx context.Context, key string) error

	// Stat returns metadata for key without reading its contents.
	// It returns ErrNotFound if key does not exist.
	Stat(ctx context.Context, key string) (ObjectInfo, error)
}
