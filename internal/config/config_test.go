package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/zehmbot/argus/internal/retention"
	"github.com/zehmbot/argus/internal/verify"
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

func TestLoad_S3(t *testing.T) {
	path := writeConfig(t, "storage:\n  s3:\n    endpoint: s3.eu-central-1.amazonaws.com\n    bucket: argus-backups\n    region: eu-central-1\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Storage.S3 == nil {
		t.Fatal("Storage.S3 = nil, want the parsed block")
	}
	if cfg.Storage.S3.Bucket != "argus-backups" {
		t.Errorf("Bucket = %q, want %q", cfg.Storage.S3.Bucket, "argus-backups")
	}
	if cfg.Storage.S3.Endpoint != "s3.eu-central-1.amazonaws.com" {
		t.Errorf("Endpoint = %q, want %q", cfg.Storage.S3.Endpoint, "s3.eu-central-1.amazonaws.com")
	}

	// Omitting the flag must mean TLS. A backup tool that silently talks
	// plaintext to a remote bucket because a field was left out is a bug.
	if cfg.Storage.S3.Insecure {
		t.Error("Insecure = true by default, want TLS unless explicitly disabled")
	}
}

func TestLoad_StorageBackendSelection(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "neither backend",
			body: "storage: {}\n",
		},
		{
			name: "both backends",
			body: "storage:\n  local:\n    path: /var/lib/argus\n  s3:\n    endpoint: e\n    bucket: b\n",
		},
		{
			name: "s3 without endpoint",
			body: "storage:\n  s3:\n    bucket: argus-backups\n",
		},
		{
			name: "s3 without bucket",
			body: "storage:\n  s3:\n    endpoint: s3.amazonaws.com\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.body)); err == nil {
				t.Error("Load() error = nil, want error")
			}
		})
	}
}

func TestS3Credentials(t *testing.T) {
	t.Setenv("ARGUS_S3_ACCESS_KEY_ID", "AKIAEXAMPLE")
	t.Setenv("ARGUS_S3_SECRET_ACCESS_KEY", "s3cret")

	id, secret, err := S3Credentials()
	if err != nil {
		t.Fatalf("S3Credentials() error = %v", err)
	}
	if id != "AKIAEXAMPLE" || secret != "s3cret" {
		t.Errorf("S3Credentials() = %q, %q, want %q, %q", id, secret, "AKIAEXAMPLE", "s3cret")
	}
}

func TestS3Credentials_Missing(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		secret string
	}{
		{name: "neither set"},
		{name: "only id set", id: "AKIAEXAMPLE"},
		{name: "only secret set", secret: "s3cret"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARGUS_S3_ACCESS_KEY_ID", tc.id)
			t.Setenv("ARGUS_S3_SECRET_ACCESS_KEY", tc.secret)

			if _, _, err := S3Credentials(); err == nil {
				t.Error("S3Credentials() error = nil, want error")
			}
		})
	}
}

func TestAgeIdentity(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	// Trailing whitespace is easy to pick up when a key is pasted into an
	// environment file, and is not a reason to fail a restore.
	t.Setenv("ARGUS_AGE_IDENTITY", id.String()+"\n")

	got, err := AgeIdentity()
	if err != nil {
		t.Fatalf("AgeIdentity() error = %v", err)
	}
	if got == nil {
		t.Fatal("AgeIdentity() = nil, want an identity")
	}
}

func TestAgeIdentity_Invalid(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "unset", value: ""},
		{name: "not a key", value: "hunter2"},
		{name: "a recipient rather than an identity", value: testRecipient(t)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARGUS_AGE_IDENTITY", tc.value)

			if _, err := AgeIdentity(); err == nil {
				t.Error("AgeIdentity() error = nil, want error")
			}
		})
	}
}

// A parse failure must not echo the key material it was handed.
func TestAgeIdentity_ErrorOmitsKey(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity() error = %v", err)
	}

	// A real key with one character removed: malformed, but still secret.
	broken := id.String()[:len(id.String())-1]
	t.Setenv("ARGUS_AGE_IDENTITY", broken)

	_, err = AgeIdentity()
	if err == nil {
		t.Fatal("AgeIdentity() error = nil, want error")
	}
	if strings.Contains(err.Error(), broken) || strings.Contains(err.Error(), "AGE-SECRET-KEY") {
		t.Errorf("AgeIdentity() error leaks key material: %v", err)
	}
}

func TestRowCountTolerance_Default(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "no verification block", body: "storage:\n  local:\n    path: /var/lib/argus\n"},
		{name: "block without a tolerance", body: "storage:\n  local:\n    path: /var/lib/argus\nverification:\n  smoke_query: SELECT 1\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.body))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if got := cfg.RowCountTolerance(); got != verify.DefaultRowCountTolerance {
				t.Errorf("RowCountTolerance() = %v, want the default %v", got, verify.DefaultRowCountTolerance)
			}
		})
	}
}

// An explicit zero means exact counts, and must not be mistaken for the
// field being absent.
func TestRowCountTolerance_ExplicitZero(t *testing.T) {
	cfg, err := Load(writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\nverification:\n  row_count_tolerance: 0\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.RowCountTolerance(); got != 0 {
		t.Errorf("RowCountTolerance() = %v, want 0", got)
	}
}

func TestRowCountTolerance_Set(t *testing.T) {
	cfg, err := Load(writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\nverification:\n  row_count_tolerance: 0.05\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.RowCountTolerance(); got != 0.05 {
		t.Errorf("RowCountTolerance() = %v, want 0.05", got)
	}
}

func TestLoad_ToleranceOutOfRange(t *testing.T) {
	for _, value := range []string{"-0.1", "1.5"} {
		t.Run(value, func(t *testing.T) {
			body := "storage:\n  local:\n    path: /var/lib/argus\nverification:\n  row_count_tolerance: " + value + "\n"

			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Error("Load() error = nil, want a rejected tolerance")
			}
		})
	}
}

func TestSmokeQuery(t *testing.T) {
	const query = "SELECT 1 FROM users LIMIT 1"

	cfg, err := Load(writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\nverification:\n  smoke_query: "+query+"\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.SmokeQuery(); got != query {
		t.Errorf("SmokeQuery() = %q, want %q", got, query)
	}

	absent, err := Load(writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := absent.SmokeQuery(); got != "" {
		t.Errorf("SmokeQuery() = %q, want empty", got)
	}
}

func TestRetentionPolicy_Default(t *testing.T) {
	cfg, err := Load(writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.RetentionPolicy(); got != retention.Default {
		t.Errorf("RetentionPolicy() = %+v, want the default %+v", got, retention.Default)
	}
}

// Setting one rule must not silently switch the others off.
func TestRetentionPolicy_PartialOverride(t *testing.T) {
	cfg, err := Load(writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\nretention:\n  daily: 30\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	got := cfg.RetentionPolicy()

	if got.Daily != 30 {
		t.Errorf("Daily = %d, want 30", got.Daily)
	}
	if got.Weekly != retention.Default.Weekly {
		t.Errorf("Weekly = %d, want the default %d", got.Weekly, retention.Default.Weekly)
	}
	if got.Monthly != retention.Default.Monthly {
		t.Errorf("Monthly = %d, want the default %d", got.Monthly, retention.Default.Monthly)
	}
}

// An explicit zero disables a rule, and must not read as "unset".
func TestRetentionPolicy_ExplicitZero(t *testing.T) {
	cfg, err := Load(writeConfig(t, "storage:\n  local:\n    path: /var/lib/argus\nretention:\n  daily: 1\n  weekly: 0\n  monthly: 0\n"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := retention.Policy{Daily: 1, Weekly: 0, Monthly: 0}
	if got := cfg.RetentionPolicy(); got != want {
		t.Errorf("RetentionPolicy() = %+v, want %+v", got, want)
	}
}

func TestLoad_NegativeRetention(t *testing.T) {
	for _, field := range []string{"daily", "weekly", "monthly"} {
		t.Run(field, func(t *testing.T) {
			body := "storage:\n  local:\n    path: /var/lib/argus\nretention:\n  " + field + ": -1\n"

			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Error("Load() error = nil, want a rejected negative count")
			}
		})
	}
}
