# Service Profile and Configuration Validation Contract

This document defines the Phase 22 service startup contract for the
long-running Youtu-RAG Go service. The goal is to make local and production
startup predictable: a clone should fail fast when required configuration is
missing, and operators should be able to inspect the resolved profile without
reading Go source.

The Go service owns configuration loading, validation, and process startup.
Python remains the worker/sidecar layer for model-heavy work.

## Schema

Configuration validation returns a stable envelope:

```json
{
  "schema_version": "service-config-check/v1",
  "profile": "demo",
  "ready": false,
  "checks": [
    {
      "name": "sidecar_url",
      "status": "failed",
      "required": true,
      "message": "YOUTU_RAG_SIDECAR_URL is required for this profile/mode",
      "value": ""
    }
  ]
}
```

`ready=true` means all required checks passed. Optional missing inputs may be
reported as `warning` or `skipped` without blocking startup checks that are
explicitly non-strict.

Each check has:

- `name`: stable machine-readable check name.
- `status`: one of `ok`, `failed`, `warning`, or `skipped`.
- `required`: whether this check contributes to `ready=false`.
- `message`: human-readable diagnostic when useful.
- `path`: filesystem path checked, when applicable.
- `value`: non-secret resolved value, when applicable.

Validation output must never include API keys, tokens, or full secret values.
Worker model secrets such as `DEEPSEEK_API_KEY` are worker-owned environment
variables and are out of scope for this Go startup report. If a future check
needs to mention a secret, it must report only whether it is configured.

## Profiles

`YOUTU_RAG_PROFILE` selects the startup policy. Supported profiles are:

| Profile | Purpose | Required at startup |
| --- | --- | --- |
| `local` | Development defaults. The service can start with partial artifacts so API and job contract tests can use temp roots and fake workers. | HTTP address and default dataset. |
| `demo` | Local demo parity run against a prepared `youtu-graphrag` artifact tree. | Artifact root, default graph/chunks, and sidecar URL when mode needs Python sidecar. |
| `production` | Long-running managed service. Defaults must be explicit and worker paths must exist. | Artifact root, worker cwd, Python executable, worker scripts, sidecar URL, HTTP address, default dataset. |

Unknown profiles are validation failures.

The default profile is `local`. It is intentionally permissive because many
unit tests create small temporary artifact roots and do not need a real Python
runtime. `demo` is the profile for a developer who has a sibling
`youtu-graphrag` checkout with demo artifacts. `production` is the profile for
a real long-running deployment.

## Environment Variables

### Core Service

| Variable | Default | Notes |
| --- | --- | --- |
| `YOUTU_RAG_PROFILE` | `local` | One of `local`, `demo`, `production`. |
| `YOUTU_RAG_APP_NAME` | `youtu-rag-service` | Process/application name. |
| `YOUTU_RAG_ENV` | `development` | Deployment environment label. |
| `YOUTU_RAG_VERSION` | `dev` | Reported service version. |
| `YOUTU_RAG_HTTP_ADDR` | `:8080` | Listen address. Empty is invalid. |
| `YOUTU_RAG_DATASET` | `demo` | Default dataset used by demo/service examples. Empty is invalid. |
| `YOUTU_RAG_DATASETS` | `<YOUTU_RAG_DATASET>` | Comma-separated known dataset names. |
| `YOUTU_RAG_MODE` | `native` | Default retrieve mode. Demo parity normally uses `native-path1-rerank`. |
| `YOUTU_RAG_VALIDATE_ON_START` | `false` | When true, startup should fail if required validation checks fail. |
| `YOUTU_RAG_SHUTDOWN_SECONDS` | `10` | Graceful shutdown timeout. |

### Artifact Roots

`YOUTU_RAG_ARTIFACT_ROOT` defaults to `../youtu-graphrag`. All other roots
default under that tree unless explicitly overridden:

| Variable | Default relative to `YOUTU_RAG_ARTIFACT_ROOT` | Purpose |
| --- | --- | --- |
| `YOUTU_RAG_CORPUS_ROOT` | `data` | Corpus and imported dataset inputs. |
| `YOUTU_RAG_SCHEMA_ROOT` | `schemas` | Dataset schema files. |
| `YOUTU_RAG_GRAPH_ROOT` | `output/graphs` | Graph JSON artifacts. |
| `YOUTU_RAG_CHUNKS_ROOT` | `output/chunks` | Chunk mapping artifacts. |
| `YOUTU_RAG_CACHE_ROOT` | `retriever/faiss_cache_new` | Vector/cache artifacts used by sidecar. |
| `YOUTU_RAG_GOLDEN_ROOT` | `output/retrieval_golden` | Golden fixtures. |
| `YOUTU_RAG_TRACE_ROOT` | `output/retrieval_traces` | Retrieval traces. |
| `YOUTU_RAG_DATASET_META_ROOT` | `output/datasets` | Managed dataset metadata. |
| `YOUTU_RAG_DATASET_OPS_ROOT` | `output/dataset_operations` | Dataset operation records. |
| `YOUTU_RAG_JOB_ROOT` | `output/jobs` | Async job records. |
| `YOUTU_RAG_WORKFLOW_ROOT` | `output/workflows` | Workflow records. |

Service-managed write roots must be writable before production startup is
considered healthy. Read-only artifact inputs may exist before startup or be
created later by jobs, depending on the profile and endpoint.

### Retrieval Inputs

| Variable | Default | Notes |
| --- | --- | --- |
| `YOUTU_RAG_GRAPH` | empty | Explicit default graph file. Required for `demo` retrieval validation. |
| `YOUTU_RAG_CHUNKS` | empty | Explicit default chunks file. Required for `demo` retrieval validation. |
| `YOUTU_RAG_SIDECAR_URL` | empty | Python vector sidecar URL. Required in `production`; required in `demo` for sidecar-backed modes. |
| `YOUTU_RAG_PATH2_THRESHOLD` | `0.1` | Path2 score threshold used by current retrieval orchestration. |

Sidecar-backed modes include the current service main path
`native-path1-rerank` and the migration modes that call Python sidecar
primitives. Pure `native` mode does not require a sidecar.

### Python Worker Commands

The Go service executes Python workers for model-heavy long-running jobs. The
profile validation contract treats these as paths, not embedded logic.

| Variable | Default relative to `YOUTU_RAG_ARTIFACT_ROOT` | Purpose |
| --- | --- | --- |
| `YOUTU_RAG_PYTHON` | `.venv/bin/python` | Python executable for worker commands. |
| `YOUTU_RAG_WORKER_CWD` | artifact root | Working directory for worker process execution. |
| `YOUTU_RAG_PARSE_DOCUMENTS_SCRIPT` | `scripts/parse_documents_worker.py` | `parse_documents` worker. |
| `YOUTU_RAG_BUILD_GRAPH_SCRIPT` | `scripts/build_graph_worker.py` | `build_graph` worker. |
| `YOUTU_RAG_ANSWER_SCRIPT` | `scripts/answer_worker.py` | `answer` worker. |
| `YOUTU_RAG_GOLDEN_SCRIPT` | `scripts/generate_retriever_golden.py` | `generate_golden` worker. |

`production` requires the Python executable, worker cwd, and worker scripts to
exist at validation time. `local` may skip them so service contract tests can
use fake workers. `demo` may warn on missing worker scripts unless a demo job
path explicitly uses them.

## Required Checks

The validation report should include at least these stable check names:

| Check | Meaning |
| --- | --- |
| `profile` | `YOUTU_RAG_PROFILE` is one of the supported profiles. |
| `http_addr` | `YOUTU_RAG_HTTP_ADDR` is non-empty. |
| `default_dataset` | `YOUTU_RAG_DATASET` is non-empty. |
| `artifact_root` | Main artifact root is configured and exists when profile requires it. |
| `default_graph` | Explicit demo graph file exists when demo retrieval validation requires it. |
| `default_chunks` | Explicit demo chunks file exists when demo retrieval validation requires it. |
| `sidecar_url` | Sidecar URL is present when profile/mode requires it. |
| `worker_cwd` | Python worker cwd exists when profile requires workers. |
| `python_bin` | Python executable exists when profile requires workers. |
| `parse_documents_script` | Parse worker script exists when profile requires workers. |
| `build_graph_script` | Build graph worker script exists when profile requires workers. |
| `answer_script` | Answer worker script exists when profile requires workers. |
| `golden_script` | Golden generation worker script exists when profile requires workers. |

Future implementations may add checks, but existing check names and semantics
should remain stable for tests and automation.

## Startup Semantics

The service has two different readiness surfaces:

- `/healthz`: process is alive. It should not require graph artifacts or
  Python sidecar health.
- `/readyz`: service can serve its configured default dataset/mode with current
  dependencies. Missing artifacts or unreachable sidecar should be visible here.

`YOUTU_RAG_VALIDATE_ON_START=true` turns required validation failures into a
startup error. This is recommended for `demo` and `production`. With validation
disabled, the service may start and expose diagnostics, but dependent endpoints
must return explicit client/actionable errors instead of panics.

The intended local run flow is:

1. Resolve profile and environment into `config.Config`.
2. Produce `service-config-check/v1`.
3. If strict validation is enabled and `ready=false`, fail before binding the
   HTTP listener.
4. Start the service.
5. Use `/readyz`, `/v1/sidecars`, and dataset/artifact endpoints for runtime
   diagnostics.

## One-Command Local Run Contract

Phase 22 should provide a documented local entrypoint, such as
`make service-local`, that:

1. Selects a profile, normally `demo`.
2. Resolves default sibling checkout paths for `../youtu-graphrag`.
3. Runs the configuration validation check first.
4. Starts `cmd/youtu-rag-service` only when required checks pass.
5. Prints the active service URL and the validation report path or JSON.

The command should not start or manage the Python vector sidecar unless that is
explicitly documented as a separate target. If sidecar startup is later added,
it should have a distinct lifecycle and logs so Go startup failures are not
confused with Python dependency failures.

## Error Semantics

Configuration failures should use stable, actionable reasons in logs and API
responses. Recommended error identifiers:

| Error | Meaning |
| --- | --- |
| `invalid_profile` | `YOUTU_RAG_PROFILE` is unsupported. |
| `missing_required_env` | A required environment variable is empty. |
| `path_not_found` | A required configured path does not exist. |
| `path_not_writable` | A required output root cannot be written. |
| `worker_not_executable` | `YOUTU_RAG_PYTHON` exists but cannot be executed. |
| `sidecar_unconfigured` | A sidecar-backed mode lacks `YOUTU_RAG_SIDECAR_URL`. |
| `sidecar_unreachable` | Sidecar URL is configured but health/capability check fails. |

The initial Go implementation may expose these in `message` text before a full
typed error code field exists, but tests should be allowed to assert the
meaning without depending on raw `os.Stat` strings.

## Acceptance Criteria

Phase 22 is complete when:

- The contract above is linked from README and service test strategy.
- `local`, `demo`, and `production` profiles are documented and recognized.
- Validation returns `service-config-check/v1` with stable check names.
- Strict startup validation can fail fast before serving traffic.
- Missing artifact, missing worker, and missing sidecar cases produce clear
  diagnostics.
- One-command local startup is documented for a clean clone with explicit
  artifact paths.
- Tests can verify validation behavior without needing real LLM keys or real
  model calls.

