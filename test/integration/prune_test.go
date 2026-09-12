//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/storage/local"
)

// writeRetentionConfig writes a config with an explicit retention policy.
// Keeping one daily and nothing else lets a test make several backups in the
// same run and still have most of them fall outside the policy.
func writeRetentionConfig(t *testing.T, daily, weekly, monthly int) (configPath, storageRoot string) {
	t.Helper()

	dir := t.TempDir()
	storageRoot = filepath.Join(dir, "storage")
	configPath = filepath.Join(dir, "argus.yaml")

	body := fmt.Sprintf(
		"storage:\n  local:\n    path: %s\nretention:\n  daily: %d\n  weekly: %d\n  monthly: %d\n",
		filepath.ToSlash(storageRoot), daily, weekly, monthly,
	)

	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	return configPath, storageRoot
}

// backupsFor makes count backups and returns them newest first.
func backupsFor(t *testing.T, dsn, configPath string, count int) ([]manifest.Manifest, storage.Backend) {
	t.Helper()

	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	for range count {
		if out, code := runArgus(t, env, "backup", "--config", configPath); code != 0 {
			t.Fatalf("argus backup exited %d:\n%s", code, out)
		}
	}

	storageRoot := storageRootFrom(t, configPath)

	backend, err := local.New(storageRoot)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	manifests, err := manifest.List(context.Background(), backend, "app_production")
	if err != nil {
		t.Fatalf("manifest.List() error = %v", err)
	}
	if len(manifests) != count {
		t.Fatalf("made %d backups but found %d manifests", count, len(manifests))
	}

	return manifests, backend
}

// storageRootFrom reads the storage path back out of a config file, so the
// helpers do not have to thread it around.
func storageRootFrom(t *testing.T, configPath string) string {
	t.Helper()

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}

	for _, line := range strings.Split(string(body), "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "path:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "path:"))
		}
	}

	t.Fatalf("no storage path in %s", configPath)

	return ""
}

func exists(t *testing.T, backend storage.Backend, key string) bool {
	t.Helper()

	_, err := backend.Stat(context.Background(), key)
	if err == nil {
		return true
	}
	if errors.Is(err, storage.ErrNotFound) {
		return false
	}

	t.Fatalf("Stat(%q) error = %v", key, err)

	return false
}

// Dry run is the default, and a tool that deletes backups has to earn the
// right to do so explicitly.
func TestPrune_DryRunByDefault(t *testing.T) {
	dsn := startPostgres(t)
	configPath, _ := writeRetentionConfig(t, 1, 0, 0)
	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	manifests, backend := backupsFor(t, dsn, configPath, 3)

	if out, code := runArgus(t, env, "verify", "--latest", "--config", configPath); code != 0 {
		t.Fatalf("argus verify exited %d:\n%s", code, out)
	}

	out, code := runArgus(t, env, "prune", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus prune exited %d:\n%s", code, out)
	}

	if !strings.Contains(out, "would delete 2") {
		t.Errorf("prune did not report what it would delete:\n%s", out)
	}
	if !strings.Contains(out, "--apply") {
		t.Errorf("prune did not say how to carry the plan out:\n%s", out)
	}

	// Nothing may actually have gone.
	for _, m := range manifests {
		if !exists(t, backend, manifest.ManifestKey("app_production", m.BackupID)) {
			t.Errorf("dry run deleted the manifest for %s", m.BackupID)
		}
		if !exists(t, backend, m.Artifact.ObjectKey) {
			t.Errorf("dry run deleted the artifact for %s", m.BackupID)
		}
	}
}

func TestPrune_Apply(t *testing.T) {
	dsn := startPostgres(t)
	configPath, _ := writeRetentionConfig(t, 1, 0, 0)
	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	manifests, backend := backupsFor(t, dsn, configPath, 3)

	if out, code := runArgus(t, env, "verify", "--latest", "--config", configPath); code != 0 {
		t.Fatalf("argus verify exited %d:\n%s", code, out)
	}

	// List returns newest first, and one daily keeps exactly that one.
	kept, doomed := manifests[0], manifests[1:]

	out, code := runArgus(t, env, "prune", "--apply", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus prune --apply exited %d:\n%s", code, out)
	}

	if !exists(t, backend, manifest.ManifestKey("app_production", kept.BackupID)) {
		t.Errorf("prune deleted the manifest it said it would keep, %s", kept.BackupID)
	}
	if !exists(t, backend, kept.Artifact.ObjectKey) {
		t.Errorf("prune deleted the artifact it said it would keep, %s", kept.BackupID)
	}

	// Both halves of a deleted backup have to go: an artifact left behind
	// costs storage forever, since nothing lists it any more.
	for _, m := range doomed {
		if exists(t, backend, manifest.ManifestKey("app_production", m.BackupID)) {
			t.Errorf("manifest for %s survived prune", m.BackupID)
		}
		if exists(t, backend, m.Artifact.ObjectKey) {
			t.Errorf("artifact for %s survived prune", m.BackupID)
		}
	}

	out, code = runArgus(t, env, "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("argus list exited %d:\n%s", code, out)
	}

	if !strings.Contains(out, kept.BackupID) {
		t.Errorf("list no longer shows the surviving backup %s:\n%s", kept.BackupID, out)
	}
	for _, m := range doomed {
		if strings.Contains(out, m.BackupID) {
			t.Errorf("list still shows the pruned backup %s:\n%s", m.BackupID, out)
		}
	}
}

// The rule the whole design turns on: paying for storage is cheaper than
// losing the last restorable backup.
func TestPrune_RefusesWithoutVerifiedBackup(t *testing.T) {
	dsn := startPostgres(t)
	configPath, _ := writeRetentionConfig(t, 1, 0, 0)
	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	manifests, backend := backupsFor(t, dsn, configPath, 3)

	// Deliberately no verify: every backup is still pending.
	out, code := runArgus(t, env, "prune", "--apply", "--config", configPath)
	if code != 3 {
		t.Fatalf("argus prune --apply exited %d, want 3:\n%s", code, out)
	}
	if !strings.Contains(out, "verified") {
		t.Errorf("refusal does not explain itself:\n%s", out)
	}

	// Refusing means deleting nothing at all, not deleting a little less.
	for _, m := range manifests {
		if !exists(t, backend, manifest.ManifestKey("app_production", m.BackupID)) {
			t.Errorf("manifest for %s was deleted despite the refusal", m.BackupID)
		}
		if !exists(t, backend, m.Artifact.ObjectKey) {
			t.Errorf("artifact for %s was deleted despite the refusal", m.BackupID)
		}
	}
}

func TestPrune_ContradictoryFlags(t *testing.T) {
	dsn := startPostgres(t)
	configPath, _ := writeRetentionConfig(t, 1, 0, 0)

	out, code := runArgus(t,
		[]string{"ARGUS_DATABASE_URL=" + dsn},
		"prune", "--apply", "--dry-run", "--config", configPath,
	)
	if code != 1 {
		t.Fatalf("argus prune --apply --dry-run exited %d, want 1:\n%s", code, out)
	}
}
