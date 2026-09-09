package main

import (
	"context"
	"fmt"

	"github.com/zehmbot/argus/internal/config"
	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/storage/local"
	"github.com/zehmbot/argus/internal/storage/s3"
)

// openBackend returns the storage backend the config selects. Validation has
// already established that exactly one of them is configured.
func openBackend(ctx context.Context, cfg *config.Config) (storage.Backend, error) {
	switch {
	case cfg.Storage.Local != nil:
		backend, err := local.New(cfg.Storage.Local.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: opening local storage: %w", errStorage, err)
		}

		return backend, nil

	case cfg.Storage.S3 != nil:
		// Credentials come from the environment, so that the file naming the
		// bucket can be committed while the keys to it cannot.
		accessKeyID, secretAccessKey, err := config.S3Credentials()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errConfig, err)
		}

		backend, err := s3.New(ctx, s3.Config{
			Endpoint: cfg.Storage.S3.Endpoint,
			Bucket:   cfg.Storage.S3.Bucket,
			Region:   cfg.Storage.S3.Region,
			UseSSL:   !cfg.Storage.S3.Insecure,
		}, s3.Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: opening s3 storage: %w", errStorage, err)
		}

		return backend, nil
	}

	return nil, fmt.Errorf("%w: no storage backend configured", errConfig)
}
