# Security policy

argus handles database credentials, full database dumps, and encryption keys.
A vulnerability in it is a vulnerability in whatever it is backing up, so
please report privately rather than opening an issue.

## Reporting

Use GitHub's private vulnerability reporting: go to the **Security** tab of
this repository and choose **Report a vulnerability**. That creates a private
advisory only the maintainers can see.

Please include what you were running, what happened, and the smallest set of
steps that reproduces it. A proof of concept helps but is not required.

## Scope

Anything that could expose a database, its backups, or the keys to them. In
particular:

- credentials or key material reaching logs, error messages, the config file,
  or an artifact
- an artifact that can be read without the age identity it was encrypted to
- a corrupted or truncated artifact that passes verification
- `prune` deleting backups the retention policy and safety rule say it should
  keep
- path handling that lets a crafted object key write outside the storage root

Reports about a database or object store that has been misconfigured — public
buckets, credentials committed to a repository — are worth telling us about
but are not vulnerabilities in argus.

## Supported versions

argus is pre-1.0 and has no released versions yet. Fixes land on `main`.
There are no backports.
