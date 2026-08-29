# A2A v1 three-process interoperability fixture

> **Evidence status**: locally verified on 2026-08-27; test fixture, not a
> production Hub or production Agent implementation

This fixture establishes the first executable boundary selected by
[ADR 0001](../../docs/adr/0001-a2a-v1-jsonrpc-sse-profile.md). It consists of
three independent processes:

1. `cmd/interop-hub`: a Go Hub-side probe that discovers an Agent Card and uses
   only the advertised A2A interface;
2. `cmd/a2a-go-fixture`: a deterministic Go A2A Agent;
3. `python-agent/agent.py`: a deterministic Python A2A Agent with the same
   externally selectable scenarios.

The Hub probe has no imports from either fixture and no knowledge of the remote
Agent's model, tools, framework, or workflow. This is the first executable check
of the opaque-Agent boundary; it is not yet routing, persistence, policy, or a
user-facing Hub API.

## Pinned dependencies

| Component | Revision |
|---|---|
| A2A protocol | `173695755607e884aa9acf8ce4feed90e32727a1` (`1.0`, normative TCK-aligned baseline) |
| A2A Go SDK | `v2.5.0`, commit `9d95b95445f4208ba77f48a137a278067937adb7` |
| A2A Python SDK | commit `6eee8956fa0e3d6378e4a61b52cf674d05b81229` |
| Binding | JSON-RPC over local HTTP; SSE for streaming |

The Go SDK is pinned in the repository `go.mod`. The Python SDK and all Python
transitive dependencies are pinned in `python-agent/uv.lock`.

## Scenario inputs

The fixtures select deterministic behavior from the first text Part:

| Input | Observable result |
|---|---|
| `message` | Direct Agent `Message`; no Task is persisted |
| `task` or `artifact-text` | Completed Task with a text Artifact |
| `artifact-file` | Completed Task with an inline raw-file Artifact Part |
| `artifact-file-url` | Completed Task with a URL Artifact Part |
| `artifact-data` | Completed Task with a structured-data Artifact Part |
| `input-required` | Task pauses in `TASK_STATE_INPUT_REQUIRED` |
| `long-running` | Task remains working until `CancelTask` |

For continuation after `INPUT_REQUIRED`, send another message with both the
returned `taskId` and `contextId`.

## Run

Prerequisites are Go 1.25, Python 3.13, `uv`, `curl`, and `jq`. Run the complete
smoke check from the repository root:

```bash
GO_BIN=/path/to/go tests/interop/run-smoke.sh
```

When `go` is already on `PATH`, omit `GO_BIN`. The script builds binaries under
a temporary directory, synchronizes the ignored Python virtual environment,
starts the two Agents on `127.0.0.1:4101` and `127.0.0.1:4102`, and invokes a
separate Hub process against each.

For manual inspection, start the processes in separate terminals:

```bash
go run ./cmd/a2a-go-fixture
uv run --project tests/interop/python-agent \
  python tests/interop/python-agent/agent.py
go run ./cmd/interop-hub \
  --agent-card-url http://127.0.0.1:4102 \
  --operation stream \
  --text artifact-data
```

The Hub probe writes newline-delimited JSON. Its operations are `discover`,
`send`, `stream`, `get`, `cancel`, and `subscribe`. `send` and `stream` accept
`--task-id` and `--context-id` for continuation.

## Verified behavior

The 2026-08-27 local run verified the same Go Hub binary against both Agents:

- Agent Card discovery and strict selection of `JSONRPC` protocol version `1.0`;
- direct Message responses;
- SSE Task, status, structured Artifact, and completion event sequences;
- text, inline raw-file, file URL, and structured-data Artifact Parts;
- `INPUT_REQUIRED` followed by continuation to `COMPLETED`;
- immediate return for long-running Tasks, active `SubscribeToTask`,
  cancellation, and subsequent `GetTask` returning `CANCELED`;
- UTC `Z` status timestamps emitted by both repository fixtures.

## Known gaps and differences

- Both task stores are in memory, so process-restart recovery is not tested.
- Authentication, Push, HTTP+JSON, gRPC, AAMP, and multi-tenant isolation are
  outside ADR 0001's first executable slice.
- Subscribing after a Task is already terminal is not uniform in the pinned
  SDKs: Go returns `task not found: no active execution`; Python closes an empty
  stream. The Hub recovery contract must define and normalize this behavior.
- History ordering and retention still need owned cross-SDK contract assertions
  before history is used for audit or recovery.
- Active `SubscribeToTask` through cancellation is smoke-tested. A forced
  disconnect/reconnect replay needs a dedicated multi-client contract test; the
  smoke script does not count terminal subscription as a pass.
- The fixtures show that both SDKs can implement the selected behaviors. They do
  not replace aligned current-spec TCK results.
