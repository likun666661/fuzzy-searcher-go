# Graph Community Compaction Stage Contract

This document defines the Phase 34 contract for running community / level-4
graph compaction as a replay-only stage after chunk extraction has already been
persisted to WAL.

The motivation is that chunk extraction and community compaction have different
failure and cost profiles:

- chunk extraction is expensive per chunk because it calls the LLM;
- community compaction is a global post-processing stage over the extracted
  graph and may load embedding models or call the LLM for community summaries;
- a compaction failure must not make previously succeeded chunks look failed;
- retrying compaction must not re-call chunk extraction.

Go owns the service job envelope, artifact metadata, cancellation, worker
process orchestration, and validation. Python owns replaying Youtu-RAG graph
payloads into the original graph builder structures, community detection,
community node creation, optional cache/index refresh, and output formatting.

## Relationship to Existing WAL

Phase 28/30 already define the durable extraction WAL:

```text
corpus chunks -> graph-build-wal/v1 chunk_succeeded rows
```

Phase 34 adds a second WAL for the global post-processing stage:

```text
graph-build-wal/v1 -> graph-compaction-wal/v1 -> compacted graph/chunks/cache
```

The extraction WAL remains the source of truth for expensive chunk results.
The compaction WAL is the source of truth for replay, community, cache, and
publish progress.

The compaction worker must never append chunk extraction rows and must never
call chunk-level extraction LLM prompts. It may call model APIs only for
community-summary or equivalent compaction substeps, and those calls must have
their own retry/progress records.

## Job Spec Fields

`build_graph` may run this stage either as part of a full build after
extraction, or as a replay-only compaction job over an existing extraction WAL.

Recommended request shape:

```json
{
  "type": "build_graph",
  "build_graph": {
    "dataset": "anony_chs",
    "corpus_path": "/abs/path/data/anony_chs/final_chunk_corpus.json",
    "schema_path": "/abs/path/schemas/anony_chs.json",
    "graph_output_path": "/abs/path/output/graphs/anony_chs_full_communities.json",
    "chunks_output_path": "/abs/path/output/chunks/anony_chs_full_communities.txt",
    "cache_dir": "/abs/path/retriever/faiss_cache_new/anony_chs",
    "wal_path": "/abs/path/output/graph_wal/anony_chs_full_flash_new.jsonl",
    "compaction_wal_path": "/abs/path/output/graph_wal/anony_chs_full_communities.compaction.jsonl",
    "resume": true,
    "replay_only": true,
    "skip_extraction": true,
    "skip_communities": false,
    "community_compaction": true,
    "community_embedding_model": "all-MiniLM-L6-v2",
    "community_timeout_seconds": 3600
  }
}
```

Stable fields:

- `wal_path`: existing `graph-build-wal/v1` extraction WAL. Required for
  replay-only compaction.
- `compaction_wal_path`: append-only `graph-compaction-wal/v1` JSONL path.
  Defaults beside `wal_path`, for example
  `{dataset}.compaction.jsonl`.
- `replay_only` / `skip_extraction`: when true, the worker must not enumerate
  chunk extraction work or call extraction LLM prompts.
- `skip_communities`: when false, run level-4/community compaction. Existing
  WAL-only graph builds used `true`; Phase 34 should explicitly test `false`.
- `community_compaction`: explicit enable flag for the community stage. It is
  separate from generic base graph compaction.
- `community_embedding_model`: model used by the community algorithm when it
  needs sentence embeddings.
- `community_timeout_seconds`: wall-clock budget for the community substage.
- `cache_dir`: optional retrieval cache/index output. Cache refresh is a
  compaction substage, not extraction.
- `resume`: when true, read `compaction_wal_path` and skip already completed
  compaction substages.

Future additive fields may include `community_batch_size`,
`community_rate_limit_rpm`, `community_max_failures`, `cache_rebuild=false`, or
`publish_suffix`.

## Input Preconditions

Before any compaction model work starts, the worker must validate:

- `wal_path` exists and parses as `graph-build-wal/v1`;
- the extraction WAL has a compatible terminal run state or enough
  `chunk_succeeded` rows for the requested corpus subset;
- there are no duplicate accepted successes for one `chunk_key`;
- corpus/schema/chunking/mode hashes match the current request, when those
  hashes are present in the WAL;
- `graph_output_path` and `chunks_output_path` are not the same as an existing
  published artifact unless overwrite policy allows it.

If the extraction WAL is incomplete, the compaction worker should fail with
`graph_compaction_input_incomplete` unless a future explicit partial-build
option is added.

## Compaction WAL Artifact

`build_graph` jobs that run community compaction should report a
`graph_compaction_wal` artifact:

```json
{
  "name": "graph_compaction_wal",
  "role": "output",
  "kind": "graph_compaction_wal_jsonl",
  "schema_version": "graph-compaction-wal/v1",
  "dataset": "anony_chs",
  "path": "/abs/path/output/graph_wal/anony_chs_full_communities.compaction.jsonl",
  "status": "pending"
}
```

Artifact status semantics:

- `pending`: job accepted, compaction WAL has not been touched.
- `running`: worker started and may append compaction rows.
- `written`: compaction WAL parsed successfully and contains
  `run_succeeded` or `run_canceled`.
- `failed`: compaction WAL is missing, malformed, stale, or contains
  `run_failed`.

The extraction WAL and compaction WAL have independent lifetimes. Dataset
delete/rebuild cleanup policies must name them separately.

## WAL Row Envelope

Every compaction WAL row is one JSON object per line:

```json
{
  "schema_version": "graph-compaction-wal/v1",
  "run_id": "job_...",
  "dataset": "anony_chs",
  "event": "community_detection_succeeded",
  "time": "2026-05-24T09:00:00Z",
  "sequence": 12,
  "stage": "community_detection",
  "input_wal_path": "/abs/path/output/graph_wal/anony_chs_full_flash_new.jsonl",
  "input_wal_hash": "sha256:...",
  "payload": {}
}
```

Required envelope fields:

- `schema_version`: current value `graph-compaction-wal/v1`.
- `run_id`: service job id or worker-generated run id.
- `dataset`: dataset name.
- `event`: one of the stable event names below.
- `time`: RFC3339 timestamp.
- `sequence`: monotonically increasing integer inside one compaction WAL file.
- `stage`: coarse stage name, such as `replay`, `base_compaction`,
  `community_detection`, `community_materialization`, `cache_build`, or
  `publish`.
- `input_wal_path`: extraction WAL path used by this run.
- `input_wal_hash`: hash of the extraction WAL state consumed by compaction.

`payload` is event-specific and may contain counts, community ids, output
paths, latency, model metadata, or error details. API keys and raw secrets must
never appear in payloads.

## Stable Events

Run-level events:

- `run_started`: compaction worker accepted the request. Payload should include
  input WAL path/hash, output paths, `skip_communities`, community model
  metadata, and overwrite policy.
- `run_resumed`: worker read an existing compaction WAL and is resuming.
- `run_succeeded`: output graph/chunks/cache artifacts validated and published.
- `run_failed`: compaction cannot continue.
- `run_canceled`: cancellation observed. Completed compaction rows remain
  reusable when safe.

Replay/base stages:

- `extraction_wal_replayed`: extraction WAL replay completed. Payload should
  include total chunk successes, failed chunks, duplicate count, and base graph
  node/edge counts.
- `base_graph_compacted`: deterministic merge/dedupe/schema normalization
  completed before community work.

Community stages:

- `community_detection_started`
- `community_detection_succeeded`
- `community_detection_failed`
- `community_batch_started`
- `community_batch_succeeded`
- `community_batch_failed`

Cache/publish stages:

- `cache_build_started`
- `cache_build_succeeded`
- `cache_build_failed`
- `artifacts_published`

The first implementation may treat all communities as one batch. If batches are
used, each batch must have a stable `community_batch_key`.

## Community Idempotency

Community work is global, but it still needs stable resume keys.

Recommended `community_batch_key`:

```text
sha256(
  dataset + input_wal_hash + community_algorithm_version +
  community_embedding_model + sorted_member_node_ids
)
```

Rules:

- completed community batches with matching keys are reusable on resume;
- if the extraction WAL hash changes, old community rows are stale;
- if the embedding model or algorithm version changes, old community rows are
  stale;
- late or duplicate community batch successes for the same key are a worker
  error and should fail with `graph_compaction_duplicate_community_success`.

## Output Artifacts

Expected artifacts:

- `graph_wal`: input `graph_construction_wal_jsonl`,
  `schema_version=graph-build-wal/v1`, status `configured`.
- `graph_compaction_wal`: output `graph_compaction_wal_jsonl`,
  `schema_version=graph-compaction-wal/v1`.
- `graph`: output `graph_json`, `schema_version=youtu-graph/v1`, status
  `pending -> written`.
- `chunks`: output `chunks_txt`, status `pending -> written`.
- `cache`: optional output `faiss_cache_dir`, status `pending -> written` when
  cache refresh is requested.
- `community_summary`: optional output summary JSON,
  `schema_version=graph-community-summary/v1`.

`graph-community-summary/v1` should include:

```json
{
  "schema_version": "graph-community-summary/v1",
  "dataset": "anony_chs",
  "input_wal_path": "/abs/path/output/graph_wal/anony_chs_full_flash_new.jsonl",
  "input_wal_hash": "sha256:...",
  "community_compaction": true,
  "skip_communities": false,
  "level2_node_count": 0,
  "community_count": 0,
  "graph_node_count_before": 0,
  "graph_node_count_after": 0,
  "graph_edge_count_before": 0,
  "graph_edge_count_after": 0,
  "cache_rebuilt": true,
  "duration_ms": 0
}
```

## Resume Algorithm

On start:

1. Replay and validate the extraction WAL.
2. Replay the compaction WAL when `resume=true`.
3. If a terminal `run_succeeded` row matches the current input WAL hash and
   output paths, validate outputs and return success.
4. Skip completed stages with matching input hashes and stage keys.
5. Re-run only incomplete or failed compaction substages.
6. Publish outputs only after all required substages succeed.

Resume must not append new `chunk_started`, `chunk_succeeded`, or
`chunk_failed` rows to the extraction WAL. A test may verify this by comparing
the extraction WAL line count and set of `chunk_succeeded` keys before and
after compaction.

## Failure Semantics

Stable error strings:

- `graph_compaction_invalid_request`: required fields are missing or invalid.
- `graph_compaction_input_wal_missing`: extraction WAL path does not exist.
- `graph_compaction_input_wal_invalid`: extraction WAL cannot be parsed.
- `graph_compaction_input_incomplete`: extraction WAL does not contain enough
  reusable successful chunks.
- `graph_compaction_input_stale`: extraction WAL hashes do not match request.
- `graph_compaction_duplicate_success`: duplicate chunk successes in the
  extraction WAL.
- `graph_compaction_wal_invalid`: compaction WAL cannot be parsed.
- `graph_compaction_wal_stale`: compaction WAL references a different input
  WAL hash or output path.
- `graph_compaction_replay_failed`: extraction WAL replay into base graph
  failed.
- `graph_community_embedding_failed`: embedding model load or encode failed.
- `graph_community_timeout`: community stage exceeded its timeout.
- `graph_community_generation_failed`: community summary/materialization failed.
- `graph_cache_build_failed`: retrieval cache/index refresh failed.
- `graph_compaction_output_invalid`: graph/chunks/cache validation failed.

Compaction failures should mark the compaction WAL and output graph/chunks as
failed or missing, but must leave successful extraction WAL rows reusable.

## Benchmark Boundary

When comparing paper-aligned benchmarks, record whether graph artifacts were
created with community compaction:

```json
{
  "deviations": {
    "graph_source": "industrial_wal_full_flash",
    "skip_communities": false,
    "community_compaction": "completed",
    "compaction_wal_path": "/abs/path/output/graph_wal/anony_chs_full_communities.compaction.jsonl"
  }
}
```

The recommended comparison is:

1. same QA subset;
2. same answer/judge model;
3. same retrieval config (`mode=agent`, `recall_paths=2`, `top_k_filter=20`);
4. only graph artifact changes:
   - `skip_communities=true`;
   - `skip_communities=false` with successful compaction.

Accuracy, AnonyRAG mapping precision/recall/F1/exact recall, failure count, and
average latency should be reported for both runs.

## Acceptance Criteria

Phase 34 gates should verify:

- compaction-only job can consume an existing extraction WAL and does not call
  chunk extraction;
- extraction WAL line count and set of `chunk_succeeded` keys are unchanged
  after compaction and resume;
- `graph_compaction_wal` artifact moves `pending -> running -> written` on
  success;
- malformed or incomplete extraction WAL fails before community model calls;
- community timeout/failure leaves extraction WAL reusable and allows retry of
  only the compaction stage;
- successful compaction writes graph/chunks/cache or records why cache was
  skipped;
- resume over a successful compaction validates outputs without rerunning
  community work;
- benchmark results record `skip_communities=false` and compaction artifact
  metadata;
- existing graph extraction WAL, multi-runner, paper benchmark, release-check,
  and service-smoke gates do not regress.
