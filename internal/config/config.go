package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const sourceDSNEnvVar = "ARGUS_DATABASE_URL"

// Config is the root of argus.yaml. Only the fields phase 1 needs exist here;
// S3, encryption, and retention settings are added as later phases need them.
type Config struct {
	Storage StorageConfig `yaml:"storage"`
}

type StorageConfig struct {
	Local *LocalStorageConfig `yaml:"local"`
}

type LocalStorageConfig struct {
	Path string `yaml:"path"`
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

func (c *Config) validate() error {
	if c.Storage.Local == nil {
		return fmt.Errorf("storage.local is required")
	}

	if c.Storage.Local.Path == "" {
		return fmt.Errorf("storage.local.path is required")
	}

	return nil
}
