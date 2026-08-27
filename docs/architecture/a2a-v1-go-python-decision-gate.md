# A2A v1 Go/Python decision gate

> **Test date**: 2026-08-28<br>
> **Evidence status**: verified local execution / decision pending<br>
> **Scope**: official A2A SDK/tooling behavior plus the repository-owned JSON-RPC/SSE TCK fixture and Hub contract evidence

## Decision question

Can Go remain the leading candidate for the Federation Hub core while Python is used for provider/runtime adapters, based on current A2A v1 interoperability and conformance evidence?

**Current answer: partially verified, not approved as an ADR.** The official Go and Python v1 SDKs interoperated in both directions across all three standard Bindings in the official ITK smoke suite. The current Go SDK also passed its own test suite. However, the available TCK run did not produce an unexplained-zero-failure result, its pinned specification differs from the current protocol snapshot in some error mappings, and its Go SUT fixture is behind the TCK's current scenario contract. Go therefore remains a provisional candidate rather than a decided core language.

## Tested sources

| Component | Tested revision | Role |
|---|---|---|
| [A2A protocol](../../submodules/a2a/) | `16ba52690519bf55b9388e34d4db356efa88aa51` | Current local protocol and specification snapshot |
| [A2A Go SDK](https://github.com/a2aproject/a2a-go/tree/9d95b95445f4208ba77f48a137a278067937adb7) | `9d95b95445f4208ba77f48a137a278067937adb7` (`2.5.0`) | Go v1 client/server peer and TCK SUT |
| [A2A Python SDK](https://github.com/a2aproject/a2a-python/tree/6eee8956fa0e3d6378e4a61b52cf674d05b81229) | `6eee8956fa0e3d6378e4a61b52cf674d05b81229` | Python v1 client/server peer |
| [A2A ITK](https://github.com/a2aproject/a2a-itk/tree/be48aa9854a062c83c2196fe8940da93f7daab82) | `be48aa9854a062c83c2196fe8940da93f7daab82` | Cross-SDK interoperability |
| [A2A TCK](https://github.com/a2aproject/a2a-tck/tree/5996b79f9cefa6fc390980e383e358a66fb9e49e) | `5996b79f9cefa6fc390980e383e358a66fb9e49e` | Protocol conformance assertions, pinned to A2A `v1.0.0` commit `173695755607e884aa9acf8ce4feed90e32727a1` |
| [A2A Inspector](https://github.com/a2aproject/a2a-inspector/tree/8aa064639af106ff771d60428ef6d460f5454743) | `8aa064639af106ff771d60428ef6d460f5454743` | Basic v1 AgentCard validation |

The local toolchain was Go `1.25.0`, Python `3.13.9`, uv `0.12.3`, and Protobuf compiler `29.3`. All external repositories and toolchains were placed under `/tmp`; no submodule source was edited.

## Results

| Gate | Result | Observed evidence |
|---|---|---|
| Go v1 and Python v1, bidirectional unary calls | pass | ITK traversed Python -> Go and Go -> Python over JSON-RPC, gRPC, and HTTP+JSON |
| Go v1 and Python v1, bidirectional streaming | pass | ITK traversed both directions over all three standard Bindings using streaming |
| Push notification | pass | ITK JSON-RPC push scenario completed and verified notification traces |
| Disconnect, resubscribe, and cancel | pass | ITK JSON-RPC resubscribe scenario completed and canceled the held task |
| Python `0.3` compatibility line | pass | ITK Python `0.3` <-> Python `1.0` scenario passed over JSON-RPC and gRPC; this does not establish every cross-language legacy pairing |
| Go SDK tests | pass | `go test ./...` passed at the tested Go SDK revision |
| Inspector v1 AgentCard check | pass, limited | Live Go SUT AgentCard produced no Inspector validation errors; Inspector validator tests passed `49/49` |
| TCK JSON-RPC MUST tests against the repository-owned SUT | evidence with waivers | Fixed TCK `5996b79f`: `74 passed, 7 failed, 154 skipped, 30 deselected`; report is written by [`tests/conformance/run-tck.sh`](../../tests/conformance/run-tck.sh) |
| TCK gRPC MUST tests | fail / unresolved | After the fixture workaround below: `43 passed, 12 failed, 180 skipped, 30 deselected` |
| TCK HTTP+JSON MUST tests | not run | The Go SDK TCK SUT has an explicit `TODO` instead of serving the REST Binding |

Inspector performs basic structural checks and interactive diagnosis; it is not a substitute for TCK or cross-SDK tests. The ITK result proves interoperability of its traversal behaviors, not complete protocol conformance, authentication, authorization, or Hub durability.

## What the TCK failures mean

The raw failure counts must not be treated as 29 independent Go SDK defects. Three kinds of issue are mixed together.

### Go TCK fixture gaps

The repository-owned SUT now covers deterministic direct Message, Task, text/raw/URL/data Artifact, chunked SSE, `INPUT_REQUIRED`, cancellation, resubscription delay, rejection, and explicit version rejection scenarios. The remaining failures are recorded rather than hidden: `STREAM-SUB-001..004` subscription semantics, `CORE-HIST-002` history-length filtering, and `JSONRPC-SSE-002` malformed-content error mapping still need alignment with the selected protocol contract. The external TCK itself remains pinned to an older embedded protocol commit, so these results are evidence with explicit waivers, not a conformance claim.

The Go SUT also advertises its gRPC interface as `http://localhost:9998`, while the current TCK passes the complete value directly to `grpc.insecure_channel`. The valid gRPC run used a temporary fixture-only change to advertise `localhost:9998`, matching the Go SDK's own compatibility tests. Results from the first DNS-failing run were excluded.

### Specification and TCK revision skew

The TCK is pinned to the A2A `v1.0.0` specification, while the local current protocol source is later. At least these gRPC mappings differ:

| Error | TCK-pinned `v1.0.0` expectation | Current local specification |
|---|---|---|
| `PushNotificationNotSupportedError` | `UNIMPLEMENTED` | `FAILED_PRECONDITION` |
| `UnsupportedOperationError` | `UNIMPLEMENTED` | `FAILED_PRECONDITION` |
| `VersionNotSupportedError` | `UNIMPLEMENTED` | `FAILED_PRECONDITION` |

The tested Go SDK matches the current local gRPC mapping for these errors. A TCK failure based only on the older mapping is therefore not valid evidence against the current target. The compatibility gate must pin protocol, SDK, and TCK revisions that assert the same normative contract.

### Findings that still require product-level tests

- The Go JSON-RPC and gRPC handlers propagate `A2A-Version` into request context but did not reject the TCK's unsupported `99.0` request. The Hub must verify whether a supported-version interceptor is required or whether this is an SDK gap.
- `a2a.NewStatusUpdateEvent` uses `time.Now()` without converting to UTC. In the Asia/Shanghai test environment, JSON-RPC emitted a `+08:00` timestamp and failed the current specification's UTC `Z` requirement. This needs a regression contract test independent of host timezone.
- List authorization is deliberately enforced by the Go in-memory task store. The Hub must install an authenticated principal and test tenant/task isolation; bypassing this failure would invalidate the security boundary.
- Subscription behavior for missing and terminal Tasks, content-type error classification, capability checks, and Push error details need direct contract tests against the current specification rather than inference from the skewed fixture.

## Gate assessment

The current evidence supports continuing with a **Go Hub-core spike plus Python adapters**:

- Go and Python are wire-interoperable in both client/server directions.
- Go exposes client, server, three standard Bindings, streaming, Push, task storage, and work/event queue building blocks.
- The SDK alone does not remove the Hub's responsibility for version enforcement, identity propagation, durable task semantics, error normalization, and conformance.

The language decision remains pending until a repository-owned minimal SUT:

1. pins A2A v1 and one explicit initial Binding Profile;
2. implements direct Message, Task, Artifact, streaming, Push, cancellation, resubscription, authentication, and version rejection fixtures;
3. runs Go Server/Python Client and Python Server/Go Client contract tests;
4. passes applicable current-spec MUST checks with every skip and waiver explained;
5. records build size, startup, concurrency, persistence integration, observability, and developer-effort evidence in an ADR.

The current owned-SUT TCK report is intentionally non-zero. Before promoting the
language decision, align the TCK embedded protocol commit with the selected
source, close or explain each remaining MUST failure, and keep the waiver file
machine-checked.

For the first owned implementation, JSON-RPC plus SSE is the best-evidenced initial profile because ITK covered unary, streaming, Push, and resubscription on that path. HTTP+JSON should follow once the owned SUT can run its TCK surface; gRPC remains a required interoperability profile but should not be the only initial external Binding.

## Related research

- [A2A protocol Bindings](../research/a2a-study/protocol-bindings.md)
- [Wire contract and conformance](../research/a2a-study/wire-contract-and-conformance.md)
- [Versioning and compatibility](../research/a2a-study/versioning-and-compatibility.md)
- [Task delivery and recovery](../research/a2a-study/task-delivery-and-recovery.md)
- [Reliability, errors, and cancellation](../research/a2a-study/reliability-errors-and-cancellation.md)
