# Workflow Contract

Workflows sit above service jobs. A job runs one unit of work, such as
`parse_documents`, `build_graph`, or `answer`. A workflow coordinates multiple
jobs and service operations into a product
operation with explicit step status, child job references, artifact handoff,
failure semantics, cancellation, and restart behavior.

The first workflow milestone should not replace the existing job API. It should
compose existing job types.

## Envelope

Every workflow response uses `workflow/v1`.

```json
{
  "schema_version": "workflow/v1",
  "id": "wf_...",
  "type": "build_and_answer",
  "status": "queued",
  "spec": {},
  "steps": [],
  "artifacts": [],
  "created_at": "2026-05-17T00:00:00Z",
  "started_at": "2026-05-17T00:00:01Z",
  "finished_at": "2026-05-17T00:00:02Z",
  "error": ""
}
```

Stable fields:

- `schema_version`: current value is `workflow/v1`.
- `id`: service-assigned workflow id.
- `type`: workflow type.
- `status`: `queued`, `running`, `succeeded`, `failed`, or `canceled`.
- `spec`: typed workflow request.
- `steps`: ordered step records.
- `artifacts`: workflow-level artifact summary.
- `error`: stable error string when the workflow fails.

## Step Record

Each workflow step points at the child job that performed the work.

```json
{
  "name": "build_graph",
  "job_id": "job_...",
  "type": "build_graph",
  "status": "succeeded",
  "input_artifacts": [],
  "output_artifacts": [],
  "started_at": "2026-05-17T00:00:01Z",
  "finished_at": "2026-05-17T00:00:30Z",
  "error": ""
}
```

Stable fields:

- `name`: step name inside the workflow.
- `job_id`: child service job id.
- `type`: child job type.
- `status`: current child step status.
- `input_artifacts`: artifact metadata passed into this step.
- `output_artifacts`: artifact metadata produced by this step.
- `error`: child step error when failed.

## Implemented Workflow: build_and_answer

The first workflow should chain:

```text
build_graph -> answer
```

Submit:

```json
{
  "type": "build_and_answer",
  "build_and_answer": {
    "dataset": "demo",
    "question": "Who signed with Barcelona?",
    "corpus_path": "/abs/path/data/demo/demo_corpus.json",
    "schema_path": "/abs/path/schemas/demo.json",
    "graph_output_path": "/abs/path/output/graphs/demo_new.json",
    "chunks_output_path": "/abs/path/output/chunks/demo.txt",
    "cache_dir": "/abs/path/retriever/faiss_cache_new/demo",
    "answer_output_path": "/abs/path/output/answers/demo.json",
    "answer_mode": "noagent",
    "top_k": 20
  }
}
```

Required minimal fields:

- `dataset`
- `question`

The service can resolve artifact paths from the dataset registry when optional
paths are omitted.

## Implemented Workflow: create_dataset

`create_dataset` turns raw documents plus a schema into a registered,
graph-built dataset.

It chains:

```text
parse_documents -> dataset_import -> build_graph
```

Submit:

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

Required minimal fields:

- `dataset`
- `document_paths`
- `schema_path`

The service can resolve corpus, graph, chunks, and cache output paths from the
configured artifact roots when optional paths are omitted.

The detailed `create_dataset` workflow contract is defined in
`docs/contracts/create_dataset_workflow.md`.

## Artifact Handoff

The workflow must record how artifacts move between steps.

For `build_and_answer`:

- `build_graph.graph` output becomes `answer.graph` input.
- `build_graph.chunks` output becomes `answer.chunks` input.
- `build_graph.cache` output is retained as workflow artifact metadata for
  sidecar/cache diagnostics.
- `answer.answer` output becomes the final workflow answer artifact.

For `create_dataset`:

- request `document_paths` become `parse_documents.document_*` inputs.
- `parse_documents.corpus` output becomes `dataset_import.corpus_path`.
- request `schema_path` becomes `dataset_import.schema_path`.
- `dataset_import.corpus` and `dataset_import.schema` become
  `build_graph.corpus_path` and `build_graph.schema_path`.
- `build_graph.graph`, `build_graph.chunks`, and `build_graph.cache` become
  final workflow output artifacts.

The handoff is part of the contract. Tests should not need to infer it from
filesystem paths alone.

## API Surface

Add these endpoints:

- `POST /v1/workflows`
- `GET /v1/workflows`
- `GET /v1/workflows/{workflow_id}`
- `GET /v1/workflows/{workflow_id}/steps`
- `GET /v1/workflows/{workflow_id}/steps/{step_name}`
- `GET /v1/workflows/{workflow_id}/events`
- `POST /v1/workflows/{workflow_id}/cancel`

The existing job endpoints remain supported.

### Workflow Listing

`GET /v1/workflows` returns a stable list envelope ordered newest first:

```json
{
  "schema_version": "workflow-list/v1",
  "count": 1,
  "workflows": [
    {
      "schema_version": "workflow/v1",
      "id": "wf_...",
      "type": "build_and_answer",
      "status": "succeeded"
    }
  ]
}
```

The list intentionally returns the same `workflow/v1` snapshots as
`GET /v1/workflows/{workflow_id}`. Clients do not need a second summary shape
for the first service milestone.

### Step Inspection

`GET /v1/workflows/{workflow_id}/steps` returns:

```json
{
  "schema_version": "workflow-steps/v1",
  "workflow_id": "wf_...",
  "count": 2,
  "steps": []
}
```

`GET /v1/workflows/{workflow_id}/steps/{step_name}` returns:

```json
{
  "schema_version": "workflow-step/v1",
  "workflow_id": "wf_...",
  "step": {
    "name": "build_graph",
    "job_id": "job_...",
    "type": "build_graph",
    "status": "succeeded"
  }
}
```

Unknown workflow ids return `workflow_not_found`. Unknown step names for a
known workflow return `workflow_step_not_found`.

## Persistence

Workflow records should persist under a configurable root, likely
`YOUTU_RAG_WORKFLOW_ROOT`, defaulting to
`$YOUTU_RAG_ARTIFACT_ROOT/output/workflows`.

The persisted file should use `workflow-record/v1`:

```json
{
  "schema_version": "workflow-record/v1",
  "workflow": {
    "schema_version": "workflow/v1"
  },
  "events": []
}
```

On startup:

- completed workflows load as-is;
- queued/running workflows are marked `failed` with an `interrupted` event
  until a durable resume executor exists.

## Dataset Operation History

Dataset-impacting workflows such as `create_dataset` should write
`dataset-operation/v1` records once Phase 21 operation history is enabled. The
operation record should include the workflow reference, sanitized request,
terminal status, and workflow artifact summary.

Detailed operation history contract:
`docs/contracts/dataset_operations.md`.

## Failure Semantics

For the first version:

- If `build_graph` fails, stop and do not submit `answer`.
- If `answer` fails, mark workflow failed but preserve build artifacts.
- If `parse_documents` fails in `create_dataset`, stop and do not import or
  build.
- If `dataset_import` fails in `create_dataset`, stop and do not submit
  `build_graph`.
- If `build_graph` fails in `create_dataset`, preserve imported
  corpus/schema/metadata artifacts.
- If a child job fails, copy a useful error string to the workflow `error`.
- If the workflow is canceled while a child job is running, cancel the child
  job and mark workflow canceled.
- If cancellation happens between steps, do not submit the next step.

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

Events should be enough for clients to show progress without scraping child
job internals.

## Testing Lanes

Workflow validation should cover:

- API contract:
  - `POST /v1/workflows` returns `workflow/v1`.
  - `GET /v1/workflows`, `/{id}`, `/steps`, `/steps/{name}`, and `/events`
    are stable.
- artifact contract:
  - graph/chunks/cache from build step appear as workflow artifacts.
  - graph/chunks are passed into answer step.
  - answer output is the final workflow output artifact.
  - source documents and schema appear in `create_dataset` artifacts.
  - parsed corpus is handed to dataset import.
  - imported corpus/schema/metadata are handed to build graph.
- lifecycle:
  - success path runs both child jobs in order.
  - build failure prevents answer submission.
  - answer failure preserves build artifacts and fails the workflow.
  - `create_dataset` success runs parse/import/build in order.
  - parse failure prevents import and build.
  - import failure prevents build.
  - build failure preserves imported corpus/schema/metadata.
  - cancellation propagates to running child job.
  - restart readback works for completed workflows.
  - stale running workflow becomes failed/interrupted on restart.
- worker validation:
  - existing `parse_documents`, dataset import, `build_graph`, and `answer`
    gates continue to pass.
- smoke:
  - existing `make test` and demo gate scripts continue to pass.

## Non-Goals For Phase 14

- No durable distributed queue yet.
- No automatic resume of interrupted running workflows yet.
- No UI/WebSocket implementation yet.
- No migration of LLM decomposition or answer generation into Go.
- No replacement of Python document parsing internals.
