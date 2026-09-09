package config

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
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

// testRecipient generates an age public key. Generating beats a hardcoded
// constant: there is no key of unknown provenance in the repository, and the
// test cannot silently depend on one particular key's properties.
func testRecipient(t *testing.T) string {
	t.Helper()

	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	return id.Recipient().String()
}

func TestLoad_Encryption(t *testing.T) {
	recipient := testRecipient(t)
	path := writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\nencryption:\n  recipient: "+recipient+"\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Encryption == nil || cfg.Encryption.Recipient != recipient {
		t.Fatalf("Encryption = %+v, want recipient %q", cfg.Encryption, recipient)
	}

	got, err := cfg.Recipient()
	if err != nil {
		t.Fatalf("Recipient() error = %v", err)
	}
	if got == nil {
		t.Error("Recipient() = nil, want a parsed recipient")
	}
}

// Absent encryption means no encryption, not an error: it is optional.
func TestLoad_EncryptionAbsent(t *testing.T) {
	path := writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	got, err := cfg.Recipient()
	if err != nil {
		t.Fatalf("Recipient() error = %v", err)
	}
	if got != nil {
		t.Errorf("Recipient() = %v, want nil when encryption is not configured", got)
	}
}

// A mistyped key must fail at load, not after a long dump has already run.
func TestLoad_EncryptionInvalid(t *testing.T) {
	cases := []struct {
		name      string
		recipient string
	}{
		{name: "empty recipient", recipient: `""`},
		{name: "not an age key", recipient: "not-a-key"},
		{name: "an identity rather than a recipient", recipient: "AGE-SECRET-KEY-1QQQQQQQ"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\nencryption:\n  recipient: "+tc.recipient+"\n")

			if _, err := Load(path); err == nil {
				t.Error("Load() error = nil, want error")
			}
		})
	}
}
