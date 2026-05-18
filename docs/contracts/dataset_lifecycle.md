# Dataset Lifecycle Contract

This document defines the Phase 19 dataset lifecycle boundary for managed
datasets. Phase 16 introduced dataset import, and Phase 18 introduced
`create_dataset`. Phase 19 adds deletion/cleanup semantics and documents the
safe rebuild shape.

The goal is to make cleanup predictable. Go owns dataset identity, managed
artifact paths, API contract, and metadata. Python workers continue to own
document parsing, graph construction, and model-heavy processing.

## Managed Dataset Definition

A dataset is considered service-managed when it has a metadata file at:

```text
$YOUTU_RAG_DATASET_META_ROOT/<dataset>.json
```

The metadata file is written by `POST /v1/datasets/import` or by workflows that
call the same import path, such as `create_dataset`.

Dataset delete MUST default to managed datasets only. This prevents accidental
removal of hand-created demo or research artifacts that the service can see
through filesystem scanning but did not create.

## Managed Artifact Paths

For dataset `<dataset>`, the service may delete these managed artifacts:

| Artifact | Path |
| --- | --- |
| corpus | `$YOUTU_RAG_CORPUS_ROOT/uploaded/<dataset>/corpus.json` |
| uploaded corpus dir | `$YOUTU_RAG_CORPUS_ROOT/uploaded/<dataset>` when empty |
| schema | `$YOUTU_RAG_SCHEMA_ROOT/<dataset>.json` |
| graph | `$YOUTU_RAG_GRAPH_ROOT/<dataset>_new.json` |
| chunks | `$YOUTU_RAG_CHUNKS_ROOT/<dataset>.txt` |
| cache | `$YOUTU_RAG_CACHE_ROOT/<dataset>` |
| golden | `$YOUTU_RAG_GOLDEN_ROOT/<dataset>.json` |
| triple trace | `$YOUTU_RAG_TRACE_ROOT/<dataset>_triple_trace.json` |
| answers | `$YOUTU_RAG_ARTIFACT_ROOT/output/answers/<dataset>.json` |
| dataset metadata | `$YOUTU_RAG_DATASET_META_ROOT/<dataset>.json` |

The delete API MUST NOT recursively delete arbitrary parent directories such as
`$YOUTU_RAG_CORPUS_ROOT`, `$YOUTU_RAG_GRAPH_ROOT`, or
`$YOUTU_RAG_ARTIFACT_ROOT`.

## Delete Endpoint

```http
DELETE /v1/datasets/{dataset}
```

Optional query parameters:

- `include_outputs`: defaults to `true`. When true, delete graph, chunks,
  cache, golden, trace, and answer outputs in addition to corpus/schema/meta.
- `dry_run`: defaults to `false`. When true, return the planned deletion set
  without deleting files.
- `force`: defaults to `false`. When false, deletion is allowed only when the
  dataset metadata file exists. When true, the service may delete known managed
  paths even if metadata is missing.

Response:

```json
{
  "schema_version": "dataset-delete/v1",
  "dataset": "news_2026",
  "status": "deleted",
  "dry_run": false,
  "include_outputs": true,
  "deleted_at": "2026-05-17T00:00:00Z",
  "artifacts": [
    {
      "name": "corpus",
      "role": "output",
      "kind": "corpus_json",
      "dataset": "news_2026",
      "path": ".../data/uploaded/news_2026/corpus.json",
      "status": "deleted"
    }
  ],
  "errors": []
}
```

Artifact status values:

- `deleted`: file or directory existed and was removed.
- `missing`: artifact was in the managed deletion set but did not exist.
- `skipped`: artifact was not included by query options, or `dry_run=true`
  reported an existing artifact without deleting it.
- `failed`: deletion was attempted and failed; the response status should be
  `failed`.

The operation should be idempotent when `force=true`: deleting an already
missing managed path should report `missing`, not fail.

## Error Semantics

Stable error codes:

- `invalid_dataset`: unsafe dataset name.
- `dataset_not_managed`: metadata file is missing and `force=false`.
- `dataset_delete_failed`: one or more managed artifacts could not be deleted.

Recommended HTTP mapping:

- invalid dataset name: `400`
- not managed: `404` or `409`; use one consistently in implementation/tests
- partial delete failure: `500` with per-artifact `failed` entries

## Running Job / Workflow Boundary

The first implementation does not need a distributed lock, but it must make
the boundary explicit:

- deletion SHOULD reject or clearly fail if a known running job/workflow for
  the same dataset exists;
- if running-operation detection is not implemented yet, document it as a
  non-goal and keep the delete operation scoped to managed paths only;
- tests should verify that delete does not mutate unrelated dataset artifacts.

## Rebuild Endpoint

Phase 20 promotes rebuild from a future shape to a stable operation:

```http
POST /v1/datasets/{dataset}/rebuild
content-type: application/json
```

Request:

```json
{
  "dry_run": false,
  "overwrite_outputs": true,
  "build_graph": {
    "mode": "noagent",
    "config_path": "/abs/path/config/base_config.yaml",
    "graph_output_path": "/abs/path/youtu-graphrag/output/graphs/news_2026_new.json",
    "chunks_output_path": "/abs/path/youtu-graphrag/output/chunks/news_2026.txt",
    "cache_dir": "/abs/path/youtu-graphrag/retriever/faiss_cache_new/news_2026"
  }
}
```

Fields:

- `dry_run`: defaults to `false`. When true, validate the managed dataset and
  report cleanup/build plan without deleting outputs or submitting a job.
- `overwrite_outputs`: defaults to `true`. When true, graph/chunks/cache
  outputs may be removed before the build job starts. When false, existing
  graph/chunks/cache outputs cause a conflict.
- `build_graph`: optional build graph overrides. Supported fields mirror the
  `build_graph` job spec where useful: `mode`, `config_path`,
  `graph_output_path`, `chunks_output_path`, `cache_dir`, `python_bin`,
  `script_path`, and `working_dir`.

Required preconditions:

- require managed corpus and schema to exist;
- require the schema to pass `docs/contracts/schema_management.md` validation,
  or require an explicitly allowed default fallback;
- require dataset metadata to exist unless a future explicit `force` option is
  added;
- resolve corpus/schema from managed artifact paths, not from caller-supplied
  arbitrary source paths.

Response for a real rebuild:

```json
{
  "schema_version": "dataset-rebuild/v1",
  "dataset": "news_2026",
  "status": "submitted",
  "dry_run": false,
  "overwrite_outputs": true,
  "job_id": "job_...",
  "job_type": "build_graph",
  "artifacts": [
    {
      "name": "corpus",
      "role": "input",
      "kind": "corpus_json",
      "dataset": "news_2026",
      "path": ".../data/uploaded/news_2026/corpus.json",
      "status": "configured"
    },
    {
      "name": "graph",
      "role": "output",
      "kind": "graph_json",
      "schema_version": "youtu-graph/v1",
      "dataset": "news_2026",
      "path": ".../output/graphs/news_2026_new.json",
      "status": "pending"
    }
  ]
}
```

Response for `dry_run=true`:

```json
{
  "schema_version": "dataset-rebuild/v1",
  "dataset": "news_2026",
  "status": "planned",
  "dry_run": true,
  "overwrite_outputs": true,
  "job_id": "",
  "job_type": "build_graph",
  "artifacts": []
}
```

Artifact status values:

- `configured`: managed corpus/schema input exists and will be passed to
  `build_graph`.
- `pending`: graph/chunks/cache output is expected from the submitted job.
- `deleted`: stale output existed and was removed before job submission.
- `missing`: output was absent during cleanup planning.
- `skipped`: output cleanup was skipped because `dry_run=true`.
- `conflict`: output exists and `overwrite_outputs=false`.
- `failed`: cleanup or validation failed.

The rebuild endpoint returns after submitting the `build_graph` job. Clients
then inspect the job through existing `/v1/jobs/{job_id}` and
`/v1/jobs/{job_id}/events` APIs.

## Rebuild Cleanup Scope

Rebuild cleanup is narrower than dataset delete. It may touch only derived
build outputs:

- graph: `$YOUTU_RAG_GRAPH_ROOT/<dataset>_new.json`
- chunks: `$YOUTU_RAG_CHUNKS_ROOT/<dataset>.txt`
- cache: `$YOUTU_RAG_CACHE_ROOT/<dataset>`

It MUST NOT delete:

- managed corpus;
- managed schema;
- dataset metadata;
- golden fixtures;
- triple traces;
- answers.

Those broader cleanup operations remain under the dataset delete endpoint.

## Rebuild Error Semantics

Stable error codes:

- `invalid_dataset`: unsafe dataset name.
- `dataset_not_managed`: metadata file is missing.
- `dataset_not_ready`: managed corpus or schema is missing.
- `dataset_rebuild_conflict`: graph/chunks/cache output exists and
  `overwrite_outputs=false`.
- `dataset_rebuild_failed`: cleanup or job submission failed.

Recommended HTTP mapping:

- invalid dataset name: `400`
- not managed or missing corpus/schema: `404` or `409`; use one consistently in
  implementation/tests
- output conflict: `409`
- cleanup/submission failure: `500`

## Operation History

Dataset delete and rebuild operations should write `dataset-operation/v1`
records once Phase 21 operation history is enabled. The operation record should
capture the sanitized request, artifact statuses, and build job reference when
rebuild submits a `build_graph` job.

Detailed operation history contract:
`docs/contracts/dataset_operations.md`.

## Acceptance Criteria

Phase 19 validation should verify:

- `DELETE /v1/datasets/{dataset}` returns `dataset-delete/v1`.
- managed dataset deletion removes corpus/schema/metadata and, by default,
  graph/chunks/cache/golden/trace/answer outputs.
- `dry_run=true` reports the same managed deletion set without removing files.
- non-managed dataset deletion fails unless `force=true`.
- deleting one dataset does not remove another dataset's artifacts.
- missing managed artifacts are reported as `missing`, not generic failure.
- registry no longer lists the deleted dataset after metadata/corpus/schema are
  removed.
- `POST /v1/datasets/{dataset}/rebuild` returns `dataset-rebuild/v1`.
- rebuild requires a managed dataset with corpus and schema.
- `dry_run=true` returns a plan and does not delete outputs or create a job.
- `overwrite_outputs=false` rejects existing graph/chunks/cache outputs.
- `overwrite_outputs=true` clears graph/chunks/cache only, preserving
  corpus/schema/metadata/golden/trace/answer artifacts.
- real rebuild submits a `build_graph` job and returns its `job_id`.
- existing import, create_dataset, build_graph, answer, workflow, and demo
  gates do not regress.
