package manifest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"time"
)

// Verification statuses.
const (
	StatusPending  = "pending"
	StatusVerified = "verified"
	StatusFailed   = "failed"
)

const (
	backupsPrefix      = "backups"
	artifactExtension  = ".dump.gz"
	encryptedExtension = ".age"
	manifestExtension  = ".json"
)

// Manifest describes one backup. It is written to object storage alongside
// the artifact it describes, and is the only record argus keeps: the bucket
// is the source of truth, there is no state database.
type Manifest struct {
	BackupID          string           `json:"backup_id"`
	CreatedAt         time.Time        `json:"created_at"`
	DurationSeconds   int              `json:"duration_seconds"`
	Source            Source           `json:"source"`
	Artifact          Artifact         `json:"artifact"`
	SchemaFingerprint string           `json:"schema_fingerprint"`
	TableCounts       map[string]int64 `json:"table_counts"`
	Verification      Verification     `json:"verification"`
	ToolVersion       string           `json:"tool_version"`
}

// Source records what was backed up, so a restore can be compared against it.
type Source struct {
	Host          string `json:"host"`
	Database      string `json:"database"`
	PGVersion     string `json:"pg_version"`
	PGDumpVersion string `json:"pg_dump_version"`
}

// Artifact records where the dump landed and how to check it.
type Artifact struct {
	ObjectKey   string `json:"object_key"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Compression string `json:"compression"`
	// EncryptionRecipient is the age public key the artifact was encrypted
	// to, empty when encryption is not configured. Recording it means a
	// restore can say which key it needs rather than failing opaquely.
	EncryptionRecipient string `json:"encryption_recipient,omitempty"`
}

// Verification records the outcome of restoring the artifact and checking it.
type Verification struct {
	Status            string     `json:"status"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	RestoredPGVersion string     `json:"restored_pg_version,omitempty"`
	Checks            []Check    `json:"checks,omitempty"`
}

// Check is one named pass/fail assertion made during verification.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// Encode writes m as indented JSON. Indented because a human debugging a
// broken backup at 3am reads these directly out of the bucket.
func (m Manifest) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	return nil
}

// Decode reads a manifest and rejects one that is syntactically valid JSON
// but describes no usable backup: a truncated or empty document must not
// pass for a backup that can be listed, restored, or counted by prune.
func Decode(r io.Reader) (Manifest, error) {
	var m Manifest
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decoding manifest: %w", err)
	}

	if m.BackupID == "" {
		return Manifest{}, errors.New("decoding manifest: missing backup_id")
	}
	if m.Artifact.ObjectKey == "" {
		return Manifest{}, errors.New("decoding manifest: missing artifact.object_key")
	}

	return m, nil
}

// NewID returns a backup ID such as 2026-09-05T03-00-12Z-a3f9: an RFC 3339
// UTC timestamp with the colons replaced, because the ID becomes part of an
// object key and a local file name, plus 16 random bits so that two backups
// started within the same second do not collide.
func NewID(t time.Time) (string, error) {
	var suffix [2]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generating backup id: %w", err)
	}

	return t.UTC().Format("2006-01-02T15-04-05Z") + "-" + hex.EncodeToString(suffix[:]), nil
}

// ArtifactKey returns the object key of a backup's artifact. encrypted adds
// the .age suffix, so that the key states what the object actually is and a
// human reading the bucket can tell encrypted backups from plain ones.
func ArtifactKey(database, backupID string, encrypted bool) string {
	name := backupID + artifactExtension
	if encrypted {
		name += encryptedExtension
	}

	return path.Join(backupsPrefix, database, name)
}

// ManifestKey returns the object key of a backup's manifest, stored
// alongside its artifact.
func ManifestKey(database, backupID string) string {
	return path.Join(backupsPrefix, database, backupID+manifestExtension)
}
