package config

import (
	"fmt"
	"os"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

const sourceDSNEnvVar = "ARGUS_DATABASE_URL"

// Config is the root of argus.yaml. Only the fields the current phase needs
// exist here; S3 and retention settings are added as later phases need them.
type Config struct {
	Storage    StorageConfig     `yaml:"storage"`
	Encryption *EncryptionConfig `yaml:"encryption"`
}

type StorageConfig struct {
	Local *LocalStorageConfig `yaml:"local"`
}

type LocalStorageConfig struct {
	Path string `yaml:"path"`
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
	if c.Storage.Local == nil {
		return fmt.Errorf("storage.local is required")
	}

	if c.Storage.Local.Path == "" {
		return fmt.Errorf("storage.local.path is required")
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
