package manifest

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/zehmbot/argus/internal/storage"
)

// Write stores m at the key derived from its own database and backup ID.
// It is the commit point of a backup: an artifact whose manifest was never
// written is invisible to list and prune.
func Write(ctx context.Context, backend storage.Backend, m Manifest) error {
	key := ManifestKey(m.Source.Database, m.BackupID)

	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		return fmt.Errorf("writing manifest %s: %w", key, err)
	}

	if err := backend.Put(ctx, key, &buf); err != nil {
		return fmt.Errorf("writing manifest %s: %w", key, err)
	}

	return nil
}

// Read loads the manifest of a single backup.
func Read(ctx context.Context, backend storage.Backend, database, backupID string) (Manifest, error) {
	return readKey(ctx, backend, ManifestKey(database, backupID))
}

// List returns every manifest stored for database, newest first.
//
// A manifest that fails to decode fails the whole call rather than being
// skipped: "there is an object in your bucket that is not what argus
// wrote" is a fact the operator needs to be told, not one to paper over.
func List(ctx context.Context, backend storage.Backend, database string) ([]Manifest, error) {
	prefix := path.Join(backupsPrefix, database) + "/"

	keys, err := backend.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("listing manifests for %s: %w", database, err)
	}

	var manifests []Manifest
	for _, key := range keys {
		// Artifacts sit under the same prefix as their manifests.
		if !strings.HasSuffix(key, manifestExtension) {
			continue
		}

		m, err := readKey(ctx, backend, key)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, m)
	}

	// Newest first, so callers can take the latest backup off the front.
	// Ties break on ID to keep the order stable across calls.
	sort.Slice(manifests, func(i, j int) bool {
		if !manifests[i].CreatedAt.Equal(manifests[j].CreatedAt) {
			return manifests[i].CreatedAt.After(manifests[j].CreatedAt)
		}
		return manifests[i].BackupID < manifests[j].BackupID
	})

	return manifests, nil
}

func readKey(ctx context.Context, backend storage.Backend, key string) (Manifest, error) {
	r, err := backend.Get(ctx, key)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", key, err)
	}
	defer r.Close()

	m, err := Decode(r)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading manifest %s: %w", key, err)
	}

	return m, nil
}
