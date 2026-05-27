# Graph Construction WAL / Checkpoint Contract

This document defines the Phase 28 contract for resumable graph construction.
The problem is specific to graph building: every chunk can trigger slow LLM
extraction, so treating a build as "run every chunk, then write graph once" is
not acceptable for long service jobs. The worker must persist chunk-level
progress before and after each expensive extraction.

Go still owns the service job envelope, artifact metadata, cancellation, and
worker process orchestration. Python still owns chunking, LLM extraction,
schema-aware parsing, graph compaction, optional community indexing, and cache
generation.

Phase 30 extends this boundary from a single resumable Python worker into a
Go-scheduled multi-runner pool. That process-level concurrency contract is
defined in `docs/contracts/graph_extraction_multi_runner.md`.

Phase 34 splits community / level-4 compaction into a replay-only stage with
its own WAL. That contract is defined in
`docs/contracts/graph_community_compaction.md`.

## Goals

- append an operation record before each expensive chunk extraction starts;
- append a terminal operation record immediately after each chunk succeeds or
  fails;
- resume from existing successful chunk records without calling the LLM again;
- make interrupted `started` chunks safe to retry;
- compact successful chunk extraction records into final graph/chunks/cache
  artifacts only after the chunk phase is complete;
- allow community compaction to be retried from the extraction WAL without
  rerunning chunk LLM extraction;
- keep WAL files useful for debugging after failure or cancellation.

## Job Spec Fields

`build_graph` accepts these WAL-related fields in addition to the existing
corpus/schema/output fields:

```json
{
  "type": "build_graph",
  "build_graph": {
    "dataset": "anony_chs",
    "corpus_path": "/abs/path/data/anony_chs/final_chunk_corpus.json",
    "schema_path": "/abs/path/schemas/anony_chs.json",
    "graph_output_path": "/abs/path/output/graphs/anony_chs_new.json",
    "chunks_output_path": "/abs/path/output/chunks/anony_chs.txt",
    "cache_dir": "/abs/path/retriever/faiss_cache_new/anony_chs",
    "wal_path": "/abs/path/output/graph_wal/anony_chs.jsonl",
    "resume": true,
    "max_workers": 5
  }
}
```

Stable fields:

- `wal_path`: append-only JSONL file for chunk-level graph construction
  records. Default should be
  `$YOUTU_RAG_ARTIFACT_ROOT/output/graph_wal/{dataset}.jsonl`.
- `resume`: when true, the worker reads `wal_path` before extraction and skips
  chunks with valid `chunk_succeeded` records. Default should be true for
  service jobs once the WAL worker is available.
- `max_workers`: upper bound for concurrent chunk extraction. The worker must
  still serialize WAL appends so records remain valid JSONL.
- `checkpoint_path`: optional compact state summary path. If omitted, the
  first implementation may treat the WAL itself as the checkpoint source of
  truth.

Future additive fields may include `max_failures`, `retry_failed`,
`reset_wal`, `skip_communities`, or `allow_stale_wal`. They should not be
needed for the first WAL implementation.

## WAL Artifact

`build_graph` jobs report a `graph_wal` output artifact:

```json
{
  "name": "graph_wal",
  "role": "output",
  "kind": "graph_construction_wal_jsonl",
  "schema_version": "graph-build-wal/v1",
  "dataset": "anony_chs",
  "path": "/abs/path/output/graph_wal/anony_chs.jsonl",
  "status": "pending"
}
```

Artifact status semantics:

- `pending`: job accepted, WAL has not been touched.
- `running`: worker started and is expected to append WAL rows.
- `written`: WAL parsed successfully and contains a terminal `run_succeeded`
  or `run_canceled` row.
- `failed`: WAL is missing when expected, malformed, stale, or the worker
  failed before the WAL could reach a clean terminal state.

The WAL is durable partial state. Deleting graph/chunks/cache outputs must not
delete the WAL unless the caller explicitly requests a rebuild/reset policy in
a future API.

## Checkpoint Artifact

A checkpoint is an optional compact summary derived from the WAL:

```json
{
  "schema_version": "graph-build-checkpoint/v1",
  "dataset": "anony_chs",
  "wal_path": "/abs/path/output/graph_wal/anony_chs.jsonl",
  "corpus_hash": "sha256:...",
  "schema_hash": "sha256:...",
  "chunking_config_hash": "sha256:...",
  "total_chunks": 2763,
  "succeeded": 120,
  "failed": 2,
  "interrupted": 1,
  "last_sequence": 412,
  "updated_at": "2026-05-19T08:00:00Z"
}
```

The checkpoint is a cache, not the source of truth. If it disagrees with the
WAL, the worker must trust the WAL or fail with `graph_checkpoint_stale`.
Workers may skip a separate checkpoint file in the first implementation if WAL
replay is fast enough.

## WAL Row Envelope

Every WAL row is one JSON object per line:

```json
{
  "schema_version": "graph-build-wal/v1",
  "run_id": "job_...",
  "dataset": "anony_chs",
  "event": "chunk_succeeded",
  "time": "2026-05-19T08:00:00Z",
  "sequence": 42,
  "chunk_key": "sha256:...",
  "chunk_id": "0FCIUkTr",
  "chunk_index": 12,
  "attempt": 1,
  "payload": {}
}
```

Required envelope fields:

- `schema_version`: current value `graph-build-wal/v1`.
- `run_id`: service job id or worker-generated run id.
- `dataset`: dataset name.
- `event`: one of the stable event names below.
- `time`: RFC3339 timestamp.
- `sequence`: monotonically increasing integer inside one WAL file. On resume,
  the worker may continue from the highest observed sequence.

Chunk-scoped rows require:

- `chunk_key`: stable idempotency key.
- `chunk_id`: chunk id used in output chunks/graph properties when available.
- `chunk_index`: stable index after corpus chunking.
- `attempt`: retry attempt for this chunk key.

`payload` is event-specific and may hold extraction output, error details,
latency, token usage, or final artifact paths.

## Stable Events

Run-level events:

- `run_started`: worker accepted the build. Payload should include corpus hash,
  schema hash, construction mode, chunking config hash, total chunk count when
  known, model metadata when available, and output paths.
- `run_resumed`: worker read an existing WAL and is resuming from it. Payload
  should include counts of reusable successes, interrupted chunks, and failed
  chunks.
- `run_compacting`: worker is turning terminal chunk records into graph/chunks
  outputs.
- `run_succeeded`: final graph/chunks/cache artifacts have been validated and
  atomically made visible.
- `run_failed`: build cannot continue or compaction failed.
- `run_canceled`: cancellation was observed between chunks; completed chunk
  rows remain reusable.

Chunk-level events:

- `chunk_started`: written before the worker calls the LLM or equivalent
  expensive extractor.
- `chunk_succeeded`: written immediately after a chunk extraction was parsed and
  normalized. This is the terminal reusable record for the chunk.
- `chunk_failed`: written after a chunk attempt fails. It is terminal for that
  attempt, but not necessarily terminal for the chunk if retry budget remains.
- `chunk_skipped`: optional resume diagnostic showing that an existing
  `chunk_succeeded` row was reused instead of re-executing extraction.

The user-facing shorthand `started/succeeded/failed/compacted` maps to
`chunk_started`, `chunk_succeeded`, `chunk_failed`, and `run_compacting` /
`run_succeeded`.

## Chunk Idempotency

The worker must not rely on random chunk ids for resume. The stable
idempotency key is:

```text
chunk_key = sha256(
  dataset + corpus_record_id_or_path + chunk_index + text_hash +
  chunking_config_hash + schema_hash + construction_mode
)
```

Rules:

- `text_hash` is computed from normalized chunk text.
- `schema_hash` is the canonical managed schema hash used by the job.
- `chunking_config_hash` changes when chunk size, overlap, or splitter behavior
  changes.
- If corpus/schema/chunking/mode changes, old chunk successes are stale and
  must not be reused unless a future explicit `allow_stale_wal=true` option is
  added.
- `chunk_id` may remain the existing Youtu-RAG chunk id, but replay decisions
  must use `chunk_key` plus hashes.

Current worker behavior:

- each manifest-aware run writes `graph-build-input-manifest/v1` in run-event
  payloads and in `chunk_succeeded.payload.manifest`;
- the manifest covers dataset, construction mode, corpus sha256, schema
  sha256, chunking settings, total chunk count, and ordered chunk hashes;
- `--resume` scans existing manifest-bearing WAL rows before replay and fails
  fast with `graph_build_wal_stale` when the manifest changed;
- legacy WALs without manifests remain readable for already-built artifacts,
  but new production WALs should always carry the manifest.

The current implementation still uses `doc_index:chunk_index:text_hash` inside
`chunk_key`. This is safe for correctness when paired with the manifest check,
but it is not optimal for future upsert: inserting a document near the front of
the corpus can shift ordinals and reduce reuse. A future upsert-capable key
should use a stable document id plus per-document chunk ordinal, or a
content-addressed chunk id with duplicate-occurrence disambiguation.

## `chunk_succeeded` Payload

The success payload is the Python-authoritative raw extraction unit from which
compaction can rebuild the graph without another LLM call.

Minimum fields:

```json
{
  "chunk_text_hash": "sha256:...",
  "attributes": {
    "Lionel Messi": ["date of birth"]
  },
  "triples": [
    ["Lionel Messi", "signed_by", "Barcelona"]
  ],
  "entity_types": {
    "Lionel Messi": "person"
  },
  "new_schema_types": {
    "nodes": [],
    "relations": [],
    "attributes": []
  },
  "raw_response": "{...}",
  "latency_ms": 12345,
  "token_usage": {
    "prompt_tokens": 0,
    "completion_tokens": 0
  }
}
```

`new_schema_types` is required for agent/schema-evolution mode. If a worker does
not support schema evolution, it should write empty arrays rather than omit the
field.

## Resume Algorithm

Before starting extraction, the worker reads the WAL sequentially and builds a
state table keyed by `chunk_key`.

State rules:

- Latest valid `chunk_succeeded`: reusable. Do not call the LLM again for that
  chunk.
- Latest `chunk_started` without later `chunk_succeeded` or `chunk_failed`:
  interrupted attempt. Retry the chunk by appending a new `chunk_started` with
  `attempt + 1`.
- Latest `chunk_failed`: retry only if retry policy permits it. First
  implementation may retry failed chunks once or fail fast; the chosen policy
  must be persisted in `run_started`.
- Malformed WAL row: fail fast with `graph_wal_invalid`; do not silently ignore
  rows.
- Stale corpus/schema/chunking/mode hash: fail fast with `graph_wal_stale`.

Resume never rewrites old WAL rows. It only appends new run/chunk rows.

## Base Compaction and Community Compaction

Compaction is the only phase that writes final graph/chunks/cache outputs.
The durable WAL boundary is chunk extraction, not optional post-processing.
The original Python graph builder may run community indexing through
`process_level4()`, which can load additional embedding models. That stage is
useful for full graph quality, but it must not invalidate completed chunk
extractions.

Rules:

- Use successful chunk payloads from the current reusable WAL state.
- Deterministically apply the same normalization/deduplication rules used by
  the graph builder.
- Treat community indexing / level-4 construction as a separate compaction
  substage with its own `graph-compaction-wal/v1` when it is enabled. See
  `docs/contracts/graph_community_compaction.md`.
- Write graph/chunks/cache to temporary paths first, then atomically rename
  into `graph_output_path`, `chunks_output_path`, and `cache_dir` when possible.
- Append `run_compacting` before writing final outputs.
- Append `run_succeeded` only after output validation passes.
- If compaction fails, append `run_failed` and leave the WAL for inspection and
  retry.
- If optional community indexing fails or hangs, the worker may fail compaction
  with `graph_compaction_failed`, or skip community indexing when explicitly
  configured. In both cases, existing `chunk_succeeded` rows remain reusable and
  resume should not call the LLM again for those chunks.

The final graph/chunks files are derived artifacts. The WAL is the durable log
for expensive chunk extraction.

When community compaction is run later as a replay-only stage, it must consume
the extraction WAL as input and must not append new `chunk_started`,
`chunk_succeeded`, or `chunk_failed` rows to that extraction WAL.

In multi-runner mode, the WAL is also the handoff between runner processes and
the final compactor. Only scheduler-accepted runner results may become
`chunk_succeeded` rows.

## Cancellation

Go service cancellation should terminate the worker process when necessary, but
the Python worker should also check for cancellation between chunks when it can.

Required behavior:

- already-written `chunk_succeeded` rows remain valid;
- a chunk with only `chunk_started` is considered interrupted and retried on
  resume;
- best effort workers append `run_canceled` before exit;
- canceled jobs must not mark graph/chunks as `written` unless compaction
  already finished successfully.

## Error Semantics

Stable error strings:

- `graph_wal_invalid`: WAL JSONL cannot be parsed or required fields are
  missing.
- `graph_build_wal_stale`: WAL hashes do not match the current
  corpus/schema/chunking/mode inputs.
- `graph_wal_write_failed`: the worker cannot append or flush a WAL row.
- `graph_checkpoint_stale`: checkpoint metadata does not match the WAL or input
  hashes.
- `graph_chunk_failed`: a chunk extraction failed.
- `graph_failure_budget_exceeded`: failed chunks exceed the configured budget.
- `graph_compaction_failed`: final graph/chunks/cache generation failed.
- `graph_output_missing`: worker exited successfully but graph/chunks output is
  missing.

Errors should appear in the job `error` field and, when the worker can report
them, in stderr or structured stdout. API keys and raw secrets must never appear
in WAL rows, events, stdout/stderr, or job records.

## Job Events

The Go service should expose coarse WAL progress through job events without
embedding every extraction payload in the job record.

Stable event names:

- `graph_wal_started`
- `graph_wal_resumed`
- `graph_chunk_started`
- `graph_chunk_succeeded`
- `graph_chunk_failed`
- `graph_compaction_started`
- `artifact_graph_written`
- `graph_wal_failed`

Event metadata should include counts when available:

```json
{
  "total_chunks": 2763,
  "started": 20,
  "succeeded": 18,
  "failed": 1,
  "skipped": 41,
  "current_chunk_key": "sha256:...",
  "wal_path": "/abs/path/output/graph_wal/anony_chs.jsonl"
}
```

## Acceptance Criteria

Phase 28 gates should verify:

- `build_graph` persists resolved `wal_path`, `resume`, and `max_workers` in
  `job.spec`.
- worker command receives `--wal`, `--resume` when enabled, and
  `--max-workers` when set.
- `graph_wal` artifact moves `pending -> running -> written` on success.
- successful chunk rows survive restart and resume does not re-execute those
  chunks.
- interrupted `chunk_started` rows are retried.
- failed chunk rows are visible and follow retry/failure-budget semantics.
- malformed WAL fails fast with `graph_wal_invalid`.
- stale WAL fails fast with `graph_build_wal_stale`.
- compaction writes graph/chunks only after successful chunk processing.
- cancellation leaves a resumable WAL and does not mark graph/chunks written.
- existing `build_graph`, `create_dataset`, `rebuild`, benchmark, release, and
  service-smoke gates do not regress.
