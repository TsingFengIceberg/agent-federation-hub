# ADR 0008: A2A Binding Matrix and TCK Pins

> **Status**: accepted for the test baseline; complete multi-binding conformance remains open
> **Evidence**: verified local execution for the listed TCK runs; upstream revision relationship is recorded, not inferred

## Context

The selected normative A2A v1.0.0 source is pinned to commit
`173695755607e884aa9acf8ce4feed90e32727a1`, which is also embedded by the
pinned TCK. The newer mainline candidate
`16ba52690519bf55b9388e34d4db356efa88aa51` is recorded separately and is not
the current baseline. The Go SDK is pinned to
`v2.5.0` (`9d95b95445f4208ba77f48a137a278067937adb7`). The latest available
upstream TCK ref inspected for this baseline is
`5996b79f9cefa6fc390980e383e358a66fb9e49e`, which embeds protocol commit
`173695755607e884aa9acf8ce4feed90e32727a1`.

## Decision

1. Keep JSON-RPC plus SSE as the accepted product profile in
   [`a2a-profile.json`](../../tests/conformance/a2a-profile.json).
2. Make the repository-owned deterministic SUT selectable by Binding. Run the
   same executor and lifecycle scenarios for JSON-RPC and HTTP+JSON, with each
   result recorded independently in [`profile-matrix.json`](../../tests/conformance/profile-matrix.json).
3. Keep gRPC as an explicit opt-in profile with its own endpoint/TLS policy,
   Agent Card advertisement, authentication behavior, and current-spec tests.
4. Treat skipped requirements and the protocol revision mismatch as evidence
   boundaries. No profile is promoted to complete conformance by changing a
   status field alone.

## Current evidence

| Binding | Local TCK result | Product status |
|---|---:|---|
| JSON-RPC + SSE | 76 MUST requirements pass, 16 skipped, 22 not tested, 0 failed | accepted with waivers |
| HTTP+JSON + SSE | 71 MUST requirements pass, 20 skipped, 23 not tested, 0 failed | verified local, opt-in |
| gRPC server-streaming | 60 MUST requirements pass, 21 skipped, 33 not tested, 0 failed | verified local, opt-in |

The matrix counts above are the TCK's registered MUST requirements after
aggregation. The selected transport's `--level must` test-case counts are also
recorded in `profile-matrix.json` as `tckTransport*`; they are a separate
execution metric and must not be substituted for requirement status counts.

The HTTP+JSON SUT uses the SDK REST handler and a test-only wrapper for
post-executor subscription replay and empty-list array encoding. This wrapper
is part of the deterministic fixture, not a claim that every provider SDK has
identical subscription semantics.

The pinned Go SDK also has a known `AUTH_REQUIRED` hand-off behavior: its local
execution manager may retain an interrupted execution slot briefly, so an
existing-Task continuation can transiently return `task execution is already in
progress`. The Hub retries only that exact error for a request carrying a
remote Task ID, with a bounded attempt count; it never replays a new Task or an
ambiguous transport error. This compatibility shim is covered by unit tests and
does not upgrade the SDK's native `AUTH_REQUIRED` continuation semantics.

## Consequences

- The Hub can reject or route against an explicit profile rather than silently
  inheriting SDK transport defaults.
- TCK runs are reproducible only when the source revisions in the matrix are
  available; a future aligned TCK must replace the current revision-skew waiver.
- HTTP+JSON implementation work can proceed without expanding the accepted
  external contract before authentication, Push, signed cards, and remaining
  requirements are tested.

## References

- [A2A source submodule](../../submodules/a2a/)
- [A2A profile](../../tests/conformance/a2a-profile.json)
- [Binding matrix](../../tests/conformance/profile-matrix.json)
- [TCK waivers](../../tests/conformance/tck-waivers.json)
