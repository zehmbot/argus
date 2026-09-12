# argus

Backs up PostgreSQL databases to S3-compatible object storage, and
automatically checks that each backup can actually be restored.

```console
$ argus verify --latest
time=2026-09-12T12:35:42.238+01:00 level=INFO msg="verification complete" backup_id=2026-09-12T11-35-23Z-18fb status=verified restored_pg_version=17.11 checks=4
backup 2026-09-12T11-35-23Z-18fb: verified (restored on PostgreSQL 17.11)

CHECK                        RESULT  DETAIL
restore_exit_code            pass
schema_fingerprint_match     pass
row_counts_within_tolerance  pass
user_smoke_query             pass
```

**Status: v0.1.0, not yet released.** The artifact layout and manifest format
are not stable and may change before v1. Integration tests exercise
PostgreSQL 17 only; other major versions are untested rather than
unsupported. Expect breaking changes.

## Why not pgBackRest?

You probably should use pgBackRest, WAL-G, or Barman.

- **pgBackRest** — full, differential and incremental backups, parallelism,
  and point-in-time recovery. The default answer for a single cluster you
  care about.
- **WAL-G** — continuous WAL archiving and delta backups, built for running
  against object storage.
- **Barman** — centralised backup management when you have many servers to
  look after rather than one.

They are mature, they are maintained by people who have been doing this for
years, and they do things argus deliberately does not.

argus exists because of a narrower problem. **Most backups are never tested.**
They are taken every night, they are never restored, and the first restore
anyone attempts is the one that has to work. That is when people find out the
dump was truncated, or the schema drifted, or the encryption key was rotated
eight months ago.

So the centre of this tool is not `backup`. It is `verify`: restore the
backup into a throwaway database, compare what came back against what was
recorded when it was taken, and write the answer down. And the rule that
follows from it — **`prune` refuses to delete anything unless at least one
backup that has been proven to restore would survive.**

If your backup tool already does that, use your backup tool.

## What it does not do

Stated plainly, because a backup tool that is vague about its limits is worse
than one with fewer features.

- **This is a logical backup, not point-in-time recovery.** `pg_dump`
  produces a consistent snapshot as of the moment it starts. You can restore
  to the time a backup was taken, and to no other time. If you need to
  recover to 14:32 last Tuesday, you need WAL archiving, and you need one of
  the tools above.
- **One database per run.** Deciding what partial failure means when database
  three of five fails is a real design problem, and it has been deferred
  rather than guessed at.
- **No incremental backups.** Every backup is a full dump.
- **No scheduler.** cron and systemd timers already exist.
- **`verify` requires Docker** on whatever host runs it, because it starts a
  throwaway PostgreSQL. `backup`, `list`, `restore` and `prune` do not.
- **`verify` cannot see your extensions.** It restores into a stock image.
  See [What verify does not prove](#what-verify-does-not-prove).
- **A process killed outright can leave things behind.** If argus is killed
  by `SIGKILL`, an OOM kill, or a host reboot, it cannot abort its own
  in-flight multipart upload, and `verify` cannot remove its own container.
  See [Operational notes](#operational-notes).

## Requirements

- `pg_dump` and `pg_restore` on `PATH`, **at least as new as the server being
  backed up**. `pg_dump` refuses to dump a newer server than itself.
- `docker` on `PATH`, for `verify` only.
- An S3-compatible bucket, or a local directory.

argus is a single static binary and needs nothing else installed.

## Getting started

```sh
cp argus.example.yaml argus.yaml
export ARGUS_DATABASE_URL="postgres://user:password@localhost:5432/app_production?sslmode=disable"

argus backup
argus list
argus verify --latest
```

## Configuration

Secrets are never in the config file, so it can be committed. They come from
the environment:

| Variable | Used by | Purpose |
|---|---|---|
| `ARGUS_DATABASE_URL` | all | the database to back up |
| `ARGUS_S3_ACCESS_KEY_ID` | all, with S3 storage | object storage credentials |
| `ARGUS_S3_SECRET_ACCESS_KEY` | all, with S3 storage | object storage credentials |
| `ARGUS_AGE_IDENTITY` | `restore`, `verify` | the private key, when backups are encrypted |

The `ARGUS_` prefix is deliberate. argus often runs on a host that already
has credentials set for something else, and silently inheriting those is how
a backup ends up in the wrong account.

```yaml
# Exactly one storage backend.
storage:
  local:
    path: /var/lib/argus

  # s3:
  #   endpoint: s3.eu-central-1.amazonaws.com
  #   bucket: argus-backups
  #   region: eu-central-1
  #   insecure: false   # only a local MinIO should ever need true

# Optional. Artifacts are encrypted to this age recipient.
encryption:
  recipient: age1...

# Optional. What counts as a successful verification.
verification:
  row_count_tolerance: 0.01
  smoke_query: "SELECT 1 FROM users WHERE email IS NOT NULL LIMIT 1"

# Optional. How many backups prune keeps.
retention:
  daily: 7
  weekly: 4
  monthly: 6
```

See `argus.example.yaml` for the annotated version.

### Encryption

Artifacts are encrypted with [age](https://age-encryption.org). The
*recipient* is a public key and lives in the config file; the *identity* is
the private half and is only ever read by `restore` and `verify`.

That asymmetry is the point. A backup host that is compromised can keep
writing backups, and cannot **read** back a single one it has already made.

It says nothing about deletion. Encryption protects confidentiality; only
your bucket permissions protect the backups from being destroyed. See
[Operational notes](#operational-notes).

```sh
age-keygen -o argus-identity.txt   # keep this somewhere else entirely
```

Encryption is optional. Without a recipient, artifacts are stored as plain
gzip.

## Commands

| Command | What it does |
|---|---|
| `argus backup` | dump, compress, encrypt, upload, write a manifest |
| `argus list [--json]` | show the backups in storage, newest first |
| `argus verify <id> \| --latest` | restore into a throwaway database and check it |
| `argus restore <id> --target <dsn>` | restore into a database you name, which should be empty |
| `argus prune [--apply]` | apply the retention policy |
| `argus version` | print the version |

Every command takes `--config` and `--log-format=text|json`.

### Exit codes

argus runs under cron, so monitoring has to tell the failure modes apart.

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | configuration error |
| 2 | backup failed |
| 3 | verification failed, or prune refused because nothing verified would survive |
| 4 | storage error |

**3 is more alarming than 2.** A failed backup is a problem you know about. A
failed verification means you thought you had backups and they do not
restore.

## Restoring in an emergency

**1. Find the backup.**

```console
$ argus list
BACKUP ID                  CREATED (UTC)        SIZE       VERIFIED
2026-09-12T11-35-23Z-18fb  2026-09-12 11:35:23  766.9 KiB  verified
```

Newest first. `VERIFIED` is the column that matters: `verified` means this
exact artifact has been restored and checked at least once.

**2. Prefer a verified backup.**

`pending` means nobody has ever restored it. `failed` means someone tried and
it did not work — do not reach for it first.

```sh
argus list --json | jq -r '[.[] | select(.verification.status == "verified")][0].backup_id'
```

**3. Restore into a new, empty database.**

```sh
createdb app_recovery
argus restore 2026-09-12T11-35-23Z-18fb \
  --target "postgres://user:password@localhost:5432/app_recovery?sslmode=disable"
```

Never restore over the database you are trying to rescue. If the restore is
wrong, you want to still have the broken original.

**4. Check it before pointing anything at it.**

Compare against what the backup recorded, then run a query your application
actually depends on.

```sh
argus list --json | jq '.[0].table_counts'
psql "$RECOVERY_DSN" -c "SELECT count(*) FROM users;"
```

**5. If the restore fails.**

| Exit code | What it means | What to do |
|---|---|---|
| 1 | configuration — bad DSN, missing `ARGUS_AGE_IDENTITY` | fix and re-run; nothing was written |
| 4 | storage — the artifact could not be fetched | check credentials and network |
| anything else | the artifact failed its checksum, or `pg_restore` rejected it | try the next older verified backup |

A checksum failure means that artifact is gone. Move to the previous one in
`argus list` rather than retrying the same id.

## How a backup is made

```
pg_dump → gzip → age encrypt → object storage
                ↑
        sha256 computed in transit
```

All four stages run at once, joined by a pipe. Nothing is staged on disk, so
backing up a 40 GB database does not require 40 GB of free space on the host,
and the plaintext dump is never written to the backup host's disk at all.

If `pg_dump` fails part way through, the pipe is closed with that error, so
the upload fails rather than storing a truncated artifact that looks
complete.

Each backup writes a JSON manifest alongside its artifact, recording the
source host and database, the server and `pg_dump` versions, the artifact's
size and sha256, a fingerprint of the schema, the row count of every table,
and the outcome of the last verification.

**The bucket is the source of truth.** There is no state database to drift
out of sync with reality, and nothing to back up in turn. The manifest is
written last, after the upload is confirmed, so a backup that fails leaves
nothing behind that `list` or `prune` would take for a real one.

`verify` is the one thing that rewrites a manifest that already exists,
recording its result in place. That is a deliberate exception: the alternative
is a second object holding verification state, which can then disagree with
the manifest it describes. Enable bucket versioning if you want the history of
those rewrites.

## How verification works

`argus verify` does what nobody does by hand often enough:

1. Starts a PostgreSQL container matching the major version the manifest
   recorded.
2. Downloads the artifact and checks its sha256 **before** touching anything.
3. Decrypts and decompresses it straight into `pg_restore`.
4. Runs the checks.
5. Writes the result into the manifest.
6. Removes the container.

| Check | What it catches |
|---|---|
| `restore_exit_code` | a dump that will not load at all |
| `schema_fingerprint_match` | schema drift between the backup and the restore |
| `row_counts_within_tolerance` | data lost, or a restore from the wrong source |
| `user_smoke_query` | whatever only you can express about your own data |

Row counts are read just before `pg_dump` starts, while `pg_dump` then runs
in its own consistent snapshot, so on a database being written to the two
legitimately disagree. Hence the tolerance, which defaults to 1%.

Be clear about what that default buys and costs: **a 1% tolerance means
losing up to 1% of a table's rows passes verification.** Setting
`row_count_tolerance: 0` demands exact counts, which is right for a database
that is quiet while the backup runs and will produce constant false alarms
for one that is not.

The outcome is recorded whether it passes or fails. A backup known to be bad
is worth more than one nobody has an opinion about.

### What verify does not prove

`verify` restores into a stock `postgres:<major>` image. It proves the
artifact loads and that the data matches what was recorded. It does not prove
a restore will succeed on your servers.

A dump that uses an extension the stock image does not carry — PostGIS,
`pg_trgm`, `pgcrypto` — will fail to restore in that container and report a
verification failure that says nothing about the backup. The same applies to
locales, tablespaces, and roles your cluster has and the container does not.

**The image is not configurable today.** If your database uses extensions,
`verify` will report false alarms, and a periodic `restore` into a database
you control is the check that means something.

## Retention

Grandfather-father-son: keep the most recent backup of each of the last N
days, weeks and months.

Periods are counted **from the backups that exist, not from today**. A system
that stopped backing up a month ago does not lose its entire history at once,
which is precisely when it can least afford to.

`prune` is a dry run unless you pass `--apply`.

> [!WARNING]
> `argus prune --apply` permanently deletes backups and their manifests. Run
> it without the flag first and read the plan.

And the rule the whole tool is built around:

> If carrying out the plan would leave no backup that has been proven to
> restore, `prune` deletes nothing and exits 3.

Paying for storage is cheaper than losing the last valid restore point.

## Operational notes

**Do not give the backup host permission to delete.** The encryption design
stops a compromised host reading your backups; it does nothing to stop one
destroying them. A host that only runs `backup` needs `s3:PutObject` and
`s3:ListBucket`, and should not have `s3:DeleteObject`.

`prune` does need delete permission. Give it separate credentials and run it
from somewhere other than the machine being backed up.

For protection against deletion — accidental, malicious, or from a bug in
this tool — use S3 Object Lock, or at minimum bucket versioning. Permissions
are a policy you can misconfigure; Object Lock is one the storage enforces.

**Expire incomplete multipart uploads.** An artifact's size is not known
before it is written, so every upload goes through S3's multipart path. argus
aborts the upload when it fails — but a process killed outright cannot abort
anything, and the parts already uploaded then sit in the bucket costing money
forever. No program can fix this from inside; set a lifecycle rule on the
bucket.

**Clean up abandoned verification containers.** For the same reason, a
`verify` that is killed leaves its container running. They are all labelled,
so they are easy to find:

> [!CAUTION]
> This removes every container carrying the label, including one belonging to
> a `verify` that is still running. Check `docker ps` first.

```sh
docker rm -f $(docker ps -aq --filter label=argus=verify)
```

**Match your client version to your server.** `pg_dump` refuses to dump a
server newer than itself, and this will bite the first time a server is
upgraded.

**Run `verify` on a schedule, not just after a failure.** A backup you have
not restored is a backup you are assuming works, which is the assumption this
tool exists to remove.

## Security

argus handles database credentials, full database dumps, and encryption keys.
Report vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## Development

```sh
docker compose up -d    # postgres and minio for local work

make build
make test               # unit tests; needs nothing installed
make test-integration   # needs docker and pg_dump
```

The integration tests start real PostgreSQL and MinIO containers and run the
built binary against them, including the cases that are hard to believe
without seeing: a backup killed mid-upload leaving nothing usable, a
multipart upload aborted rather than orphaned, a single flipped byte caught
by the checksum, and a verification that fails when the data does not match
what was recorded.

They are behind a build tag, so `go test ./...` stays runnable with nothing
installed.
