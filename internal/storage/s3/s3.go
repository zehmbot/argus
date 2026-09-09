// Package s3 implements storage.Backend against any S3-compatible object
// store: AWS, Backblaze B2, Cloudflare R2, Scaleway, or MinIO locally.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/zehmbot/argus/internal/storage"
)

// Config describes how to reach the object store. Credentials are
// deliberately absent: they are passed separately because they come from the
// environment and must never be readable from the config file.
type Config struct {
	Endpoint string
	Bucket   string
	Region   string
	UseSSL   bool
}

// Credentials are the access key pair, read from the environment.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Backend stores objects in one bucket of an S3-compatible store.
type Backend struct {
	client *minio.Client
	bucket string
}

var _ storage.Backend = (*Backend)(nil)

// New connects to the object store and checks that the bucket is reachable.
func New(ctx context.Context, cfg Config, creds Credentials) (*Backend, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(creds.AccessKeyID, creds.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("creating s3 client: %w", err)
	}

	// Check the bucket now rather than at upload time, so a wrong name or a
	// bad key pair fails before pg_dump spends an hour producing an artifact
	// that has nowhere to go.
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("checking bucket %s: %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %s does not exist", cfg.Bucket)
	}

	return &Backend{client: client, bucket: cfg.Bucket}, nil
}

// Put writes the contents of r to key.
//
// The length is given as -1 because the artifact is produced by a pipeline
// and its final size is not known before it has been written. The client
// therefore uploads in parts, and aborts the multipart upload itself if a
// part fails, rather than leaving orphaned parts accruing storage charges.
func (b *Backend) Put(ctx context.Context, key string, r io.Reader) error {
	if _, err := b.client.PutObject(ctx, b.bucket, key, r, -1, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("putting %s: %w", key, err)
	}

	return nil
}

func (b *Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting %s: %w", key, err)
	}

	// GetObject is lazy: it does not contact the server until the first read,
	// so a missing object would otherwise surface far from this call, as a
	// read error inside whatever is consuming the stream. Stat forces the
	// request now so the contract's ErrNotFound is returned here.
	if _, err := obj.Stat(); err != nil {
		obj.Close()

		if isNotFound(err) {
			return nil, storage.ErrNotFound
		}

		return nil, fmt.Errorf("getting %s: %w", key, err)
	}

	return obj, nil
}

func (b *Backend) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string

	for obj := range b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, obj.Err)
		}

		keys = append(keys, obj.Key)
	}

	return keys, nil
}

func (b *Backend) Delete(ctx context.Context, key string) error {
	// S3 deletes are idempotent: removing a key that is not there succeeds.
	// The Backend contract says a missing key is ErrNotFound, and the local
	// implementation behaves that way, so check first rather than let the
	// two implementations disagree about what deleting nothing means.
	if _, err := b.Stat(ctx, key); err != nil {
		return err
	}

	if err := b.client.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("deleting %s: %w", key, err)
	}

	return nil
}

func (b *Backend) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	info, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return storage.ObjectInfo{}, storage.ErrNotFound
		}

		return storage.ObjectInfo{}, fmt.Errorf("statting %s: %w", key, err)
	}

	return storage.ObjectInfo{
		Key:     key,
		Size:    info.Size,
		ModTime: info.LastModified,
	}, nil
}

// isNotFound reports whether err is the object store saying the key does not
// exist, so that callers can use errors.Is(err, storage.ErrNotFound) without
// knowing which backend they are talking to.
func isNotFound(err error) bool {
	if errors.Is(err, storage.ErrNotFound) {
		return true
	}

	return minio.ToErrorResponse(err).Code == minio.NoSuchKey
}
