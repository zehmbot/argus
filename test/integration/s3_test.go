//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/zehmbot/argus/internal/storage"
	"github.com/zehmbot/argus/internal/storage/s3"
)

const (
	// quay.io rather than Docker Hub: minio/minio there now requires
	// authentication, so an anonymous pull on a CI runner is refused with
	// "pull access denied". MinIO publishes the same releases to quay.io,
	// which serves them without credentials.
	minioImage  = "quay.io/minio/minio:RELEASE.2024-01-16T16-07-38Z"
	testBucket  = "argus-test"
	minioUser   = "argus"
	minioSecret = "argus-secret-key"
)

// startMinIO brings up MinIO with testBucket created, and returns the
// settings needed to reach it.
func startMinIO(t *testing.T) (s3.Config, s3.Credentials) {
	t.Helper()

	ctx := context.Background()

	container, err := tcminio.Run(ctx, minioImage,
		tcminio.WithUsername(minioUser),
		tcminio.WithPassword(minioSecret),
	)
	if err != nil {
		t.Fatalf("starting minio: %v", err)
	}

	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminating minio: %v", err)
		}
	})

	endpoint, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("minio connection string: %v", err)
	}

	cfg := s3.Config{Endpoint: endpoint, Bucket: testBucket, Region: "us-east-1"}
	creds := s3.Credentials{AccessKeyID: container.Username, SecretAccessKey: container.Password}

	admin, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(creds.AccessKeyID, creds.SecretAccessKey, ""),
	})
	if err != nil {
		t.Fatalf("minio.New() error = %v", err)
	}
	if err := admin.MakeBucket(ctx, testBucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
		t.Fatalf("creating bucket: %v", err)
	}

	return cfg, creds
}

func newS3Backend(t *testing.T) storage.Backend {
	t.Helper()

	cfg, creds := startMinIO(t)

	backend, err := s3.New(context.Background(), cfg, creds)
	if err != nil {
		t.Fatalf("s3.New() error = %v", err)
	}

	return backend
}

func put(t *testing.T, backend storage.Backend, key, body string) {
	t.Helper()

	if err := backend.Put(context.Background(), key, bytes.NewReader([]byte(body))); err != nil {
		t.Fatalf("Put(%q) error = %v", key, err)
	}
}

func get(t *testing.T, backend storage.Backend, key string) string {
	t.Helper()

	r, err := backend.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", key, err)
	}
	defer r.Close()

	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading %q: %v", key, err)
	}

	return string(body)
}

// The assertions below deliberately mirror the local backend's unit tests:
// two implementations of one interface are only interchangeable if they are
// held to the same contract.
func TestS3_PutGetRoundTrip(t *testing.T) {
	backend := newS3Backend(t)

	put(t, backend, "backups/app/one.dump.gz", "hello object storage")

	if got := get(t, backend, "backups/app/one.dump.gz"); got != "hello object storage" {
		t.Errorf("Get() = %q, want %q", got, "hello object storage")
	}
}

func TestS3_PutOverwrites(t *testing.T) {
	backend := newS3Backend(t)

	put(t, backend, "backups/app/one.json", "first")
	put(t, backend, "backups/app/one.json", "second")

	if got := get(t, backend, "backups/app/one.json"); got != "second" {
		t.Errorf("Get() = %q, want %q", got, "second")
	}
}

func TestS3_NotFound(t *testing.T) {
	backend := newS3Backend(t)
	ctx := context.Background()

	t.Run("get", func(t *testing.T) {
		if _, err := backend.Get(ctx, "backups/app/absent"); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Get() error = %v, want storage.ErrNotFound", err)
		}
	})

	t.Run("stat", func(t *testing.T) {
		if _, err := backend.Stat(ctx, "backups/app/absent"); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Stat() error = %v, want storage.ErrNotFound", err)
		}
	})

	// S3 deletes are idempotent, so this is the case where the backend has
	// to add behaviour rather than pass the store's answer through.
	t.Run("delete", func(t *testing.T) {
		if err := backend.Delete(ctx, "backups/app/absent"); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Delete() error = %v, want storage.ErrNotFound", err)
		}
	})
}

func TestS3_Delete(t *testing.T) {
	backend := newS3Backend(t)
	ctx := context.Background()

	put(t, backend, "backups/app/gone.json", "{}")

	if err := backend.Delete(ctx, "backups/app/gone.json"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := backend.Stat(ctx, "backups/app/gone.json"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat() after Delete error = %v, want storage.ErrNotFound", err)
	}
}

func TestS3_ListPrefix(t *testing.T) {
	backend := newS3Backend(t)

	put(t, backend, "backups/app/one.json", "{}")
	put(t, backend, "backups/app/two.json", "{}")
	put(t, backend, "backups/other/three.json", "{}")

	got, err := backend.List(context.Background(), "backups/app/")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	sort.Strings(got)

	want := []string{"backups/app/one.json", "backups/app/two.json"}
	if len(got) != len(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestS3_Stat(t *testing.T) {
	backend := newS3Backend(t)
	const body = "twenty-four bytes here!!"

	put(t, backend, "backups/app/one.dump.gz", body)

	info, err := backend.Stat(context.Background(), "backups/app/one.dump.gz")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	if info.Key != "backups/app/one.dump.gz" {
		t.Errorf("Key = %q, want %q", info.Key, "backups/app/one.dump.gz")
	}
	if info.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", info.Size, len(body))
	}
	if info.ModTime.IsZero() {
		t.Error("ModTime is zero")
	}
}

// Put is given a length of -1 because an artifact's size is not known before
// it is written, which sends anything larger than one part through the
// multipart path. This is the case that would break silently.
func TestS3_PutLargeObjectUsesMultipart(t *testing.T) {
	backend := newS3Backend(t)
	ctx := context.Background()

	// Larger than minio-go's default part size, so more than one part.
	const size = 20 << 20

	body := bytes.Repeat([]byte("argus backup payload 0123456789 "), size/32)
	want := sha256.Sum256(body)

	if err := backend.Put(ctx, "backups/app/large.dump.gz", bytes.NewReader(body)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	info, err := backend.Stat(ctx, "backups/app/large.dump.gz")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", info.Size, len(body))
	}

	r, err := backend.Get(ctx, "backups/app/large.dump.gz")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer r.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, r); err != nil {
		t.Fatalf("reading large object: %v", err)
	}

	if got := hash.Sum(nil); !bytes.Equal(got, want[:]) {
		t.Error("large object read back with a different checksum")
	}
}

// A wrong bucket name must fail when the backend is opened, not after a dump
// has already been produced.
func TestS3_NewRejectsMissingBucket(t *testing.T) {
	cfg, creds := startMinIO(t)
	cfg.Bucket = "no-such-bucket"

	if _, err := s3.New(context.Background(), cfg, creds); err == nil {
		t.Error("s3.New() error = nil, want a missing-bucket failure")
	}
}
