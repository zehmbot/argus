package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/zehmbot/argus/internal/storage"
)

func TestPutGetRoundTrip(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	want := "hello backup"
	if err := b.Put(ctx, "backups/app/one.dump", bytes.NewBufferString(want)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	r, err := b.Get(ctx, "backups/app/one.dump")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	if string(got) != want {
		t.Errorf("Get() = %q, want %q", got, want)
	}
}

func TestGet_NotFound(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = b.Get(context.Background(), "missing")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestList_PrefixAndTempFilesExcluded(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	keys := []string{
		"backups/app/one.dump",
		"backups/app/two.dump",
		"backups/other/three.dump",
	}
	for _, k := range keys {
		if err := b.Put(ctx, k, bytes.NewBufferString("x")); err != nil {
			t.Fatalf("Put(%s) error = %v", k, err)
		}
	}

	got, err := b.List(ctx, "backups/app/")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	sort.Strings(got)

	want := []string{"backups/app/one.dump", "backups/app/two.dump"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("List() = %v, want %v", got, want)
	}
}

func TestDelete(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	if err := b.Put(ctx, "key", bytes.NewBufferString("x")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if err := b.Delete(ctx, "key"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := b.Get(ctx, "key"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = b.Delete(context.Background(), "missing")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

func TestStat(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	if err := b.Put(ctx, "key", bytes.NewBufferString("hello")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	info, err := b.Stat(ctx, "key")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	if info.Size != 5 {
		t.Errorf("Stat().Size = %d, want 5", info.Size)
	}
	if info.Key != "key" {
		t.Errorf("Stat().Key = %q, want %q", info.Key, "key")
	}
}

func TestStat_NotFound(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = b.Stat(context.Background(), "missing")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat() error = %v, want ErrNotFound", err)
	}
}

func TestResolve_PreventsPathTraversal(t *testing.T) {
	root := t.TempDir()
	b, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	if err := b.Put(ctx, "../../etc/passwd", bytes.NewBufferString("owned")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// The write must have landed inside root, not above it.
	got, err := b.Get(ctx, "etc/passwd")
	if err != nil {
		t.Fatalf("Get() error = %v, want the traversal to be neutralized into root/etc/passwd", err)
	}
	got.Close()
}
