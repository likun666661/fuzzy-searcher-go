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
- `concurrency`: optional worker concurrency. Defaults to `1`. Real model
  smoke should keep this low; larger values require explicit approval because
  they multiply API spend and rate-limit risk.
- `rate_limit_rpm`: optional request-per-minute budget for LLM calls. The
  worker should throttle before provider errors when this is set.
- `checkpoint_path`: optional progress/checkpoint JSONL path. Defaults beside
  `output_path`, such as `{output_path}.checkpoint.jsonl`.
- `resume`: optional boolean. When true, the worker may skip questions already
  present in `checkpoint_path`.
- `checkpoint_every`: optional completed-item interval for flushing progress.
  Defaults to `1` for smoke runs.
- `max_failures`: optional failure budget. When exceeded, stop early and mark
  the job failed.
- `question_timeout_seconds`: optional per-question timeout budget.
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
| `concurrency` | `--concurrency <n>` |
| `rate_limit_rpm` | `--rate-limit-rpm <n>` |
| `checkpoint_path` | `--checkpoint <path>` |
| `resume` | `--resume` |
| `checkpoint_every` | `--checkpoint-every <n>` |
| `max_failures` | `--max-failures <n>` |
| `question_timeout_seconds` | `--question-timeout <seconds>` |
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
  "anonymized_mapping": {
    "schema_version": "anonymized-mapping-summary/v1",
    "applicable_count": 20,
    "expected_count": 82,
    "predicted_count": 80,
    "matched_count": 70,
    "exact_matched_count": 58,
    "precision": 0.875,
    "recall": 0.8537,
    "f1": 0.8642,
    "exact_recall": 0.7073
  },
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
      "mapping_score": {
        "schema_version": "anonymized-mapping-score/v1",
        "applicable": true,
        "expected_count": 4,
        "predicted_count": 4,
        "matched_count": 3,
        "exact_matched_count": 2,
        "precision": 0.75,
        "recall": 0.75,
        "f1": 0.75,
        "exact_recall": 0.5,
        "missing_keys": ["PERSON#1"],
        "extra_keys": [],
        "by_key": {}
      },
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

For AnonyRAG-style questions, the worker also emits deterministic anonymized
mapping metrics. It extracts mappings such as `PERSON#370 -> 王进` from the
gold and predicted answers, then reports item-level `mapping_score` and an
aggregate `anonymized_mapping` summary. `matched_count` uses relaxed
containment matching so answers like `洪信（即洪太尉）` can match `洪太尉`;
`exact_matched_count` / `exact_recall` remain strict normalized string metrics.
These metrics are advisory for generic benchmark jobs but should be preferred
over a pure LLM judge when evaluating AnonyRAG entity restoration quality.

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
- `benchmark_checkpoint`: optional output `benchmark_checkpoint_jsonl`,
  status `pending` -> `written`, used for progress and resume.
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
- `benchmark_progress`
- `artifact_benchmark_written` on success
- `succeeded`, `failed`, or `canceled`

The job `error` field must preserve worker stderr or output validation details.

## Progress Contract

Benchmark jobs may run for a long time and spend real model API budget.
Progress must be observable through job events and, when configured, through a
checkpoint artifact.

`benchmark_progress` events should use this minimum payload in the event
message or future structured event metadata:

```json
{
  "schema_version": "benchmark-progress/v1",
  "dataset": "anony_chs",
  "total": 688,
  "completed": 25,
  "succeeded": 23,
  "failed": 2,
  "correct": 11,
  "accuracy_so_far": 0.4783,
  "current_id": "qa_25",
  "checkpoint_path": "/abs/path/output/benchmarks/anony_chs.checkpoint.jsonl"
}
```

Stable fields:

- `total`: total planned questions after `offset` and `limit` are applied.
- `completed`: items with terminal per-question outcome.
- `succeeded`: items that produced an answer and judge result.
- `failed`: items that failed after retry budget.
- `correct`: items judged correct.
- `accuracy_so_far`: `correct / succeeded` when `succeeded > 0`, otherwise
  `0`.
- `current_id`: current or most recently completed QA id.
- `checkpoint_path`: checkpoint artifact path when configured.

Event frequency should be bounded. Recommended defaults:

- emit one progress event after every completed item for `limit <= 20`;
- emit every `checkpoint_every` items for larger runs;
- always emit a final progress event before `succeeded`, `failed`, or
  `canceled`.

## Checkpoint and Resume

The checkpoint file should be append-only JSONL, one terminal item per line:

```json
{
  "schema_version": "benchmark-checkpoint-item/v1",
  "id": "qa_25",
  "index": 24,
  "question": "...",
  "gold_answer": "...",
  "predicted_answer": "...",
  "judge": "1",
  "correct": true,
  "latency_ms": 11849,
  "error": "",
  "finished_at": "2026-05-18T00:00:00Z"
}
```

Resume rules:

- `resume=false`: ignore any existing checkpoint and start from the requested
  offset.
- `resume=true`: read checkpoint ids and skip completed items with matching
  ids.
- a malformed checkpoint should fail fast with `benchmark_checkpoint_invalid`;
- final `benchmark-result/v1.items` should include both resumed items and newly
  completed items in QA order.

The first implementation may keep resume inside the Python worker. Go only
needs to pass the fields, record the checkpoint artifact, and expose progress
through events.

## Concurrency and Cost Controls

Concurrency and rate limiting are part of the external contract because this
job can spend real API money.

Rules:

- default `concurrency=1`;
- `concurrency` must be a positive integer;
- implementation should cap concurrency to a conservative service maximum
  unless a future admin config raises it;
- `rate_limit_rpm` applies to model calls, not just questions, because each
  question may call both answer and judge models;
- `limit` should be required or strongly enforced for smoke runs against large
  datasets such as AnonyRAG;
- API keys must not appear in progress events, checkpoint rows, final result,
  stdout/stderr summaries, job spec, or operation history.

Recommended failure codes:

- `benchmark_invalid_concurrency`: invalid or unsupported concurrency value.
- `benchmark_rate_limited`: provider or worker rate limit stopped the run.
- `benchmark_checkpoint_invalid`: checkpoint could not be parsed for resume.
- `benchmark_failure_budget_exceeded`: `max_failures` was exceeded.

## Cancellation Semantics

Cancellation should be cooperative:

- Go canceling the job should signal the worker process as current job runners
  do for other workers.
- The Python worker should stop between questions when it observes cancellation
  or receives a termination signal.
- Completed checkpoint rows should remain on disk.
- The job should end as `canceled` when cancellation is explicit, not `failed`.
- Partial final output is optional on cancel; checkpoint is the durable partial
  artifact.

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
- `benchmark_checkpoint_invalid`: checkpoint JSONL is malformed.
- `benchmark_llm_unconfigured`: required model/API environment is missing.
- `benchmark_judge_invalid`: judge output cannot be interpreted as `1` or `0`.
- `benchmark_failure_budget_exceeded`: per-item failures exceeded
  `max_failures`.

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
- progress events expose completed/total/correct/failed counts.
- checkpoint/resume can recover completed items without re-answering them.
- concurrency/rate-limit inputs are persisted without exposing API keys.
- existing release-check, service-smoke, demo-service-smoke, and job/workflow
  gates do not regress.
