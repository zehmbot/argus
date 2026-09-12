//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/zehmbot/argus/internal/manifest"
	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/verify"
)

// reloadManifest reads a manifest back out of storage, which is where verify
// records its outcome.
func reloadManifest(t *testing.T, backend storage.Backend, backupID string) manifest.Manifest {
	t.Helper()

	m, err := manifest.Read(context.Background(), backend, "app_production", backupID)
	if err != nil {
		t.Fatalf("manifest.Read() error = %v", err)
	}

	return m
}

func checkByName(t *testing.T, m manifest.Manifest, name string) manifest.Check {
	t.Helper()

	for _, check := range m.Verification.Checks {
		if check.Name == name {
			return check
		}
	}

	t.Fatalf("no check named %q in %+v", name, m.Verification.Checks)

	return manifest.Check{}
}

func TestVerify(t *testing.T) {
	dsn := startPostgres(t)
	m, backend, configPath := backupOnce(t, dsn, "")

	out, code := runArgus(t, []string{"ARGUS_DATABASE_URL=" + dsn}, "verify", m.BackupID, "--config", configPath)
	if code != 0 {
		t.Fatalf("argus verify exited %d:\n%s", code, out)
	}

	// The outcome has to be recorded, not just printed: prune's safety rule
	// reads this field, and nothing else remembers the result.
	verified := reloadManifest(t, backend, m.BackupID)

	if verified.Verification.Status != manifest.StatusVerified {
		t.Errorf("Status = %q, want %q", verified.Verification.Status, manifest.StatusVerified)
	}
	if verified.Verification.VerifiedAt == nil {
		t.Error("VerifiedAt was not recorded")
	}
	if !strings.HasPrefix(verified.Verification.RestoredPGVersion, "17.") {
		t.Errorf("RestoredPGVersion = %q, want a 17.x release", verified.Verification.RestoredPGVersion)
	}

	for _, name := range []string{verify.CheckRestoreExitCode, verify.CheckSchemaFingerprint, verify.CheckRowCounts} {
		if check := checkByName(t, verified, name); !check.Passed {
			t.Errorf("check %q failed: %s", name, check.Detail)
		}
	}

	// No smoke query is configured, so none should be recorded.
	for _, check := range verified.Verification.Checks {
		if check.Name == verify.CheckSmokeQuery {
			t.Error("a smoke query check was recorded without one being configured")
		}
	}
}

func TestVerify_Latest(t *testing.T) {
	dsn := startPostgres(t)
	first, backend, configPath := backupOnce(t, dsn, "")

	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	out, code := runArgus(t, env, "backup", "--config", configPath)
	if code != 0 {
		t.Fatalf("second backup exited %d:\n%s", code, out)
	}

	manifests, err := manifest.List(context.Background(), backend, "app_production")
	if err != nil {
		t.Fatalf("manifest.List() error = %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("found %d manifests, want 2", len(manifests))
	}

	newest := manifests[0]
	if newest.BackupID == first.BackupID {
		t.Fatal("List did not return the newest backup first")
	}

	if out, code := runArgus(t, env, "verify", "--latest", "--config", configPath); code != 0 {
		t.Fatalf("argus verify --latest exited %d:\n%s", code, out)
	}

	if got := reloadManifest(t, backend, newest.BackupID); got.Verification.Status != manifest.StatusVerified {
		t.Errorf("newest backup status = %q, want %q", got.Verification.Status, manifest.StatusVerified)
	}

	// The older one must be left alone.
	if got := reloadManifest(t, backend, first.BackupID); got.Verification.Status != manifest.StatusPending {
		t.Errorf("older backup status = %q, want it untouched at %q", got.Verification.Status, manifest.StatusPending)
	}
}

func TestVerify_Encrypted(t *testing.T) {
	dsn := startPostgres(t)

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	m, backend, configPath := backupOnce(t, dsn, identity.Recipient().String())

	out, code := runArgus(t,
		[]string{"ARGUS_DATABASE_URL=" + dsn, "ARGUS_AGE_IDENTITY=" + identity.String()},
		"verify", m.BackupID, "--config", configPath,
	)
	if code != 0 {
		t.Fatalf("argus verify exited %d:\n%s", code, out)
	}

	if got := reloadManifest(t, backend, m.BackupID); got.Verification.Status != manifest.StatusVerified {
		t.Errorf("Status = %q, want %q", got.Verification.Status, manifest.StatusVerified)
	}
}

// The test that proves verify compares rather than rubber-stamps. The
// manifest is rewritten to claim a row count the database cannot have, so a
// verification that passes is not looking at the data at all.
func TestVerify_DetectsRowCountMismatch(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	m, backend, configPath := backupOnce(t, dsn, "")

	m.TableCounts["users"] = 999999
	if err := manifest.Write(ctx, backend, m); err != nil {
		t.Fatalf("manifest.Write() error = %v", err)
	}

	out, code := runArgus(t, []string{"ARGUS_DATABASE_URL=" + dsn}, "verify", m.BackupID, "--config", configPath)
	if code != 3 {
		t.Fatalf("argus verify exited %d, want 3 (verification failed):\n%s", code, out)
	}

	failed := reloadManifest(t, backend, m.BackupID)

	if failed.Verification.Status != manifest.StatusFailed {
		t.Errorf("Status = %q, want %q", failed.Verification.Status, manifest.StatusFailed)
	}

	// A failure has to say which table, or an operator is no better off.
	check := checkByName(t, failed, verify.CheckRowCounts)
	if check.Passed {
		t.Fatal("row count check passed against a manifest claiming 999999 users")
	}
	if !strings.Contains(check.Detail, "users") {
		t.Errorf("Detail = %q, want it to name the table", check.Detail)
	}

	// The restore itself still worked, so that check must still pass.
	if restore := checkByName(t, failed, verify.CheckRestoreExitCode); !restore.Passed {
		t.Errorf("restore check failed unexpectedly: %s", restore.Detail)
	}
}
