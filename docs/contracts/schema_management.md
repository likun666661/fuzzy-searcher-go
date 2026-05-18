# Schema Management Contract

This document defines the Phase 23 schema management boundary for the
long-running Youtu-RAG Go service.

The original Python `youtu-graphrag` repository treats schema JSON as a file
that is passed into construction/retrieval paths. The Go service should make
that file a managed dataset artifact: clients can read, validate, upload, and
version the schema through stable service APIs, while Python workers continue
to consume the resolved schema path.

## Schema Shape

The service-managed schema format is the existing Youtu-RAG JSON shape:

```json
{
  "Nodes": ["person", "organization"],
  "Relations": ["created_by", "located_in"],
  "Attributes": ["name", "date"]
}
```

Validation rules:

- top-level JSON must be an object;
- `Nodes`, `Relations`, and `Attributes` are required;
- each field must be a non-empty array of non-empty strings;
- values should be trimmed before validation;
- duplicates should be rejected or normalized consistently; first
  implementation should reject duplicates and return `duplicate_schema_item`;
- unknown top-level fields may be preserved, but must not be required by Go;
- schema upload must not mutate existing graph/chunks/cache outputs.

The field names are intentionally capitalized to match the Python repo and its
existing `schemas/*.json` files.

## Managed Paths

For dataset `<dataset>`, the managed schema path is:

```text
$YOUTU_RAG_SCHEMA_ROOT/<dataset>.json
```

The default fallback schema path is:

```text
$YOUTU_RAG_SCHEMA_ROOT/default.json
```

If no dataset-specific schema exists, read/validation endpoints may report the
default fallback when configured. Build/import/create workflows must record
which path was actually used.

Schema metadata should be written under the dataset metadata root:

```text
$YOUTU_RAG_DATASET_META_ROOT/<dataset>.schema.json
```

Metadata file shape:

```json
{
  "schema_version": "dataset-schema-metadata/v1",
  "dataset": "news_2026",
  "version": 3,
  "hash": "sha256:...",
  "path": ".../schemas/news_2026.json",
  "fallback": false,
  "updated_at": "2026-05-18T00:00:00Z",
  "source": {
    "kind": "upload",
    "path": "/abs/path/incoming/schema.json"
  },
  "summary": {
    "nodes": 10,
    "relations": 12,
    "attributes": 8
  }
}
```

`hash` is computed from the canonical persisted JSON bytes. `version`
increments on every successful `PUT`; if no previous metadata exists, version
starts at `1`.

## API Surface

### Get Schema

```http
GET /v1/datasets/{dataset}/schema
```

Response:

```json
{
  "schema_version": "dataset-schema/v1",
  "dataset": "news_2026",
  "status": "ready",
  "path": ".../schemas/news_2026.json",
  "fallback": false,
  "version": 3,
  "hash": "sha256:...",
  "updated_at": "2026-05-18T00:00:00Z",
  "summary": {
    "nodes": 10,
    "relations": 12,
    "attributes": 8
  },
  "schema": {
    "Nodes": [],
    "Relations": [],
    "Attributes": []
  }
}
```

Status values:

- `ready`: dataset-specific schema exists and validates.
- `fallback`: dataset-specific schema is missing but default schema exists and
  validates.
- `missing`: no dataset-specific schema and no valid default fallback exists.
- `invalid`: schema file exists but fails validation.

Query options:

- `include_body`: optional boolean, defaults to `true`. When false, omit the
  `schema` body and return only metadata/summary.
- `allow_fallback`: optional boolean, defaults to `true`. When false, missing
  dataset-specific schema returns `schema_not_found` even if `default.json`
  exists.

### Upload or Replace Schema

```http
PUT /v1/datasets/{dataset}/schema
content-type: application/json
```

Request:

```json
{
  "schema": {
    "Nodes": ["person"],
    "Relations": ["created_by"],
    "Attributes": ["name"]
  },
  "overwrite": true,
  "source_path": "/abs/path/incoming/schema.json"
}
```

Fields:

- `schema`: required schema object using the shape above.
- `overwrite`: optional; defaults to `false`. When false and a dataset-specific
  schema exists, return `schema_exists`.
- `source_path`: optional sanitized provenance. The service should not read
  this path for JSON body uploads; it is metadata only.

Response:

```json
{
  "schema_version": "dataset-schema-update/v1",
  "dataset": "news_2026",
  "status": "written",
  "path": ".../schemas/news_2026.json",
  "version": 4,
  "hash": "sha256:...",
  "artifacts": [
    {
      "name": "schema",
      "role": "input",
      "kind": "schema_json",
      "schema_version": "dataset-schema/v1",
      "dataset": "news_2026",
      "path": ".../schemas/news_2026.json",
      "status": "written"
    },
    {
      "name": "schema_metadata",
      "role": "metadata",
      "kind": "dataset_schema_metadata_json",
      "schema_version": "dataset-schema-metadata/v1",
      "dataset": "news_2026",
      "path": ".../output/datasets/news_2026.schema.json",
      "status": "written"
    }
  ]
}
```

`PUT` must be atomic from the client's perspective: bad schema input must not
partially overwrite the existing managed schema or metadata.

### Validate Schema

```http
POST /v1/schemas/validate
content-type: application/json
```

Request:

```json
{
  "schema": {
    "Nodes": ["person"],
    "Relations": ["created_by"],
    "Attributes": ["name"]
  }
}
```

Response:

```json
{
  "schema_version": "schema-validation/v1",
  "valid": true,
  "summary": {
    "nodes": 1,
    "relations": 1,
    "attributes": 1
  },
  "errors": []
}
```

This endpoint validates schema shape only. It does not write dataset artifacts.

### List Schema Status

Dataset list and artifact endpoints should expose schema status without forcing
clients to call the schema endpoint for every dataset:

```json
{
  "name": "schema",
  "role": "input",
  "kind": "schema_json",
  "schema_version": "dataset-schema/v1",
  "dataset": "news_2026",
  "path": ".../schemas/news_2026.json",
  "status": "ready",
  "hash": "sha256:...",
  "version": 3
}
```

Allowed artifact statuses:

- `ready`: dataset schema exists and validates.
- `fallback`: default schema is being used.
- `missing`: no usable schema.
- `invalid`: schema file exists but fails validation.
- `written`: upload just wrote the schema.
- `failed`: write or validation failed.

## Default Fallback Semantics

Default fallback exists to keep local/demo flows simple, not to hide production
configuration mistakes.

Rules:

- `GET /v1/datasets/{dataset}/schema` may return fallback by default.
- `build_graph`, `create_dataset`, and `rebuild` should prefer dataset-specific
  schema; if they use fallback, they must record `fallback=true` in job/workflow
  spec or artifact metadata.
- `production` profile may disable fallback in a future config; until then,
  tests should verify fallback is observable.
- `PUT /v1/datasets/{dataset}/schema` always writes the dataset-specific schema
  and stops using fallback for that dataset.

## Integration Boundaries

### Dataset Import

`POST /v1/datasets/import` may continue to accept `schema_path` for prepared
imports. It should validate the imported schema with the same rules as
`PUT /v1/datasets/{dataset}/schema`, copy it to the managed schema path, and
write schema metadata. Bad schema must fail import before corpus/schema
metadata is committed.

### create_dataset Workflow

`create_dataset` currently accepts `schema_path`. That path is still valid as a
source schema. The `dataset_import` step should become the point where the
schema is validated and registered. Workflow artifacts should distinguish:

- `schema_source`: source schema path from the request.
- `schema`: managed dataset schema path.
- `schema_metadata`: managed schema metadata path.

### build_graph Job

`build_graph` workers should receive the resolved managed schema path. They
should not search for arbitrary schema files. If fallback is used, the job spec
and artifacts must record that the schema came from fallback.

### rebuild Operation

`POST /v1/datasets/{dataset}/rebuild` requires a usable schema. It must fail
with `schema_not_found` or `schema_invalid` when neither dataset-specific nor
allowed fallback schema is valid. Rebuild must not modify schema files.

## Operation History

Schema writes should create a `dataset-operation/v1` record:

```json
{
  "type": "schema_update",
  "dataset": "news_2026",
  "status": "succeeded",
  "artifacts": []
}
```

Phase 23 may add `schema_update` to operation history as part of implementation
or leave it as the first follow-up if #94 stays scoped to schema API only.
When implemented, failed schema uploads should also leave `failed` records with
sanitized request metadata and validation errors.

## Error Semantics

Stable error codes:

- `invalid_dataset`: unsafe dataset name.
- `invalid_json`: malformed request body or schema JSON.
- `invalid_schema`: schema object fails shape validation.
- `duplicate_schema_item`: duplicate value in `Nodes`, `Relations`, or
  `Attributes`.
- `schema_exists`: dataset schema exists and `overwrite=false`.
- `schema_not_found`: no dataset-specific schema and fallback is disabled or
  unavailable.
- `schema_invalid`: schema file exists but fails validation.
- `schema_write_failed`: service could not persist schema or metadata.

Recommended HTTP mapping:

- invalid dataset/body/schema: `400`
- not found: `404`
- exists conflict: `409`
- write failure: `500`

## Acceptance Criteria

Phase 23 validation should verify:

- `GET /v1/datasets/{dataset}/schema` returns managed schema metadata and body.
- `PUT /v1/datasets/{dataset}/schema` writes schema and metadata atomically.
- `POST /v1/schemas/validate` validates without writing files.
- bad schema input does not overwrite existing schema.
- duplicate/empty/missing schema fields return stable errors.
- default fallback is observable and can be disabled by request.
- dataset/artifact registry exposes schema status, version, and hash.
- import/create/build/rebuild resolve managed schema paths and do not regress.
- restart readback preserves schema metadata.
- existing dataset lifecycle, operation history, job, workflow, and retriever
  gates do not regress.
