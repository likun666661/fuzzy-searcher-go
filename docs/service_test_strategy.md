# Youtu-RAG Go Service Test Strategy

This document defines the testing track for the long-running Go service. The
goal is not to force every Python model capability into Go. The goal is to keep
the Go service boundary stable while Python remains the sidecar/worker layer for
model-heavy work.

## Current Boundary

The Go service owns:

- HTTP API and response envelopes.
- Dataset and artifact registry.
- Retrieve orchestration and mode selection.
- Async job lifecycle and persisted job records.
- Sidecar health/capability checks.
- Golden parity gates for retriever behavior.

Python still owns:

- Embedding and FAISS execution.
- Rerank scoring.
- Path2 model-backed retrieval primitive.
- Future graph construction, decomposition, and answer generation workers unless
  a later phase intentionally replaces one primitive.

Tests should protect this boundary instead of testing Python internals through
Go unit tests.

## Test Lanes

### 1. Service Contract Tests

These tests exercise HTTP handlers as clients see them.

Required coverage:

- `/healthz`, `/readyz`, `/v1/version`.
- `docs/openapi.yaml` parses successfully and includes every public route from
  `internal/svc.Service.Routes`.
- Service profile validation via `service-config-check/v1`, including missing
  artifact, worker, and sidecar diagnostics.
- `/v1/retrieve` validation, supported modes, unsupported modes.
- `/v1/datasets`, `/v1/datasets/{dataset}`,
  `/v1/datasets/{dataset}/artifacts`.
- schema management APIs: `GET/PUT /v1/datasets/{dataset}/schema` and
  `POST /v1/schemas/validate`.
- `/v1/sidecars` and `/v1/sidecars/vector/health`.
- `/v1/jobs`, `/v1/jobs/{job_id}`, `/v1/jobs/{job_id}/events`,
  `/v1/jobs/{job_id}/cancel`.

Gate rule: client-correctable errors should return 4xx with a stable error
envelope; missing runtime dependencies should be explicit and must not look like
random 500s.

Profile/startup validation rules are documented in
`docs/contracts/service_profile.md`.

### 2. Retriever Parity Gates

These are the existing demo golden gates and must remain the hard regression
guard for fuzzy search behavior.

Required gates:

- `runtime-trace`: baseline bridge.
- `primitive-merge`: path1/path2 primitive regression path.
- `rerank-merge`: rerank-only regression path.
- `native-path1-rerank`: current main path.

Each mode must pass:

- `loader`
- `chunk`
- `triple`
- `full`

Gate rule: for demo golden, current green modes must stay at zero diff unless a
human explicitly approves a golden update.

### 3. Sidecar Boundary Tests

These tests use fake sidecars and endpoint logs to make sure Go is calling the
intended primitive, not silently falling back.

Required assertions:

- `native-path1-rerank` must not call `/v1/retrieval/path1-triples`.
- Mode-specific debug strategy must match the expected implementation path.
- Rerank modes must expose input/output counts in debug metadata.
- Sidecar unconfigured/unreachable cases return clear service status.

Gate rule: every new sidecar primitive must have at least one test that fails if
the old broader primitive is called instead.

### 4. Async Job Tests

Jobs are the main shape for long-running service workflows.

Required coverage:

- Submit returns a job id quickly.
- Job status transitions are monotonic and visible.
- Event stream records lifecycle milestones.
- Cancel is idempotent and visible.
- File-backed records survive service restart.
- Stale running jobs are marked `interrupted` on load.
- `build_graph` WAL/resume gates cover chunk-level `started`, `succeeded`, and
  `failed` rows, interrupted chunk retry, malformed/stale WAL failure, and final
  compaction before graph/chunks become `written`.
- Multi-runner graph extraction gates cover lease uniqueness, runner crash,
  lease expiry, late result rejection, resume without duplicate LLM calls, and
  final compaction consuming only scheduler-accepted successes.
- Paper-aligned benchmark gates cover the original Python GraphQ + KTRetriever
  + Eval path, checkpoint/resume, invalid judge output, missing graph/chunks/
  schema artifacts, and explicit deviation metadata for WAL-built graphs that
  skip community compaction. This contract is documented in
  `docs/contracts/paper_aligned_benchmark.md`.

Gate rule: job APIs should be stable before attaching graph construction,
golden generation, answer generation, or other long-running workers.

### 5. Artifact and Dataset Tests

The service must never depend on a hidden sibling checkout without reporting it.

Required coverage:

- Missing graph/chunks/cache artifacts are reported by `/readyz`.
- Clean-clone demo gates fail fast with explicit `GRAPH`, `CHUNKS`, and `GOLDEN`
  override instructions.
- Dataset registry reports corpus/schema/graph/chunks/cache/golden/trace
  readiness independently.
- Job output artifacts are written under configured roots, not ad hoc temp
  paths, once workers are attached.

Gate rule: a missing artifact should produce an actionable diagnostic, not a
panic or opaque retrieve failure.

## Phase Acceptance Matrix

| Phase | Primary owner | Test owner | Must pass |
| --- | --- | --- | --- |
| Retrieve/service refactor | Production owner | Test owner | `go test ./...`, service contract tests, demo gates |
| New sidecar primitive | Python/Go owners | Test owner | endpoint no-fallback test, debug strategy check, demo gates |
| Async job extension | Go owner | Test owner | job API tests, persistence/restart tests, error envelope tests |
| New artifact workflow | Go/Python owners | Test owner | artifact registry tests, readyz tests, clean-clone smoke |
| Golden update | Human-approved | Test owner | old/new diff report, documented reason, regenerated fixture schema check |

## CI Shape

Short gate:

```bash
make test
python3 -m json.tool docs/contracts/retrieve_result.schema.json >/dev/null
sh -n scripts/run_demo_gates.sh
```

Sidecar-backed gate:

```bash
SIDECAR_URL=http://127.0.0.1:8765 \
GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json \
CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt \
GOLDEN=/abs/path/youtu-graphrag/output/retrieval_golden/demo.json \
scripts/run_demo_gates.sh
```

Real service demo gate:

```bash
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765 \
make demo-service-smoke
```

This gate uses real demo graph/chunks/cache and the Python vector sidecar, but
does not require an LLM key. The contract is documented in
`docs/contracts/real_demo_smoke.md`.

Service smoke gate:

```bash
make release-check
make service-smoke
```

## Review Checklist

For every service phase:

- Does the change keep `internal/svc` thin?
- Is the behavior reachable through HTTP, not only CLI?
- Are request/response fields documented or covered by schema?
- Is sidecar fallback impossible or explicitly tested?
- Is failure observable through status/error JSON?
- Does `make test` pass?
- Do existing demo gates remain green?
