// Package local implements storage.Backend on the local filesystem.
package local

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/zehmbot/argus/internal/storage"
)

// tempDirName holds in-progress writes so they never appear in List results
// and a crash mid-write never leaves a partial file at a real key.
const tempDirName = ".tmp"

// Backend stores objects as files under root.
type Backend struct {
	root string
}

var _ storage.Backend = (*Backend)(nil)

// New returns a Backend rooted at root, creating it if it does not exist.
func New(root string) (*Backend, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("creating storage root %s: %w", root, err)
	}

	return &Backend{root: root}, nil
}

func (b *Backend) Put(ctx context.Context, key string, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dest := b.resolve(key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("creating directory for %s: %w", key, err)
	}

	tmpDir := filepath.Join(b.root, tempDirName)
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}

	tmp, err := os.CreateTemp(tmpDir, "upload-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", key, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", key, err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file for %s: %w", key, err)
	}

	if err := os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("committing %s: %w", key, err)
	}

	return nil
}

func (b *Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := os.Open(b.resolve(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("opening %s: %w", key, err)
	}

	return f, nil
}

func (b *Backend) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var keys []string
	err := filepath.WalkDir(b.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == tempDirName {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(b.root, p)
		if err != nil {
			return err
		}

		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}

		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing %s: %w", prefix, err)
	}

	return keys, nil
}

func (b *Backend) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := os.Remove(b.resolve(key)); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("deleting %s: %w", key, err)
	}

	return nil
}

func (b *Backend) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}

	info, err := os.Stat(b.resolve(key))
	if err != nil {
		if os.IsNotExist(err) {
			return storage.ObjectInfo{}, storage.ErrNotFound
		}
		return storage.ObjectInfo{}, fmt.Errorf("statting %s: %w", key, err)
	}

	return storage.ObjectInfo{
		Key:     key,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

// resolve maps an object key to a filesystem path under root, neutralizing
// any ".." segments so a key can never resolve outside root.
func (b *Backend) resolve(key string) string {
	clean := path.Clean("/" + key)
	return filepath.Join(b.root, filepath.FromSlash(clean))
}
