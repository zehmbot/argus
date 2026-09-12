//go:build integration

package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/zehmbot/argus/internal/postgres"
	"github.com/zehmbot/argus/internal/verify"
)

// labelledContainers returns the ids of containers argus started for
// verification, so the test can see them appear and disappear.
func labelledContainers(t *testing.T) []string {
	t.Helper()

	out, err := exec.Command("docker", "ps", "--all", "--quiet", "--filter", "label=argus=verify").Output()
	if err != nil {
		t.Fatalf("listing containers: %v", err)
	}

	return strings.Fields(string(out))
}

func TestStartPostgres(t *testing.T) {
	ctx := context.Background()

	before := len(labelledContainers(t))

	container, err := verify.StartPostgres(ctx, "17.11")
	if err != nil {
		t.Fatalf("StartPostgres() error = %v", err)
	}
	t.Cleanup(func() { _ = container.Remove(context.Background()) })

	// Only the major version is asked for, so the patch release may differ.
	if !strings.HasPrefix(container.Version, "17.") {
		t.Errorf("Version = %q, want a 17.x release", container.Version)
	}

	// Bound to loopback, since this database briefly holds production data.
	if !strings.Contains(container.DSN, "127.0.0.1") {
		t.Errorf("DSN = %q, want it bound to loopback", container.DSN)
	}

	// Ready means queryable, not merely started.
	counts, err := postgres.TableCounts(ctx, container.DSN)
	if err != nil {
		t.Fatalf("TableCounts() error = %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("a fresh verification database holds %v, want nothing", counts)
	}

	// The label is what makes a container left behind by a killed process
	// findable afterwards.
	if got := len(labelledContainers(t)); got != before+1 {
		t.Errorf("found %d labelled containers, want %d", got, before+1)
	}

	if err := container.Remove(ctx); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if got := len(labelledContainers(t)); got != before {
		t.Errorf("found %d labelled containers after removal, want %d", got, before)
	}

	// Gone means gone: nothing is listening any more.
	if _, err := postgres.ServerVersion(ctx, container.DSN); err == nil {
		t.Error("the database still answers after Remove")
	}

	// Removing twice must not fail; cleanup paths call it more than once.
	if err := container.Remove(ctx); err != nil {
		t.Errorf("second Remove() error = %v", err)
	}
}

// A version with no published image must fail without leaving a container
// behind.
func TestStartPostgres_UnknownVersion(t *testing.T) {
	ctx := context.Background()

	before := len(labelledContainers(t))

	container, err := verify.StartPostgres(ctx, "3.1")
	if err == nil {
		_ = container.Remove(ctx)
		t.Fatal("StartPostgres() error = nil, want a failure for an unpublished version")
	}

	if got := len(labelledContainers(t)); got != before {
		t.Errorf("found %d labelled containers after a failed start, want %d", got, before)
	}
}
