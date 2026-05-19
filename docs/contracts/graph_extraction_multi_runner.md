# Graph Extraction Multi-Runner Contract

This document defines the Phase 30 contract for accelerating graph
construction with multiple Python runner processes. It builds on
`docs/contracts/graph_construction_wal.md`: the WAL remains the durable source
of truth, but extraction is no longer owned by one long Python process.

The target shape is:

```text
Go scheduler
  -> creates chunk tasks and leases
  -> starts N Python graph runners
  -> records task state and WAL rows
  -> runs final compactor after all chunks are terminal

Python graph runner
  -> receives one chunk or a small batch
  -> calls the LLM
  -> returns normalized extraction payload
  -> exits or asks for the next lease
```

This is intentionally process-level concurrency. Do not implement production
graph acceleration by sharing one Python interpreter and multiple threads over
SentenceTransformer, Torch, FAISS, or mutable retriever/global caches.

## Goals

- scale slow per-chunk LLM extraction by running multiple Python runner
  processes;
- keep each chunk extraction as a transaction with a lease and terminal WAL
  row;
- let a crashed runner lose only its current lease, not the whole build;
- avoid duplicate LLM calls for chunks that already have `chunk_succeeded`;
- keep final graph/chunks/cache generation in a separate compaction phase.

## Non-Goals

- no shared Python model object across runners;
- no distributed queue requirement in the first version;
- no direct graph mutation from runner processes;
- no final graph/chunks writes from runner processes.

## Scheduler Ownership

Go owns the scheduler for a multi-runner build:

- enumerate corpus chunks and compute stable `chunk_key`;
- replay the WAL to skip chunks with valid `chunk_succeeded`;
- create `graph-extraction-task/v1` task records for pending chunks;
- grant leases to runner processes;
- enforce `max_runners`, lease timeout, retry budget, and cancellation;
- append run/task lifecycle WAL rows, or ingest runner output and append the
  final `chunk_succeeded`/`chunk_failed` rows;
- run final compaction after all chunks are either succeeded or failed within
  policy.

The first implementation may keep the task queue in memory while also writing
WAL rows. The WAL must remain sufficient to resume after process restart.

## Task Record

Each pending chunk is represented by a task:

```json
{
  "schema_version": "graph-extraction-task/v1",
  "task_id": "chunk_000001",
  "dataset": "anony_chs",
  "chunk_key": "sha256:...",
  "chunk_id": "anony_chs_000001_000_abcdef12",
  "doc_index": 1,
  "chunk_index": 0,
  "chunk_text_hash": "sha256:...",
  "schema_hash": "sha256:...",
  "chunking_config_hash": "sha256:...",
  "mode": "noagent",
  "status": "pending",
  "attempt": 0,
  "lease_id": "",
  "leased_until": ""
}
```

Stable statuses:

- `pending`: ready for lease.
- `leased`: assigned to a runner and not yet terminal.
- `succeeded`: terminal, has a matching `chunk_succeeded` WAL row.
- `failed`: terminal for the current attempt. May return to `pending` if retry
  budget remains.
- `canceled`: terminal because the parent job/workflow was canceled.
- `stale`: cannot be reused because corpus/schema/chunking/mode changed.

`task_id` is service-local. Idempotency and resume decisions use `chunk_key`,
not `task_id`.

## Lease Semantics

A lease gives a runner temporary ownership of a task:

```json
{
  "schema_version": "graph-extraction-lease/v1",
  "lease_id": "lease_...",
  "runner_id": "runner_03",
  "task_id": "chunk_000001",
  "chunk_key": "sha256:...",
  "attempt": 1,
  "leased_at": "2026-05-19T15:10:00Z",
  "leased_until": "2026-05-19T15:20:00Z"
}
```

Rules:

- only one live lease may exist for a `chunk_key`;
- a runner result is accepted only if `lease_id`, `chunk_key`, and `attempt`
  match the scheduler's current lease;
- expired leases are returned to `pending` with `attempt + 1` unless retry
  budget is exhausted;
- late results from expired leases must be rejected as
  `graph_runner_lease_expired`;
- cancellation revokes outstanding leases and prevents new leases.

## WAL Events

Phase 30 reuses `graph-build-wal/v1` and adds scheduler/runner events.

Run-level events:

- `runner_pool_started`: scheduler started a pool. Payload includes
  `max_runners`, `lease_timeout_seconds`, and `retry_budget`.
- `runner_pool_stopped`: scheduler stopped all runners.

Task/lease events:

- `chunk_leased`: scheduler assigned a chunk to a runner.
- `chunk_lease_expired`: scheduler detected a stale lease.
- `chunk_requeued`: scheduler returned a chunk to pending.
- `chunk_succeeded`: accepted extraction result for a current lease.
- `chunk_failed`: accepted runner failure for a current lease.
- `runner_started`: runner process started.
- `runner_exited`: runner process exited, with exit code.

The existing `chunk_started` event may still be used for a single-process
worker. In multi-runner mode, `chunk_leased` is the durable "started" event.

## Runner Command

The first runner integration is command-based:

```bash
${python_bin} ${graph_runner_script} \
  --dataset "${dataset}" \
  --task-json "${task_json_path}" \
  --result-json "${result_json_path}" \
  --schema "${schema_path}" \
  --config "${config_path}" \
  --mode "${mode}"
```

Required inputs:

- `task_json`: a single `graph-extraction-task/v1` task plus lease metadata and
  chunk text.
- `result_json`: path where the runner writes a single
  `graph-extraction-result/v1`.
- `dataset`, `schema`, `config`, and `mode`: same meaning as `build_graph`.

The service may also support stdin/stdout JSON later, but file paths are the
stable first contract because they are easy to debug after crashes.

## Runner Result

A successful runner writes:

```json
{
  "schema_version": "graph-extraction-result/v1",
  "dataset": "anony_chs",
  "runner_id": "runner_03",
  "lease_id": "lease_...",
  "task_id": "chunk_000001",
  "chunk_key": "sha256:...",
  "chunk_id": "anony_chs_000001_000_abcdef12",
  "attempt": 1,
  "status": "succeeded",
  "extraction": {
    "attributes": {},
    "triples": [],
    "entity_types": {},
    "new_schema_types": {
      "nodes": [],
      "relations": [],
      "attributes": []
    }
  },
  "raw_response": "{...}",
  "latency_ms": 12345,
  "token_usage": {
    "prompt_tokens": 0,
    "completion_tokens": 0
  },
  "finished_at": "2026-05-19T15:11:00Z"
}
```

A failed runner may write the same envelope with:

```json
{
  "status": "failed",
  "error": {
    "code": "graph_runner_llm_failed",
    "message": "request timed out",
    "retryable": true
  }
}
```

The scheduler validates the result before appending terminal WAL rows. Invalid
or missing result files are `graph_runner_result_invalid`.

## Final Compaction

Compaction remains a separate stage:

```text
succeeded extraction WAL rows -> deterministic merge/dedupe/schema validation
-> optional community/index/cache stages -> graph/chunks/cache artifacts
```

Rules:

- runners never write final graph/chunks/cache artifacts;
- compactor reads only accepted `chunk_succeeded` rows;
- duplicate successful rows for the same `chunk_key` are a scheduler bug and
  must fail compaction with `graph_wal_duplicate_success`;
- optional community indexing remains a compaction substage and can be skipped
  without invalidating chunk extraction WAL.

## Failure, Retry, and Cancellation

Stable errors:

- `graph_runner_failed`: runner process returned non-zero or reported failure.
- `graph_runner_timeout`: runner exceeded per-task timeout.
- `graph_runner_lease_expired`: result arrived after lease expiry.
- `graph_runner_result_missing`: runner exited without result JSON.
- `graph_runner_result_invalid`: result JSON failed schema or lease validation.
- `graph_runner_retry_exhausted`: retry budget exhausted for a chunk.
- `graph_wal_duplicate_success`: multiple accepted successes for one
  `chunk_key`.

Retry rules:

- retry unit is a chunk task, never the whole graph build;
- successful chunks are never retried;
- failed or expired chunks may be retried until `max_attempts`;
- retry policy must be written in `runner_pool_started`.

Cancellation rules:

- stop granting new leases;
- terminate or let current runners finish according to service policy;
- append `run_canceled`;
- keep successful chunk WAL rows reusable for a future resume.

## Scheduler / Job Spec Fields

Future `build_graph` spec fields for multi-runner mode:

```json
{
  "runner_mode": "multi_process",
  "runner_count": 5,
  "runner_batch_size": 1,
  "runner_lease_timeout_seconds": 900,
  "runner_max_attempts": 3,
  "runner_script_path": "/abs/path/scripts/graph_extraction_runner.py",
  "task_root": "/abs/path/output/graph_tasks/anony_chs"
}
```

Defaults:

- `runner_mode`: `single_process` until multi-runner is explicitly enabled.
- `runner_count`: min of configured count and service concurrency budget.
- `runner_batch_size`: `1` for simplest lease semantics.
- `runner_lease_timeout_seconds`: enough for one LLM chunk extraction plus
  provider tail latency.
- `runner_max_attempts`: `2` or `3`, depending on cost budget.
- `task_root`: `$YOUTU_RAG_ARTIFACT_ROOT/output/graph_tasks/{dataset}`.

## Acceptance Criteria

Phase 30 gates should verify:

- scheduler creates one task per chunk with stable `chunk_key`;
- no two live leases exist for the same `chunk_key`;
- multiple Python runner processes can complete disjoint chunks;
- runner crash or missing result requeues only the leased chunk;
- late result from expired lease is rejected and does not append
  `chunk_succeeded`;
- resume skips chunks already accepted in WAL and does not re-call LLM;
- final compaction consumes accepted successes and writes graph/chunks once;
- cancellation leaves reusable successes and no false `written` graph status;
- existing Phase 28 single-worker WAL path still works unless explicitly
  replaced.
