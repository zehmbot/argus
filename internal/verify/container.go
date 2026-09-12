package verify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/zehmbot/argus/internal/postgres"
)

const (
	// containerLabel marks every container this package starts, so that any
	// left behind by a process that was killed can be found and removed.
	containerLabel = "argus=verify"

	// verifyDatabase is the database created inside the container.
	verifyDatabase = "argus_verify"

	readyTimeout  = 2 * time.Minute
	readyInterval = 250 * time.Millisecond
)

// Container is a throwaway PostgreSQL started for a single verification.
type Container struct {
	id string
	// DSN reaches the container on loopback.
	DSN string
	// Version is what the server actually reported, which is what the
	// manifest records as the version the backup was restored under.
	Version string
}

// StartPostgres runs a PostgreSQL whose major version matches the one
// recorded in a manifest, and waits until it accepts connections.
//
// It shells out to docker rather than linking a container library, for the
// same reason it shells out to pg_dump: the work belongs to a tool that
// already exists, and the shipped binary stays free of its dependencies.
// docker is then one more external program that one command needs, which the
// README has to state.
func StartPostgres(ctx context.Context, recordedVersion string) (*Container, error) {
	major, err := majorVersion(recordedVersion)
	if err != nil {
		return nil, err
	}

	password, err := randomPassword()
	if err != nil {
		return nil, err
	}

	// Bound to loopback rather than every interface: this database briefly
	// holds a full copy of production data.
	id, err := dockerOutput(ctx,
		"run", "--detach",
		"--label", containerLabel,
		"--env", "POSTGRES_PASSWORD="+password,
		"--env", "POSTGRES_DB="+verifyDatabase,
		"--publish", "127.0.0.1::5432",
		"postgres:"+major,
	)
	if err != nil {
		return nil, fmt.Errorf("starting verification database: %w", err)
	}

	container := &Container{id: id}

	port, err := dockerOutput(ctx, "port", id, "5432/tcp")
	if err != nil {
		container.Remove(context.WithoutCancel(ctx))

		return nil, fmt.Errorf("reading container port: %w", err)
	}

	container.DSN = fmt.Sprintf(
		"postgres://postgres:%s@127.0.0.1:%s/%s?sslmode=disable",
		password, hostPort(port), verifyDatabase,
	)

	version, err := waitReady(ctx, container.DSN)
	if err != nil {
		// Never leave a container behind because the wait failed.
		container.Remove(context.WithoutCancel(ctx))

		return nil, err
	}

	container.Version = version

	return container, nil
}

// Remove deletes the container. It is safe to call more than once.
func (c *Container) Remove(ctx context.Context) error {
	if c == nil || c.id == "" {
		return nil
	}

	if _, err := dockerOutput(ctx, "rm", "--force", "--volumes", c.id); err != nil {
		return fmt.Errorf("removing verification database: %w", err)
	}

	c.id = ""

	return nil
}

// waitReady polls until the server answers, returning the version it
// reports.
//
// Polling the thing we actually need beats sleeping for a guessed interval
// or trusting a log line: the container is ready exactly when a query
// against it succeeds.
func waitReady(ctx context.Context, dsn string) (string, error) {
	deadline := time.Now().Add(readyTimeout)

	var lastErr error

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		version, err := postgres.ServerVersion(ctx, dsn)
		if err == nil {
			return version, nil
		}

		lastErr = err

		time.Sleep(readyInterval)
	}

	return "", fmt.Errorf("verification database never became ready: %w", lastErr)
}

// majorVersion reduces a recorded server version to the major version that
// names a PostgreSQL image: "17.11" becomes "17".
//
// Only the major version is used. A backup taken on 17.11 restores perfectly
// well on 17.12, and pinning the patch release would make verification fail
// the day an image stops being published.
func majorVersion(recorded string) (string, error) {
	trimmed := strings.TrimSpace(recorded)

	end := strings.IndexFunc(trimmed, func(r rune) bool {
		return r < '0' || r > '9'
	})

	switch {
	case end == 0 || trimmed == "":
		return "", fmt.Errorf("cannot read a major version from %q", recorded)
	case end < 0:
		return trimmed, nil
	}

	return trimmed[:end], nil
}

// hostPort takes the port from docker's "127.0.0.1:49154" output, which can
// carry more than one line when a port is published on several addresses.
func hostPort(out string) string {
	line := strings.TrimSpace(out)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}

	if i := strings.LastIndex(line, ":"); i >= 0 {
		return line[i+1:]
	}

	return line
}

// randomPassword returns a throwaway password, so that no fixed credential
// is baked into the tool even for a container that lives for a minute.
func randomPassword() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating a password for the verification database: %w", err)
	}

	return hex.EncodeToString(b[:]), nil
}

func dockerOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}
