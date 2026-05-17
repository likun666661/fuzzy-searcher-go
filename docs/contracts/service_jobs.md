# Youtu-RAG Service Job Contract

This document defines the long-running job boundary for the Go service. The
goal is a maintainable service, not a full Go rewrite of every Python model
component.

Go owns the API, job lifecycle, status, persistence, artifact metadata, and
worker orchestration. Python can continue to own model-heavy workers and
sidecars, such as embedding, FAISS, rerank, graph construction, decomposition,
and answer generation.

## Job Envelope

Every job returned by the service uses `service-job/v1`.

```json
{
  "schema_version": "service-job/v1",
  "id": "job_...",
  "type": "retrieve",
  "status": "queued",
  "spec": {},
  "artifacts": [],
  "created_at": "2026-05-17T00:00:00Z",
  "started_at": "2026-05-17T00:00:01Z",
  "finished_at": "2026-05-17T00:00:02Z",
  "error": "",
  "result": {}
}
```

Stable fields:

- `schema_version`: job envelope version. Current value is `service-job/v1`.
- `id`: service-assigned job id.
- `type`: job type. Current implemented type is `retrieve`.
- `status`: one of `queued`, `running`, `succeeded`, `failed`, `canceled`.
- `spec`: typed request payload for the job type.
- `artifacts`: input/output artifact metadata.
- `error`: stable error string when the job fails.
- `result`: inline result for small outputs. Large future outputs should be
  represented by output artifacts instead.

## Events

`GET /v1/jobs/{job_id}/events` returns an event list:

```json
{
  "job_id": "job_...",
  "events": [
    {
      "time": "2026-05-17T00:00:00Z",
      "type": "queued",
      "message": "job accepted",
      "status": "queued"
    }
  ]
}
```

Events are append-only lifecycle breadcrumbs. Tests can use them to locate
which stage failed without depending on implementation internals.

## Artifact Metadata

Artifacts describe the files or inline objects a job consumes or produces.

```json
{
  "name": "retrieve_result",
  "role": "output",
  "kind": "retrieve_result_json",
  "schema_version": "retrieve-result/v1",
  "dataset": "demo",
  "path": "",
  "status": "inline",
  "description": "RetrieveResult is returned inline in the job result field."
}
```

Required fields:

- `name`: stable name inside the job.
- `role`: `input` or `output`.
- `kind`: machine-readable artifact kind.
- `schema_version`: version for structured output artifacts when applicable.
- `dataset`: dataset name when relevant.
- `path`: filesystem path for file artifacts.
- `status`: current artifact state such as `configured`, `inline`, `pending`,
  `written`, or `missing`.

## Implemented Job: retrieve

Submit:

```http
POST /v1/jobs
content-type: application/json
```

```json
{
  "type": "retrieve",
  "retrieve": {
    "dataset": "demo",
    "question": "Who signed with Barcelona?",
    "top_k": 20,
    "mode": "native-path1-rerank",
    "graph_path": "/abs/path/output/graphs/demo_new.json",
    "chunks_path": "/abs/path/output/chunks/demo.txt",
    "sidecar_url": "http://127.0.0.1:8765"
  }
}
```

The service stores this as a typed `RetrieveSpec` in `job.spec`:

```json
{
  "dataset": "demo",
  "question": "Who signed with Barcelona?",
  "top_k": 20,
  "mode": "native-path1-rerank",
  "graph_path": "/abs/path/output/graphs/demo_new.json",
  "chunks_path": "/abs/path/output/chunks/demo.txt",
  "sidecar_url": "http://127.0.0.1:8765"
}
```

Retrieve jobs currently report these artifacts:

- `graph`: input `graph_json`, path from `graph_path` when supplied.
- `chunks`: input `chunks_txt`, path from `chunks_path` when supplied.
- `retrieve_result`: output `retrieve_result_json`,
  `schema_version=retrieve-result/v1`, stored inline in `job.result`.

## Planned Job Types

These types are named now so later work can extend the same contract instead of
inventing a new shape:

- `build_graph`: long-running Python graph construction worker.
- `generate_golden`: fixture/golden generation for regression tests.
- `answer`: retrieval plus decomposition/LLM answer generation.

The first implementation may return `unsupported job type` for these until the
worker is attached. The contract expectation is that each new type adds a typed
`spec` and explicit input/output `artifacts`.

## Persistence

The current service persists one JSON record per job under
`YOUTU_RAG_JOB_ROOT`, defaulting to
`$YOUTU_RAG_ARTIFACT_ROOT/output/jobs`.

The persisted record uses `job-record/v1`:

```json
{
  "schema_version": "job-record/v1",
  "job": {
    "schema_version": "service-job/v1"
  },
  "events": []
}
```

On startup, completed jobs are loaded back into the in-memory manager. Any
previously persisted `queued` or `running` job is marked `failed` with an
`interrupted` event, because the process-local runner that owned it no longer
exists.

## Testing Expectations

Service tests should assert:

- `POST /v1/jobs` returns `service-job/v1`.
- retrieve jobs include stable `spec` and graph/chunks/result artifacts.
- job events include queued/running/job-specific/succeeded or failed events.
- failed jobs keep `error` and useful events.
- file-backed jobs survive manager/service restart.
- stale running jobs are marked interrupted on reload.

These tests protect the service contract while allowing the Python worker layer
to evolve behind sidecar/worker boundaries.
