//go:build integration

// Package integration exercises the argus binary end to end against a real
// PostgreSQL started by the test itself.
//
// These tests are behind the "integration" build tag so that the default
// `go test ./...` needs neither Docker nor pg_dump installed.
package integration

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// argusBin is the binary under test, built once for the whole package.
var argusBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "argus-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating temp dir:", err)
		os.Exit(1)
	}

	argusBin = filepath.Join(dir, "argus")
	if runtime.GOOS == "windows" {
		argusBin += ".exe"
	}

	// Test the binary that ships, not an in-process call. That is the only
	// way to cover the exit codes, which are part of the CLI contract.
	build := exec.Command("go", "build", "-o", argusBin, "../../cmd/argus")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building argus: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

// runArgus invokes the binary and returns its combined output and exit code.
func runArgus(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command(argusBin, args...)
	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode()
	}

	t.Fatalf("running argus %v: %v\n%s", args, err, out)

	return "", 0
}

// writeConfig writes a config file pointing storage at a fresh directory.
func writeConfig(t *testing.T) (configPath, storageRoot string) {
	t.Helper()

	dir := t.TempDir()
	storageRoot = filepath.Join(dir, "storage")
	configPath = filepath.Join(dir, "argus.yaml")

	// ToSlash because a Windows path's backslashes would be escape
	// sequences to the YAML parser.
	body := fmt.Sprintf("storage:\n  local:\n    path: %s\n", filepath.ToSlash(storageRoot))
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	return configPath, storageRoot
}
