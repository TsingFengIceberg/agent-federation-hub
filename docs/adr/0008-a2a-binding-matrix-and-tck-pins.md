# ADR 0008: A2A Binding Matrix and TCK Pins

> **Status**: accepted for the test baseline; complete multi-binding conformance remains open
> **Evidence**: verified local execution for the listed TCK runs; upstream revision relationship is recorded, not inferred

## Context

The current A2A repository source is pinned to commit
`16ba52690519bf55b9388e34d4db356efa88aa51` and the Go SDK is pinned to
`v2.5.0` (`9d95b95445f4208ba77f48a137a278067937adb7`). The latest available
upstream TCK ref inspected for this baseline is
`5996b79f9cefa6fc390980e383e358a66fb9e49e`, which embeds protocol commit
`173695755607e884aa9acf8ce4feed90e32727a1`. Those protocol commits differ, so
the TCK cannot be represented as a complete current-spec conformance result.

## Decision

1. Keep JSON-RPC plus SSE as the accepted product profile in
   [`a2a-profile.json`](../../tests/conformance/a2a-profile.json).
2. Make the repository-owned deterministic SUT selectable by Binding. Run the
   same executor and lifecycle scenarios for JSON-RPC and HTTP+JSON, with each
   result recorded independently in [`profile-matrix.json`](../../tests/conformance/profile-matrix.json).
3. Keep gRPC explicitly `not-implemented` until the Hub has a transport adapter,
   Agent Card advertisement, authentication behavior, and current-spec tests.
4. Treat skipped requirements and the protocol revision mismatch as evidence
   boundaries. No profile is promoted to complete conformance by changing a
   status field alone.

## Current evidence

| Binding | Local TCK result | Product status |
|---|---:|---|
| JSON-RPC + SSE | 67 MUST pass, 25 skipped, 0 failed | accepted with waivers |
| HTTP+JSON + SSE | 73 MUST pass, 14 skipped, 0 failed | verified local, opt-in |
| gRPC | not run by the owned SUT | not implemented |

The HTTP+JSON SUT uses the SDK REST handler and a test-only wrapper for
post-executor subscription replay and empty-list array encoding. This wrapper
is part of the deterministic fixture, not a claim that every provider SDK has
identical subscription semantics.

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
