# Dataset Operation History Contract

This document defines the Phase 21 dataset operation history boundary.

Dataset APIs and workflows now cover import, create, rebuild, and delete. The
service also needs a durable operations log so clients and operators can answer:
who changed a dataset, when, which job/workflow was involved, and which
artifacts were written, deleted, skipped, or failed.

Go owns this operation history. Python workers remain behind job/workflow
contracts and do not write operation records directly.

## Operation Record

Each operation record uses `dataset-operation/v1`.

```json
{
  "schema_version": "dataset-operation/v1",
  "id": "op_...",
  "dataset": "news_2026",
  "type": "rebuild",
  "status": "succeeded",
  "created_at": "2026-05-17T00:00:00Z",
  "finished_at": "2026-05-17T00:00:30Z",
  "request": {},
  "job_refs": [
    {
      "name": "build_graph",
      "job_id": "job_...",
      "type": "build_graph"
    }
  ],
  "workflow_refs": [],
  "artifacts": [],
  "error": ""
}
```

Stable fields:

- `schema_version`: current value is `dataset-operation/v1`.
- `id`: service-assigned operation id.
- `dataset`: dataset name.
- `type`: one of `import`, `create_dataset`, `rebuild`, `delete`, or
  `schema_update`.
- `status`: `planned`, `running`, `succeeded`, `failed`, or `canceled`.
- `created_at`: operation record creation time.
- `finished_at`: terminal time when known.
- `request`: sanitized request summary. It must not contain secrets.
- `job_refs`: child job references involved in the operation.
- `workflow_refs`: workflow references involved in the operation.
- `artifacts`: artifact summary copied from job/workflow/API responses.
- `error`: stable error text when the operation fails.

## References

Job refs:

```json
{
  "name": "build_graph",
  "job_id": "job_...",
  "type": "build_graph"
}
```

Workflow refs:

```json
{
  "name": "create_dataset",
  "workflow_id": "wf_...",
  "type": "create_dataset"
}
```

Reference names are stable within an operation and should match workflow step
names where possible.

## Operation Types

### import

Created by `POST /v1/datasets/import`.

Expected refs:

- no job refs in the first implementation;
- no workflow refs.

Artifacts:

- `corpus`
- `schema`
- `metadata`

Status:

- `succeeded` when import returns `dataset-import/v1`;
- `failed` when import validation/copy/metadata write fails.

### create_dataset

Created by `POST /v1/workflows` with `type=create_dataset`.

Expected refs:

- one workflow ref named `create_dataset`;
- job refs can be omitted in the operation record if they are inspectable
  through the workflow steps, or copied from the workflow result once complete.

Artifacts:

- source documents and schema source;
- parsed corpus;
- managed corpus/schema/metadata;
- graph/chunks/cache.

Status:

- follows the workflow terminal state.

### rebuild

Created by `POST /v1/datasets/{dataset}/rebuild`.

Expected refs:

- one job ref named `build_graph` when `dry_run=false` and submission succeeds;
- no job ref for `dry_run=true`.

Artifacts:

- managed corpus/schema inputs;
- graph/chunks/cache cleanup plan and build outputs.

Status:

- `planned` for dry-run;
- `running` or `succeeded` after job submission depending on whether the first
  implementation tracks child job completion synchronously;
- `failed` for validation/conflict/cleanup/submission errors.

### delete

Created by `DELETE /v1/datasets/{dataset}`.

Expected refs:

- no job refs in the first implementation;
- no workflow refs.

Artifacts:

- deletion response artifacts from `dataset-delete/v1`.

Status:

- `succeeded` when status is `deleted`;
- `planned` when `dry_run=true`;
- `failed` when deletion status is `failed` or the API returns an error.

### schema_update

Created by `PUT /v1/datasets/{dataset}/schema` once schema operation history is
enabled.

Expected refs:

- no job refs;
- no workflow refs.

Artifacts:

- `schema`
- `schema_metadata`

Status:

- `succeeded` when schema upload returns `dataset-schema-update/v1`;
- `failed` when schema validation or metadata write fails.

## API Surface

Global listing:

```http
GET /v1/dataset-operations
```

Dataset-scoped listing:

```http
GET /v1/datasets/{dataset}/operations
```

Single operation:

```http
GET /v1/dataset-operations/{operation_id}
```

List response:

```json
{
  "schema_version": "dataset-operation-list/v1",
  "count": 1,
  "operations": [
    {
      "schema_version": "dataset-operation/v1",
      "id": "op_...",
      "dataset": "news_2026",
      "type": "rebuild",
      "status": "succeeded"
    }
  ]
}
```

Ordering:

- newest first by `created_at`;
- tie-break by `id` descending.

Errors:

- unknown operation id returns `dataset_operation_not_found`;
- invalid dataset name returns `invalid_dataset`.

## Persistence

Operation records should persist under:

```text
$YOUTU_RAG_DATASET_OPS_ROOT
```

Default:

```text
$YOUTU_RAG_ARTIFACT_ROOT/output/dataset_operations
```

Persist one JSON file per operation:

```json
{
  "schema_version": "dataset-operation-record/v1",
  "operation": {
    "schema_version": "dataset-operation/v1"
  }
}
```

Startup behavior:

- completed operations load as-is;
- non-terminal operations should be marked `failed` with an `interrupted`
  error unless the implementation can reconcile child job/workflow state.

## Artifact Summary Rules

Operation records do not need to duplicate large artifacts. They should record
artifact metadata only:

- `name`
- `role`
- `kind`
- `schema_version`
- `dataset`
- `path`
- `status`
- `description`

Statuses should be copied from the source API/job/workflow response whenever
possible. For operations that submit a long-running job, initial artifacts may
be `pending` or `configured`; later phases can add reconciliation to update
them after child job completion.

## Non-Goals

- No auth/user identity yet. The first record does not include an actor.
- No distributed event stream yet.
- No automatic replay of interrupted operations.
- No secret capture in `request`.

## Acceptance Criteria

Phase 21 validation should verify:

- import creates an `import` operation with corpus/schema/metadata artifacts.
- create_dataset creates a `create_dataset` operation with a workflow ref.
- rebuild creates a `rebuild` operation with dry-run planned status or
  build_graph job ref.
- delete creates a `delete` operation with deletion artifact statuses.
- failed import/rebuild/delete operations are recorded with `status=failed`.
- operation records survive service restart.
- global and dataset-scoped listing return newest first.
- single-operation lookup works.
- missing operation id returns `dataset_operation_not_found`.
- existing dataset lifecycle, workflow, job, and demo gates do not regress.
