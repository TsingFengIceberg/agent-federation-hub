# ADR 0006: Artifact Object Data Plane

> **Status**: accepted for the current implementation | **Evidence**: deterministic, PostgreSQL 17, and MinIO tests | **Date**: 2026-08-28

## Context

A2A file Parts may contain raw bytes or remote URLs. Persisting those bytes in a
Task JSON document or PostgreSQL row makes state replay expensive, bypasses
content controls, and exposes every Task reader to untrusted binary data.
Fetching provider URLs also creates an SSRF boundary. Multi-tenant retention
requires quota accounting and cleanup that work across Hub instances.

## Decision

Externalize every observed raw or URL file Part before committing its Task/Event
mutation. Text and structured data remain inline. The normalized Part retains a
deterministic object ID, byte size, SHA-256 digest, detected media type, and
filename; raw base64 and provider URI are removed.

Spool each candidate through a bounded temporary file, detect and enforce a MIME
allowlist, reserve tenant byte/object quota, and scan before availability. A
ClamAV `INSTREAM` adapter is provided. Scanner error fails closed and releases
the reservation. Infected objects may be retained as quarantined evidence but
are never returned by the content API. Non-development startup requires both a
clean-scan policy and a configured scanner.

Object bytes live behind a replaceable interface with secure local-filesystem
and S3-compatible/MinIO implementations. PostgreSQL or the development journal
stores metadata only. A deterministic identity makes replay of the same A2A
observation idempotent and prevents duplicate quota charge.

Remote URL imports allow HTTPS only, reject credentials in URLs, validate every
redirect, resolve and pin public addresses per connection, reject private and
reserved ranges, and enforce response and streaming byte limits. Query and
fragment components are not retained in metadata.

Metadata and content routes derive tenant scope from the authenticated Principal
and require `artifacts:read`. Only `AVAILABLE` content is downloadable. Expired
objects are claimed through leases and `SKIP LOCKED`, deleted idempotently from
the object store, marked deleted, and then removed from tenant usage.

## Consequences

- PostgreSQL rows and Task Events no longer carry observed binary payloads.
- The object store is not a trust bypass; metadata state and scan policy control
  every Hub download.
- A failed upload or scan releases quota. Available and quarantined objects count
  until lifecycle deletion completes.
- Filesystem storage remains a local/single-node option. S3-compatible storage
  is the multi-instance object backend.
- Encryption-at-rest configuration, legal holds, object versioning, backup/
  restore, DLP/content moderation beyond malware scanning, and production-scale
  throughput tests remain deployment gates.

## Evidence

Deterministic tests cover size/MIME rejection, exact-size writes, scan failure,
ClamAV streaming, quarantine denial, idempotent accounting, query redaction,
tenant-scoped HTTP reads, journal replay, and lifecycle quota release. A real
PostgreSQL 17 test uses two connection pools for concurrent reservation and
deletion lease exclusion. A disposable MinIO test performs actual Put, Stat,
Get, and Delete operations.

## Related Material

- [Task/Event/Artifact contract](../specifications/task-event-artifact-contract.md)
- [A2A content research](../research/a2a-study/content-and-media-exchange.md)
- [PostgreSQL leased execution](0004-postgresql-leased-background-execution.md)
