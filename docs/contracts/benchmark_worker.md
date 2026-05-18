# Benchmark Job and Workflow Contract

This document defines the Phase 26 `benchmark` boundary for the Go service.

The goal is to turn ad hoc dataset evaluation into a tracked long-running
service operation. Go owns the job/workflow envelope, persistence, artifact
metadata, worker command execution, restart readback, and failure visibility.
Python continues to own model-heavy benchmark internals: retrieval/answer
execution, LLM judge prompts, dataset-specific parsing, and model API calls.

## Job Request

Submit a benchmark job through `POST /v1/jobs`:

```json
{
  "type": "benchmark",
  "benchmark": {
    "dataset": "anony_eng",
    "qa_path": "/abs/path/youtu-graphrag/data/anony_eng/final_qa_pairs.json",
    "output_path": "/abs/path/youtu-graphrag/output/benchmarks/anony_eng_smoke.json",
    "limit": 20,
    "mode": "noagent",
    "top_k": 20,
    "answer_model": "deepseek-v4-pro",
    "judge_model": "deepseek-v4-pro",
    "llm_base_url": "https://api.deepseek.com"
  }
}
```

Stable fields:

- `dataset`: dataset name. Required.
- `qa_path`: QA JSON file. Required unless a future dataset registry can
  resolve it.
- `output_path`: benchmark result JSON path. Defaults to
  `$YOUTU_RAG_ARTIFACT_ROOT/output/benchmarks/{dataset}.json`.
- `limit`: optional max QA count. Defaults to worker behavior; production
  callers should set this explicitly for smoke runs.
- `offset`: optional starting QA offset.
- `mode`: answer mode such as `noagent` or `agent`.
- `top_k`: retrieval depth.
- `answer_model`: model used for answer generation.
- `judge_model`: model used for LLM judging.
- `llm_base_url`: model API base URL. For DeepSeek this should be
  `https://api.deepseek.com`.
- `graph_path`, `chunks_path`, `cache_dir`, `schema_path`: optional resolved
  dataset artifacts.
- `config_path`: optional Python config path.
- `python_bin`, `script_path`, `working_dir`: per-job worker command
  overrides.

The persisted `job.spec` must contain resolved worker command fields and
non-secret model metadata. It must not store API keys.

## Service Configuration

Recommended environment defaults:

| Purpose | Environment variable | Default |
| --- | --- | --- |
| Artifact root | `YOUTU_RAG_ARTIFACT_ROOT` | `../youtu-graphrag` |
| Python binary | `YOUTU_RAG_PYTHON` | `$YOUTU_RAG_ARTIFACT_ROOT/.venv/bin/python` |
| Benchmark script | `YOUTU_RAG_BENCHMARK_SCRIPT` | `$YOUTU_RAG_ARTIFACT_ROOT/scripts/benchmark_worker.py` |
| Worker cwd | `YOUTU_RAG_WORKER_CWD` | `$YOUTU_RAG_ARTIFACT_ROOT` |
| Default answer model | `YOUTU_RAG_ANSWER_MODEL` | unset |
| Default judge model | `YOUTU_RAG_JUDGE_MODEL` | unset |
| Default LLM base URL | `YOUTU_RAG_LLM_BASE_URL` | unset |

API keys are worker-owned environment variables. The preferred DeepSeek mapping
is:

```bash
export LLM_API_KEY="${DEEPSEEK_API_KEY}"
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-v4-pro"
```

The Go service may check that a required key is present before starting the
worker, but it must not log, persist, echo, or place the key in a job spec,
artifact, event, or result.

## Worker Command

The Go runner maps the job spec to a Python command:

```bash
$python_bin $script_path \
  --dataset "$dataset" \
  --qa "$qa_path" \
  --output "$output_path"
```

Optional fields append:

| Job field | Python flag |
| --- | --- |
| `limit` | `--limit <n>` |
| `offset` | `--offset <n>` |
| `mode` | `--mode <value>` |
| `top_k` | `--top-k <n>` |
| `answer_model` | `--answer-model <value>` |
| `judge_model` | `--judge-model <value>` |
| `llm_base_url` | `--llm-base-url <url>` |
| `graph_path` | `--graph <path>` |
| `chunks_path` | `--chunks <path>` |
| `corpus_path` | `--corpus <path>` |
| `cache_dir` | `--cache-dir <path>` |
| `schema_path` | `--schema <path>` |
| `config_path` | `--config <path>` |

The worker process should inherit the same conservative native-thread defaults
as other Python workers:

```text
TOKENIZERS_PARALLELISM=false
OMP_NUM_THREADS=1
MKL_NUM_THREADS=1
VECLIB_MAXIMUM_THREADS=1
NUMEXPR_NUM_THREADS=1
```

## Output Artifact

Successful worker execution requires `output_path` to exist and parse as
`benchmark-result/v1`:

```json
{
  "schema_version": "benchmark-result/v1",
  "dataset": "anony_eng",
  "qa_path": "/abs/path/youtu-graphrag/data/anony_eng/final_qa_pairs.json",
  "question_count": 20,
  "answered_count": 20,
  "correct_count": 14,
  "failed_count": 0,
  "accuracy": 0.7,
  "started_at": "2026-05-18T00:00:00Z",
  "finished_at": "2026-05-18T00:05:00Z",
  "duration_ms": 300000,
  "model": {
    "answer_model": "deepseek-v4-pro",
    "judge_model": "deepseek-v4-pro",
    "llm_base_url": "https://api.deepseek.com"
  },
  "retrieval": {
    "mode": "noagent",
    "top_k": 20
  },
  "items": [
    {
      "id": "qa_1",
      "question": "...",
      "gold_answer": "...",
      "predicted_answer": "...",
      "judge": "1",
      "correct": true,
      "latency_ms": 11849,
      "error": ""
    }
  ]
}
```

Minimum required fields:

- `schema_version=benchmark-result/v1`
- `dataset`
- `question_count`
- `correct_count`
- `accuracy`
- `items`

`items` may contain extra fields such as retrieved triples/chunks, prompts,
judge rationale, token usage, or cost estimates. Go should validate only the
minimum schema at this layer.

## Inline Job Result

Completed benchmark jobs return a compact inline result:

```json
{
  "schema_version": "benchmark-job-result/v1",
  "dataset": "anony_eng",
  "output_path": "/abs/path/youtu-graphrag/output/benchmarks/anony_eng_smoke.json",
  "question_count": 20,
  "correct_count": 14,
  "accuracy": 0.7,
  "stdout": "...",
  "stderr": ""
}
```

The full item-level result remains on disk as the `benchmark_result` artifact.

## Job Artifacts

Expected artifacts:

- `qa`: input `qa_json`, status `configured`.
- `corpus`: optional input `corpus_json`, status `configured`.
- `graph`: optional input `graph_json`, status `configured`.
- `chunks`: optional input `chunks_txt`, status `configured`.
- `schema`: optional input `schema_json`, status `configured`.
- `cache`: optional input `faiss_cache_dir`, status `configured`.
- `benchmark_result`: output `benchmark_result_json`,
  `schema_version=benchmark-result/v1`.

Benchmark result status transitions:

- `pending`: job accepted.
- `running`: worker process started.
- `written`: worker succeeded and output passed schema validation.
- `missing`: worker exited `0` but output file was absent.
- `failed`: worker failed or output schema validation failed.

## Events

Expected job events:

- `queued`
- `running`
- `worker_started`
- `artifact_benchmark_written` on success
- `succeeded`, `failed`, or `canceled`

The job `error` field must preserve worker stderr or output validation details.

## Benchmark Workflow

The first benchmark workflow should compose existing capabilities instead of
rebuilding every step inline.

Submit through `POST /v1/workflows`:

```json
{
  "type": "benchmark",
  "benchmark": {
    "dataset": "anony_eng",
    "qa_path": "/abs/path/youtu-graphrag/data/anony_eng/final_qa_pairs.json",
    "limit": 20,
    "build_first": false,
    "mode": "noagent",
    "top_k": 20,
    "answer_model": "deepseek-v4-pro",
    "judge_model": "deepseek-v4-pro"
  }
}
```

Two workflow shapes are allowed:

```text
benchmark
```

or, when `build_first=true`:

```text
build_graph -> benchmark
```

Step and artifact handoff rules:

- `build_graph.graph` and `build_graph.chunks` become benchmark inputs.
- `build_graph.cache` is retained as benchmark cache input when available.
- `benchmark.benchmark_result` becomes the final workflow output artifact.
- If `build_graph` fails, do not submit benchmark.
- If benchmark fails, preserve build artifacts and mark workflow failed.

Workflow result:

```json
{
  "schema_version": "benchmark-workflow-result/v1",
  "dataset": "anony_eng",
  "benchmark_job_id": "job_...",
  "build_graph_job_id": "job_...",
  "output_path": "/abs/path/youtu-graphrag/output/benchmarks/anony_eng_smoke.json",
  "accuracy": 0.7,
  "question_count": 20
}
```

## Failure Semantics

Stable error meanings:

- `benchmark_invalid_request`: missing dataset, QA path, or output path.
- `benchmark_missing_input`: QA/graph/chunks/schema/cache path is required but
  missing.
- `benchmark_worker_failed`: worker process exited non-zero.
- `benchmark_output_missing`: worker exited `0` but output file is missing.
- `benchmark_output_invalid`: output JSON is malformed or not
  `benchmark-result/v1`.
- `benchmark_llm_unconfigured`: required model/API environment is missing.
- `benchmark_judge_invalid`: judge output cannot be interpreted as `1` or `0`.

When a worker fails due to missing API key, the job error should say which env
var is missing but must not include secret values.

## Acceptance Criteria

Phase 26 validation should verify:

- `POST /v1/jobs` accepts `type=benchmark` and returns `service-job/v1`.
- persisted job spec includes resolved non-secret model, worker, dataset, QA,
  and output fields.
- success writes `benchmark-result/v1`, marks `benchmark_result` as `written`,
  and survives service restart.
- worker failure, missing output, bad schema version, and invalid judge output
  fail the job with stable diagnostics.
- optional workflow mode can run benchmark alone or build_graph then benchmark.
- existing release-check, service-smoke, demo-service-smoke, and job/workflow
  gates do not regress.
