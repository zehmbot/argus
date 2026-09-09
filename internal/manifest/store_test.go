package manifest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/storage/local"
)

// newBackend returns a real local backend rooted in a temporary directory.
// These tests deliberately exercise a real storage implementation rather
// than a fake, so they fail if the key layout and the backend disagree.
func newBackend(t *testing.T) storage.Backend {
	t.Helper()

	backend, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	return backend
}

// storeManifest builds a manifest for database created at createdAt and
// writes it, returning it for comparison.
func storeManifest(t *testing.T, backend storage.Backend, database, backupID string, createdAt time.Time) Manifest {
	t.Helper()

	m := testManifest()
	m.BackupID = backupID
	m.CreatedAt = createdAt
	m.Source.Database = database
	m.Artifact.ObjectKey = ArtifactKey(database, backupID, false)

	if err := Write(context.Background(), backend, m); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	return m
}

func TestWriteRead_RoundTrip(t *testing.T) {
	backend := newBackend(t)
	want := storeManifest(t, backend, "app_production", "2026-09-05T03-00-12Z-a3f9", time.Date(2026, 9, 5, 3, 0, 12, 0, time.UTC))

	got, err := Read(context.Background(), backend, want.Source.Database, want.BackupID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if got.BackupID != want.BackupID {
		t.Errorf("BackupID = %q, want %q", got.BackupID, want.BackupID)
	}
	if got.Artifact != want.Artifact {
		t.Errorf("Artifact = %+v, want %+v", got.Artifact, want.Artifact)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestWrite_UsesManifestKey(t *testing.T) {
	backend := newBackend(t)
	m := storeManifest(t, backend, "app_production", "2026-09-05T03-00-12Z-a3f9", time.Date(2026, 9, 5, 3, 0, 12, 0, time.UTC))

	key := ManifestKey(m.Source.Database, m.BackupID)
	if _, err := backend.Stat(context.Background(), key); err != nil {
		t.Errorf("Stat(%q) error = %v, want manifest written there", key, err)
	}
}

func TestRead_NotFound(t *testing.T) {
	backend := newBackend(t)

	_, err := Read(context.Background(), backend, "app_production", "does-not-exist")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Read() error = %v, want storage.ErrNotFound", err)
	}
}

func TestList_NewestFirst(t *testing.T) {
	backend := newBackend(t)
	const database = "app_production"

	storeManifest(t, backend, database, "oldest", time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC))
	storeManifest(t, backend, database, "newest", time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC))
	storeManifest(t, backend, database, "middle", time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC))

	got, err := List(context.Background(), backend, database)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	want := []string{"newest", "middle", "oldest"}
	if len(got) != len(want) {
		t.Fatalf("List() returned %d manifests, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].BackupID != id {
			t.Errorf("List()[%d].BackupID = %q, want %q", i, got[i].BackupID, id)
		}
	}
}

func TestList_IgnoresArtifacts(t *testing.T) {
	backend := newBackend(t)
	const (
		database = "app_production"
		backupID = "2026-09-05T03-00-12Z-a3f9"
	)

	storeManifest(t, backend, database, backupID, time.Date(2026, 9, 5, 3, 0, 12, 0, time.UTC))

	// The artifact lives under the same prefix and is not JSON; listing
	// must skip it rather than try to decode it.
	artifact := ArtifactKey(database, backupID, false)
	if err := backend.Put(context.Background(), artifact, strings.NewReader("not a manifest")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	got, err := List(context.Background(), backend, database)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(got) != 1 || got[0].BackupID != backupID {
		t.Errorf("List() = %+v, want exactly the manifest for %q", got, backupID)
	}
}

func TestList_ScopedToDatabase(t *testing.T) {
	backend := newBackend(t)
	createdAt := time.Date(2026, 9, 5, 3, 0, 12, 0, time.UTC)

	storeManifest(t, backend, "app_production", "prod-backup", createdAt)
	storeManifest(t, backend, "app_staging", "staging-backup", createdAt)

	got, err := List(context.Background(), backend, "app_production")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(got) != 1 || got[0].BackupID != "prod-backup" {
		t.Errorf("List() = %+v, want only app_production's backup", got)
	}
}

func TestList_Empty(t *testing.T) {
	backend := newBackend(t)

	got, err := List(context.Background(), backend, "app_production")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(got) != 0 {
		t.Errorf("List() = %+v, want no manifests", got)
	}
}

func TestList_FailsOnCorruptManifest(t *testing.T) {
	backend := newBackend(t)
	const database = "app_production"

	storeManifest(t, backend, database, "good", time.Date(2026, 9, 5, 3, 0, 12, 0, time.UTC))

	corrupt := ManifestKey(database, "corrupt")
	if err := backend.Put(context.Background(), corrupt, strings.NewReader("{")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	_, err := List(context.Background(), backend, database)
	if err == nil {
		t.Fatal("List() error = nil, want error naming the corrupt manifest")
	}
	if !strings.Contains(err.Error(), corrupt) {
		t.Errorf("List() error = %v, want it to name %q", err, corrupt)
	}
}
