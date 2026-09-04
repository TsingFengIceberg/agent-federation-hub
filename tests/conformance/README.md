# A2A Conformance Boundary

> **Status**: verified local A2A v1.0.0/TCK pin alignment; multi-binding coverage remains partial<br>
> **Updated**: 2026-08-27

`a2a-profile.json` is the machine-readable contract for the currently selected
A2A wire profile and evaluated source revisions. `profile_test.go` prevents the
Go SDK pin or evidence status from drifting silently.

Run the local check with:

```bash
go test ./tests/conformance
```

This check is not the upstream A2A TCK. The evaluated TCK commit is pinned for
traceability. The selected normative baseline is the official A2A `v1.0.0`
commit embedded by the pinned TCK; a newer upstream mainline candidate is
recorded separately and cannot silently change the baseline. The
repository-owned SUT can be run against JSON-RPC, HTTP+JSON, or gRPC:

```bash
A2A_TCK_DIR=/path/to/a2a-tck A2A_TCK_TRANSPORT=jsonrpc A2A_TCK_BINDING=jsonrpc \
  tests/conformance/run-tck.sh
A2A_TCK_DIR=/path/to/a2a-tck A2A_TCK_TRANSPORT=http_json A2A_TCK_BINDING=http_json \
  tests/conformance/run-tck.sh
A2A_TCK_DIR=/path/to/a2a-tck A2A_TCK_TRANSPORT=grpc A2A_TCK_BINDING=grpc \
  tests/conformance/run-tck.sh
```

The tested Binding matrix is recorded in [`profile-matrix.json`](profile-matrix.json).
Each run writes both `owned-sut-result.json` and the raw
`compatibility-report.json` under its report directory. The runner invokes
[`verify-tck-result.sh`](verify-tck-result.sh), which checks exact protocol/TCK
pins, explicit Binding selection, transport failures, and that every non-PASS
MUST requirement is enumerated. Skipped and not-tested requirements remain
visible; setting `A2A_TCK_REQUIRE_COMPLETE=1` additionally turns those gaps
into a deliberate gate failure.
Repository-owned lifecycle and recovery tests are complementary evidence, not a
substitute for a future protocol-aligned TCK run.

The repository-owned JSON-RPC fixture also contains a narrow compatibility shim
for the pinned TCK's Push CRUD client, which sends `task_id`; it rewrites only
that Push parameter to the canonical A2A `taskId` field before SDK decoding.
This shim is test-fixture behavior and is not part of the Hub's provider-facing
wire contract.

Run the reproducible three-Binding matrix with a pinned TCK checkout:

```bash
A2A_TCK_DIR=/path/to/a2a-tck tests/conformance/run-matrix.sh
```

To require complete MUST coverage for a selected run (normally a future
profile-upgrade gate):

```bash
A2A_TCK_REQUIRE_COMPLETE=1 \
A2A_TCK_DIR=/path/to/a2a-tck \
A2A_TCK_TRANSPORT=jsonrpc A2A_TCK_BINDING=jsonrpc \
tests/conformance/run-tck.sh
```

`check-pins.sh` verifies the local A2A source and Go SDK pins and, when a TCK
checkout is supplied, its repository commit. The matrix records the normative
protocol commit and the newer mainline candidate separately; set
`A2A_TCK_REQUIRE_PIN=1` when a missing TCK checkout must fail a CI job.

See [the implementation boundary](../../docs/architecture/phase-one-hub-conformance-boundary.md)
and [ADR 0001](../../docs/adr/0001-a2a-v1-jsonrpc-sse-profile.md).
