//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/zehmbot/argus/internal/manifest"
)

// runArgusSeparate keeps stdout and stderr apart, which is the whole point
// of the test below: the combined output would hide exactly the mixing it is
// meant to detect.
func runArgusSeparate(t *testing.T, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.Command(argusBin, args...)
	cmd.Env = append(os.Environ(), env...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("running argus %v: %v", args, err)
	}

	return outBuf.String(), errBuf.String(), code
}

// Logs go to stderr so that a command's output stays machine-readable. If a
// log line ever reaches stdout, piping list --json into another program
// breaks, and it breaks silently.
func TestLogging_DoesNotContaminateStdout(t *testing.T) {
	dsn := startPostgres(t)
	configPath, _ := writeConfig(t, "")
	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	_, backupStderr, code := runArgusSeparate(t, env, "backup", "--config", configPath, "--log-format", "json")
	if code != 0 {
		t.Fatalf("argus backup exited %d:\n%s", code, backupStderr)
	}

	stdout, stderr, code := runArgusSeparate(t, env, "list", "--config", configPath, "--json", "--log-format", "json")
	if code != 0 {
		t.Fatalf("argus list exited %d:\n%s", code, stderr)
	}

	var manifests []manifest.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifests); err != nil {
		t.Fatalf("stdout is not a clean JSON array: %v\n%s", err, stdout)
	}
	if len(manifests) != 1 {
		t.Errorf("decoded %d manifests, want 1", len(manifests))
	}
}

// With --log-format=json every log line has to be parseable, or a cron job
// shipping them somewhere gets a stream nothing can read.
func TestLogging_JSONFormat(t *testing.T) {
	dsn := startPostgres(t)
	configPath, _ := writeConfig(t, "")
	env := []string{"ARGUS_DATABASE_URL=" + dsn}

	_, stderr, code := runArgusSeparate(t, env, "backup", "--config", configPath, "--log-format", "json")
	if code != 0 {
		t.Fatalf("argus backup exited %d:\n%s", code, stderr)
	}

	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no log output on stderr")
	}

	var sawBackupComplete bool

	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}

		for _, field := range []string{"time", "level", "msg"} {
			if _, ok := entry[field]; !ok {
				t.Errorf("log entry has no %q: %s", field, line)
			}
		}

		if entry["msg"] == "backup complete" {
			sawBackupComplete = true

			for _, field := range []string{"backup_id", "database", "object_key", "size_bytes"} {
				if _, ok := entry[field]; !ok {
					t.Errorf("backup log has no %q: %s", field, line)
				}
			}
		}
	}

	if !sawBackupComplete {
		t.Errorf("no backup completion was logged:\n%s", stderr)
	}
}

// A connection string carries a password, and a log line is the easiest
// place for one to escape.
func TestLogging_NoCredentialsInOutput(t *testing.T) {
	const password = "hunter2"

	dsn := startPostgres(t)
	configPath, _ := writeConfig(t, "")

	// Same database, but with a password that would be recognisable if it
	// were ever echoed.
	leaky := strings.Replace(dsn, ":"+passwordOf(t, dsn)+"@", ":"+password+"@", 1)

	stdout, stderr, code := runArgusSeparate(t,
		[]string{"ARGUS_DATABASE_URL=" + leaky},
		"backup", "--config", configPath, "--log-format", "json",
	)

	// The wrong password must actually have been used and rejected. Without
	// this the test would still pass if the command failed for some earlier
	// reason that never touched the connection string.
	if code == 0 {
		t.Fatal("backup succeeded with a wrong password, so nothing was proven")
	}
	if !strings.Contains(stderr, "password authentication failed") {
		t.Fatalf("backup did not fail on authentication, so nothing was proven:\n%s", stderr)
	}

	for name, output := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if strings.Contains(output, password) {
			t.Errorf("%s contains the database password:\n%s", name, output)
		}
	}
}

// passwordOf pulls the password out of a connection string, so the test can
// swap it for a known one.
func passwordOf(t *testing.T, dsn string) string {
	t.Helper()

	_, rest, found := strings.Cut(dsn, "://")
	if !found {
		t.Fatalf("not a url-style dsn: %s", dsn)
	}

	credentials, _, found := strings.Cut(rest, "@")
	if !found {
		t.Fatalf("no credentials in dsn: %s", dsn)
	}

	_, password, found := strings.Cut(credentials, ":")
	if !found {
		t.Fatalf("no password in dsn: %s", dsn)
	}

	return password
}
