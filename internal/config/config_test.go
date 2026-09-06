package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "argus.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	return path
}

func TestLoad_Valid(t *testing.T) {
	path := writeConfig(t, `
storage:
  local:
    path: /var/backups/argus
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Storage.Local.Path != "/var/backups/argus" {
		t.Errorf("Storage.Local.Path = %q, want %q", cfg.Storage.Local.Path, "/var/backups/argus")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeConfig(t, "storage: [this is not valid: yaml")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error for invalid YAML")
	}
}

func TestLoad_MissingStorageLocal(t *testing.T) {
	path := writeConfig(t, "storage: {}\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing storage.local")
	}
}

func TestLoad_MissingStoragePath(t *testing.T) {
	path := writeConfig(t, `
storage:
  local:
    path: ""
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error for empty storage.local.path")
	}
}

func TestSourceDSN_Set(t *testing.T) {
	t.Setenv(sourceDSNEnvVar, "postgres://user:pass@localhost:5432/app")

	dsn, err := SourceDSN()
	if err != nil {
		t.Fatalf("SourceDSN() error = %v, want nil", err)
	}

	if dsn != "postgres://user:pass@localhost:5432/app" {
		t.Errorf("SourceDSN() = %q, want %q", dsn, "postgres://user:pass@localhost:5432/app")
	}
}

func TestSourceDSN_NotSet(t *testing.T) {
	t.Setenv(sourceDSNEnvVar, "")

	_, err := SourceDSN()
	if err == nil {
		t.Fatal("SourceDSN() error = nil, want error when env var is unset")
	}
}
