package config

import (
	"fmt"
	"os"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

// Every secret is read from the environment, never from the config file.
// The ARGUS_ prefix is deliberate: argus often runs on a host that already
// has credentials set for something else, and silently picking those up is
// how a backup ends up in the wrong account.
const (
	sourceDSNEnvVar   = "ARGUS_DATABASE_URL"
	s3AccessKeyEnvVar = "ARGUS_S3_ACCESS_KEY_ID"
	s3SecretKeyEnvVar = "ARGUS_S3_SECRET_ACCESS_KEY"
	ageIdentityEnvVar = "ARGUS_AGE_IDENTITY"
)

// Config is the root of argus.yaml. Only the fields the current phase needs
// exist here; S3 and retention settings are added as later phases need them.
type Config struct {
	Storage    StorageConfig     `yaml:"storage"`
	Encryption *EncryptionConfig `yaml:"encryption"`
}

type StorageConfig struct {
	Local *LocalStorageConfig `yaml:"local"`
	S3    *S3StorageConfig    `yaml:"s3"`
}

type LocalStorageConfig struct {
	Path string `yaml:"path"`
}

// S3StorageConfig describes an S3-compatible bucket. The access keys are not
// here: they come from the environment, because this file gets committed.
//
// Insecure rather than a use_ssl flag, so that the zero value is the safe
// one: leaving it out gives TLS, and turning it off has to be deliberate.
// Only a local MinIO should ever need it.
type S3StorageConfig struct {
	Endpoint string `yaml:"endpoint"`
	Bucket   string `yaml:"bucket"`
	Region   string `yaml:"region"`
	Insecure bool   `yaml:"insecure"`
}

// EncryptionConfig names the age recipient that artifacts are encrypted to.
//
// A recipient is a public key, so unlike the database password it is safe in
// a file that gets committed. The matching identity is a secret and is never
// read from here: that asymmetry is the point, since it lets a server write
// backups it cannot itself read back.
type EncryptionConfig struct {
	Recipient string `yaml:"recipient"`
}

// Load reads and validates a config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config file %s: %w", path, err)
	}

	return &cfg, nil
}

// SourceDSN returns the connection string for the database to back up. It is
// read from an environment variable, never from the config file
func SourceDSN() (string, error) {
	dsn := os.Getenv(sourceDSNEnvVar)
	if dsn == "" {
		return "", fmt.Errorf("%s environment variable is not set", sourceDSNEnvVar)
	}

	return dsn, nil
}

// Recipient returns the age recipient to encrypt artifacts to, or nil when
// encryption is not configured.
func (c *Config) Recipient() (age.Recipient, error) {
	if c.Encryption == nil {
		return nil, nil
	}

	recipient, err := age.ParseX25519Recipient(c.Encryption.Recipient)
	if err != nil {
		return nil, fmt.Errorf("parsing encryption.recipient: %w", err)
	}

	return recipient, nil
}

func (c *Config) validate() error {
	if err := c.Storage.validate(); err != nil {
		return err
	}

	if c.Encryption != nil {
		if c.Encryption.Recipient == "" {
			return fmt.Errorf("encryption.recipient is required when encryption is configured")
		}

		// Parse it now so a mistyped key is rejected at startup, rather than
		// after pg_dump has already spent an hour on a large database.
		if _, err := c.Recipient(); err != nil {
			return err
		}
	}

	return nil
}

// S3Credentials returns the S3 access key pair from the environment.
func S3Credentials() (accessKeyID, secretAccessKey string, err error) {
	accessKeyID = os.Getenv(s3AccessKeyEnvVar)
	if accessKeyID == "" {
		return "", "", fmt.Errorf("%s environment variable is not set", s3AccessKeyEnvVar)
	}

	secretAccessKey = os.Getenv(s3SecretKeyEnvVar)
	if secretAccessKey == "" {
		return "", "", fmt.Errorf("%s environment variable is not set", s3SecretKeyEnvVar)
	}

	return accessKeyID, secretAccessKey, nil
}

// validate requires exactly one backend. Neither leaves backups nowhere to
// go; both would make it ambiguous which one holds the history, and a backup
// tool that is vague about where its backups are is worse than useless.
func (s StorageConfig) validate() error {
	switch {
	case s.Local == nil && s.S3 == nil:
		return fmt.Errorf("one of storage.local or storage.s3 is required")

	case s.Local != nil && s.S3 != nil:
		return fmt.Errorf("only one of storage.local or storage.s3 may be set")

	case s.Local != nil:
		if s.Local.Path == "" {
			return fmt.Errorf("storage.local.path is required")
		}

	case s.S3 != nil:
		if s.S3.Endpoint == "" {
			return fmt.Errorf("storage.s3.endpoint is required")
		}
		if s.S3.Bucket == "" {
			return fmt.Errorf("storage.s3.bucket is required")
		}
	}

	return nil
}

// AgeIdentity returns the private key used to decrypt artifacts, read from
// the environment.
//
// It is deliberately absent from the config file and from everything backup
// touches. Only the commands that read artifacts back need it, which is what
// lets a compromised backup host write new backups without being able to
// read the ones it already made.
func AgeIdentity() (*age.X25519Identity, error) {
	raw := os.Getenv(ageIdentityEnvVar)
	if raw == "" {
		return nil, fmt.Errorf("%s environment variable is not set", ageIdentityEnvVar)
	}

	identity, err := age.ParseX25519Identity(strings.TrimSpace(raw))
	if err != nil {
		// The error from the parser can quote the key it was given, so it is
		// not wrapped: nothing derived from a private key belongs in output.
		return nil, fmt.Errorf("%s is not a valid age identity", ageIdentityEnvVar)
	}

	return identity, nil
}
