# Dataset Import Contract

This document defines the first dataset lifecycle API for the Go service.

The service does not parse arbitrary uploaded documents yet. Phase 16 starts
with a smaller, stable boundary: import an already prepared corpus JSON and
schema JSON into service-managed artifact roots, persist metadata, and make the
dataset visible to the existing artifact registry and `build_graph` workflow.

## Endpoint

```http
POST /v1/datasets/import
content-type: application/json
```

Request:

```json
{
  "dataset": "news_2026",
  "corpus_path": "/abs/path/incoming/corpus.json",
  "schema_path": "/abs/path/incoming/schema.json",
  "overwrite": false
}
```

Fields:

- `dataset`: required. Must match `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`.
- `corpus_path`: required absolute or service-local path to a valid JSON file.
- `schema_path`: required absolute or service-local path to a valid JSON file.
- `overwrite`: optional. Defaults to `false`; when false, existing managed
  corpus/schema/metadata artifacts cause a conflict.

Response:

```json
{
  "schema_version": "dataset-import/v1",
  "dataset": "news_2026",
  "status": "imported",
  "imported_at": "2026-05-17T00:00:00Z",
  "source": {
    "corpus_path": "/abs/path/incoming/corpus.json",
    "schema_path": "/abs/path/incoming/schema.json"
  },
  "artifacts": [
    {
      "name": "corpus",
      "role": "input",
      "kind": "corpus_json",
      "dataset": "news_2026",
      "path": ".../data/uploaded/news_2026/corpus.json",
      "status": "written"
    },
    {
      "name": "schema",
      "role": "input",
      "kind": "schema_json",
      "dataset": "news_2026",
      "path": ".../schemas/news_2026.json",
      "status": "written"
    },
    {
      "name": "metadata",
      "role": "output",
      "kind": "dataset_metadata_json",
      "schema_version": "dataset-import/v1",
      "dataset": "news_2026",
      "path": ".../output/datasets/news_2026.json",
      "status": "written"
    }
  ]
}
```

## Managed Paths

The service copies source files into:

- corpus: `$YOUTU_RAG_CORPUS_ROOT/uploaded/<dataset>/corpus.json`
- schema: `$YOUTU_RAG_SCHEMA_ROOT/<dataset>.json`
- metadata: `$YOUTU_RAG_DATASET_META_ROOT/<dataset>.json`

`YOUTU_RAG_DATASET_META_ROOT` defaults to
`$YOUTU_RAG_ARTIFACT_ROOT/output/datasets`.

## Registry Integration

After import, existing dataset endpoints discover the dataset:

```bash
curl -s http://127.0.0.1:8080/v1/datasets
curl -s http://127.0.0.1:8080/v1/datasets/news_2026/artifacts
```

The imported dataset is expected to be `schema_ready`: corpus and schema exist,
while graph/chunks/cache are still missing until `build_graph` runs.

The existing `build_graph` job and `build_and_answer` workflow resolve the
imported corpus/schema paths from the artifact registry. No Python graph
construction logic changes are required.

## Errors

Stable error codes:

- `invalid_json`: malformed request body.
- `invalid_dataset`: unsafe or unsupported dataset name.
- `dataset_exists`: managed corpus/schema/metadata already exists and
  `overwrite` is false.
- `dataset_import_failed`: source file cannot be read or corpus/schema is not
  valid JSON.

## Non-Goals

- No multipart upload yet.
- No PDF/DOCX/TXT parsing yet.
- No schema editor yet.
- No automatic graph build. Import only prepares corpus/schema artifacts; graph
  construction remains a separate job/workflow step.
- No dataset deletion in the import endpoint. Managed dataset cleanup is defined
  separately in `docs/contracts/dataset_lifecycle.md`.
