# A2A Conformance Boundary

> **Status**: verified local pin check / unresolved external TCK alignment<br>
> **Updated**: 2026-08-27

`a2a-profile.json` is the machine-readable contract for the currently selected
A2A wire profile and evaluated source revisions. `profile_test.go` prevents the
Go SDK pin or evidence status from drifting silently.

Run the local check with:

```bash
go test ./tests/conformance
```

This check is not the upstream A2A TCK. The evaluated TCK commit is pinned for
traceability, but remains marked `unresolved-revision-skew` because it asserts an
older protocol source and its available Go SUT does not implement all current
scenarios. Repository-owned lifecycle and recovery tests are complementary
evidence, not a substitute for a future protocol-aligned TCK run.

See [the implementation boundary](../../docs/architecture/phase-one-hub-conformance-boundary.md)
and [ADR 0001](../../docs/adr/0001-a2a-v1-jsonrpc-sse-profile.md).
