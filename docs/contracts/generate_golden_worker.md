# generate_golden Worker Contract

This document defines the Phase 11 `generate_golden` job boundary. The Go
service owns the job API, lifecycle, persistence, artifact metadata, and worker
process execution. Python owns the retriever runtime and golden fixture
generation logic.

The first worker implementation is intentionally command-based. It should be
easy to replace later with a Python sidecar endpoint or queue worker without
changing the external `service-job/v1` envelope.

## Job Request

Submit a golden generation job through the shared async job endpoint:

```http
POST /v1/jobs
content-type: application/json
```

```json
{
  "type": "generate_golden",
  "generate_golden": {
    "dataset": "demo",
    "limit": 1,
    "output_path": "/abs/path/youtu-graphrag/output/retrieval_golden/demo.json"
  }
}
```

The minimum stable request fields are:

- `dataset`: dataset name. Defaults may be supplied by service config, but
  persisted jobs should include the resolved dataset.
- `limit`: number of QA records to export. Defaults to `1` when omitted.
- `output_path`: absolute or worker-cwd-relative path for the generated golden
  JSON. If omitted, the service may resolve it under `YOUTU_RAG_GOLDEN_ROOT`.

Optional request fields:

- `questions`: explicit question strings. When present, the worker passes each
  value as `--question`.
- `top_k`: retrieval top-k override.
- `involved_types`: JSON string or `@path` value passed through to Python
  `--involved-types`.
- `config_path`: Python config path passed as `--config`.
- `skip_build_indices`: pass `--skip-build-indices`. This is useful only when
  the cache is known to be complete.
- `python_bin`: per-job Python executable override. Defaults to service config.
- `script_path`: per-job golden generator script override. Defaults to service
  config.
- `working_dir`: per-job worker cwd override. Defaults to service config.

The persisted `job.spec` must contain the resolved values actually used by the
runner, including `python_bin`, `script_path`, `working_dir`, and
`output_path`. This makes a completed job auditable after restart.

## Service Configuration

The Go service resolves worker defaults from environment-backed config:

| Config | Environment | Default |
| --- | --- | --- |
| Python binary | `YOUTU_RAG_PYTHON` | `$YOUTU_RAG_ARTIFACT_ROOT/.venv/bin/python` |
| Golden script | `YOUTU_RAG_GOLDEN_SCRIPT` | `$YOUTU_RAG_ARTIFACT_ROOT/scripts/generate_retriever_golden.py` |
| Worker cwd | `YOUTU_RAG_WORKER_CWD` | `$YOUTU_RAG_ARTIFACT_ROOT` |
| Golden root | `YOUTU_RAG_GOLDEN_ROOT` | `$YOUTU_RAG_ARTIFACT_ROOT/output/retrieval_golden` |

## Worker Command

The command runner maps the job spec to the existing Python entrypoint:

```bash
${python_bin} ${script_path} \
  --dataset "${dataset}" \
  --output "${output_path}" \
  --limit "${limit}"
```

Optional fields append the corresponding Python flags:

| Job field | Python flag |
| --- | --- |
| `questions[]` | repeat `--question "<value>"` |
| `top_k` | `--top-k <value>` |
| `involved_types` | `--involved-types <json-or-@path>` |
| `config_path` | `--config <path>` |
| `skip_build_indices=true` | `--skip-build-indices` |

The worker process should run with conservative threading defaults unless the
service explicitly overrides them:

```text
TOKENIZERS_PARALLELISM=false
OMP_NUM_THREADS=1
MKL_NUM_THREADS=1
VECLIB_MAXIMUM_THREADS=1
NUMEXPR_NUM_THREADS=1
```

The command's stdout and stderr are captured into the job result for diagnosis.

## Python Exit Semantics

Successful Python execution:

- exit code is `0`;
- stdout may contain a JSON summary such as
  `{"ok": true, "output": "...", "records": 1}`;
- `output_path` exists and is valid `retriever-golden/v1` JSON.

Failed Python execution:

- nonzero exit code marks the job `failed`;
- stderr is captured in `job.result.stderr` when available;
- if stderr contains structured JSON such as
  `{"ok": false, "code": "missing_dependency", "message": "..."}`, the Go
  service should preserve that text in the job error or events rather than
  hiding it behind a generic command failure.

Output validation failure:

- if the process exits `0` but `output_path` is missing, unreadable, or not a
  `retriever-golden/v1` fixture, the job must be marked `failed`;
- the output artifact should remain `pending` or move to `missing`, not
  `written`.

## Job Artifacts

`generate_golden` jobs report at least one output artifact:

```json
{
  "name": "golden_fixture",
  "role": "output",
  "kind": "retriever_golden_json",
  "schema_version": "retriever-golden/v1",
  "dataset": "demo",
  "path": "/abs/path/youtu-graphrag/output/retrieval_golden/demo.json",
  "status": "pending",
  "description": "Python retriever golden fixture written by generate_golden worker."
}
```

Artifact status rules:

- `pending`: job accepted and output path is known.
- `written`: worker succeeded and the output fixture exists.
- `missing`: worker reported success but the expected output file cannot be
  found or parsed.

Input artifacts can be added as the service learns more about dataset registry
state. Suggested names are `graph`, `chunks`, `schema`, and `faiss_cache`, with
status `configured` when the path is known.

## Inline Result

Completed jobs return a small inline result in `job.result`:

```json
{
  "schema_version": "generate-golden-result/v1",
  "dataset": "demo",
  "output_path": "/abs/path/youtu-graphrag/output/retrieval_golden/demo.json",
  "stdout": "{\"ok\": true, \"output\": \"...\", \"records\": 1}",
  "stderr": ""
}
```

The large golden fixture itself is not embedded in `job.result`; it is tracked
through the `golden_fixture` artifact.

## Lifecycle Events

Stable event names for tests and operators:

- `queued`: job accepted by the shared job manager.
- `running`: job runner started.
- `worker_started`: Python worker command started.
- `artifact_golden_written`: golden fixture artifact was written and validated.
- `succeeded`: job completed successfully.
- `failed`: job failed.
- `interrupted`: stale queued/running job was reloaded after service restart.

## Acceptance Criteria

Phase 11 acceptance should verify:

- `POST /v1/jobs` accepts `type=generate_golden`.
- response envelope is `service-job/v1`.
- persisted `job.spec` contains resolved worker command values.
- `golden_fixture` artifact starts as `pending`.
- successful jobs mark `golden_fixture.status=written`.
- failed jobs preserve stderr/error text and useful lifecycle events.
- file-backed job records survive service restart.
- generated output is a valid `retriever-golden/v1` fixture.
