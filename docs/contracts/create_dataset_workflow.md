# create_dataset Workflow Contract

This document defines the Phase 18 `create_dataset` workflow boundary.

The workflow turns raw source documents plus a schema into a registered,
graph-built dataset that downstream `retrieve`, `answer`, and
`generate_golden` jobs can use. Go owns workflow orchestration, step records,
artifact handoff, persistence, cancellation, and restart behavior. Python
continues to own document parsing and graph construction internals through the
existing workers.

## Workflow Request

```http
POST /v1/workflows
content-type: application/json
```

```json
{
  "type": "create_dataset",
  "create_dataset": {
    "dataset": "news_2026",
    "document_paths": [
      "/abs/path/incoming/a.pdf",
      "/abs/path/incoming/b.md"
    ],
    "schema_path": "/abs/path/incoming/schema.json",
    "overwrite": false
  }
}
```

Minimum stable fields:

- `dataset`: target dataset id.
- `document_paths`: one or more raw source document paths.
- `schema_path`: source schema JSON path.

Recommended optional fields:

- `overwrite`: defaults to `false`; passed to dataset import.
- `parse_output_path`: staged corpus JSON path written by `parse_documents`.
  Defaults to a workflow-owned staging path or the managed corpus path.
- `graph_output_path`: final graph JSON path. Defaults to
  `$YOUTU_RAG_GRAPH_ROOT/<dataset>_new.json`.
- `chunks_output_path`: final chunks text path. Defaults to
  `$YOUTU_RAG_CHUNKS_ROOT/<dataset>.txt`.
- `cache_dir`: final vector cache directory. Defaults to
  `$YOUTU_RAG_CACHE_ROOT/<dataset>`.
- `parse_mode`: parser mode passed to `parse_documents`.
- `build_mode`: graph construction mode passed to `build_graph`.
- `config_path`: shared Python config passed to worker jobs.

The persisted `workflow.spec` must contain resolved paths actually used by the
workflow, so a completed record is auditable after restart.

## Step Order

`create_dataset` is a three-step workflow:

```text
parse_documents -> dataset_import -> build_graph
```

Step types:

- `parse_documents`: child `service-job/v1` job of type `parse_documents`.
- `dataset_import`: synchronous service operation using the dataset import
  contract. It has no child job id in the first implementation.
- `build_graph`: child `service-job/v1` job of type `build_graph`.

The `workflow.steps` array must record these steps in order. For the
`dataset_import` step, `job_id` is omitted and `type` is `dataset_import`.

## Artifact Handoff

The workflow must make artifact movement explicit; tests should not need to
infer handoff from paths alone.

Initial inputs:

- `document_1`, `document_2`, ...: raw source documents, `kind=source_document`.
- `schema_source`: source schema JSON, `kind=schema_json`.

Parse output:

- `parsed_corpus`: corpus JSON written by the `parse_documents` job,
  `kind=corpus_json`, `schema_version=corpus-json/v1`.

Dataset import outputs:

- `corpus`: managed corpus JSON copied or written under the corpus root.
- `schema`: managed schema JSON copied under the schema root.
- `dataset_metadata`: metadata JSON written under the dataset metadata root,
  `schema_version=dataset-import/v1`.

Build graph outputs:

- `graph`: graph JSON, `schema_version=youtu-graph/v1`.
- `chunks`: chunk text file.
- `cache`: vector cache directory.

Handoff rules:

- `document_paths` become `parse_documents.document_*` input artifacts.
- `parse_documents.corpus` becomes `dataset_import.corpus_path`.
- request `schema_path` becomes `dataset_import.schema_path`.
- `dataset_import.corpus` becomes `build_graph.corpus_path`.
- `dataset_import.schema` becomes `build_graph.schema_path`.
- `build_graph.graph`, `build_graph.chunks`, and `build_graph.cache` become
  final workflow output artifacts.

## Workflow Result

Successful workflows return a small inline result:

```json
{
  "schema_version": "create-dataset-result/v1",
  "dataset": "news_2026",
  "parse_documents_job_id": "job_...",
  "build_graph_job_id": "job_...",
  "corpus_path": "/abs/path/youtu-graphrag/data/uploaded/news_2026/corpus.json",
  "schema_path": "/abs/path/youtu-graphrag/schemas/news_2026.json",
  "metadata_path": "/abs/path/youtu-graphrag/output/datasets/news_2026.json",
  "graph_output_path": "/abs/path/youtu-graphrag/output/graphs/news_2026_new.json",
  "chunks_output_path": "/abs/path/youtu-graphrag/output/chunks/news_2026.txt",
  "cache_dir": "/abs/path/youtu-graphrag/retriever/faiss_cache_new/news_2026"
}
```

The corpus, schema, metadata, graph, chunks, and cache contents are not embedded
in the workflow result; they are tracked as workflow artifacts.

## Failure Semantics

For the first implementation:

- If `parse_documents` fails, stop and do not run `dataset_import` or
  `build_graph`.
- If `dataset_import` fails, stop and do not submit `build_graph`.
- If `build_graph` fails, mark workflow failed but preserve imported
  corpus/schema/metadata artifacts.
- Child job errors must be copied into the workflow `error` string.
- The failing step must include the child job error or import error in
  `step.error`.

Artifact status rules:

- artifacts start as `configured` for known inputs and `pending` for expected
  outputs;
- successful step outputs move to `written`;
- missing or invalid expected outputs move to `missing` or `failed`, matching
  the underlying job/import contract;
- successful earlier step artifacts stay `written` even if a later step fails.

## Cancellation Semantics

- If cancellation is requested while `parse_documents` is running, cancel that
  child job and mark workflow canceled.
- If cancellation is requested while `build_graph` is running, cancel that
  child job and mark workflow canceled.
- If cancellation is requested between steps, do not submit the next step.
- `dataset_import` is synchronous and should be treated as best-effort
  cancellation: if it has not started, skip it; if it already completed, keep
  its artifacts and do not start `build_graph`.

## Restart Semantics

The first implementation follows the existing workflow manager behavior:

- completed workflows load as-is from `workflow-record/v1`;
- queued/running workflows reloaded after service restart are marked `failed`
  with an `interrupted` event;
- no automatic resume is required yet.

Future resume support must use the recorded step refs and artifact statuses to
decide whether to restart from `parse_documents`, `dataset_import`, or
`build_graph`.

## Events

Expected events:

- `queued`
- `running`
- `step_started`
- `step_succeeded`
- `step_failed`
- `artifact_handoff`
- `dataset_imported`
- `succeeded`, `failed`, `canceled`, or `interrupted`

Events should be sufficient for a UI to show that the workflow is parsing
documents, importing the dataset, or building the graph without scraping child
job internals.

## Acceptance Criteria

Phase 18 validation should verify:

- `POST /v1/workflows` accepts `type=create_dataset`.
- response envelope is `workflow/v1`.
- persisted `workflow.spec` contains resolved document, schema, corpus, graph,
  chunks, and cache paths.
- success records three ordered steps: `parse_documents`, `dataset_import`,
  and `build_graph`.
- parse output is handed to dataset import.
- imported corpus/schema are handed to build graph.
- workflow artifacts include source documents, source schema, parsed corpus,
  managed corpus/schema/metadata, graph, chunks, and cache.
- parse failure prevents import and build.
- import failure prevents build while preserving parse artifacts.
- build failure preserves imported corpus/schema/metadata.
- cancellation propagates to running parse/build child jobs.
- completed workflow survives service restart.
- stale running workflow is marked interrupted on restart.
- existing parse_documents, dataset import, build_graph, build_and_answer, and
  demo gates do not regress.
