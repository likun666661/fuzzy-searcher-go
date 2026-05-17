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

## Rebuild Shape

Rebuild is not required as a separate endpoint in Phase 19, but the contract
should leave a stable path:

```http
POST /v1/datasets/{dataset}/rebuild
content-type: application/json
```

Proposed request:

```json
{
  "overwrite_outputs": true,
  "build_graph": {
    "mode": "noagent",
    "config_path": "/abs/path/config/base_config.yaml"
  }
}
```

Recommended behavior:

- require managed corpus and schema to exist;
- delete or overwrite graph/chunks/cache outputs;
- submit a `build_graph` job or future workflow step;
- return a job/workflow reference rather than blocking until build completes.

Until this endpoint exists, rebuild should be performed by submitting
`build_graph` or `create_dataset` with explicit overwrite semantics.

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
- existing import, create_dataset, build_graph, answer, workflow, and demo
  gates do not regress.
