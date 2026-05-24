# Paper-Aligned Benchmark Contract

This document defines the Phase 33 contract for running a benchmark that is
aligned with the original Youtu-GraphRAG paper path while keeping the service
runner observable and resumable.

The key distinction is that this is not the lightweight `benchmark` smoke path
that answers from keyword-selected chunks or from the Go service `/v1/retrieve`
API. A paper-aligned run must call the original Python GraphQ + KTRetriever +
Eval chain, with an outer runner that adds checkpointing, progress, timeout,
and failure controls.

## Scope

Phase 33 targets `anony_chs` first:

| Artifact | Path |
| --- | --- |
| QA | `data/anony_chs/final_qa_pairs.json` |
| graph | `output/graphs/anony_chs_full_flash_new.json` |
| chunks | `output/chunks/anony_chs_full_flash.txt` |
| schema | `schemas/anony_chs.json` |

The graph and chunks are expected to be prebuilt before the benchmark starts.
The benchmark runner must not rebuild the graph as part of a paper-aligned
answer/eval run.

The initial full `anony_chs` graph artifact was built by the industrial WAL /
multi-runner graph construction path with `skip_communities=true`. Therefore a
Phase 33 result over that artifact should be reported as:

```text
paper retrieval/answer/eval path + industrial WAL graph artifact
```

It should not claim to be a byte-for-byte reproduction of the original
community-compaction stage when level-4 community embedding was skipped. Once
Phase 34 replay-only community compaction has produced a
`graph-compaction-wal/v1` and community-enriched graph, the benchmark result
should set `skip_communities=false` and reference the compaction WAL path.

## Paper Method Boundary

The Python runner should reuse the same functional path as the original
`youtu-graphrag/main.py` retrieval flow:

1. Load QA rows from the dataset QA file.
2. Construct `GraphQ` from `models.retriever.agentic_decomposer`.
3. Construct `KTRetriever` from `models.retriever.enhanced_kt_retriever`.
4. Build or load FAISS indices from the supplied graph/chunks/cache artifacts.
5. For each question, decompose through `GraphQ.decompose(question, schema)`.
6. Retrieve through `KTRetriever.process_retrieval_results`.
7. Generate an answer through `KTRetriever.generate_answer`.
8. Judge with `utils.eval.Eval.eval(question, gold_answer, predicted_answer)`.
9. Record the judge result as `"1"` or `"0"` and aggregate accuracy.

Required retrieval config for the main paper-aligned run:

| Config | Required value |
| --- | --- |
| `constructor_trigger` | `false` |
| `retrieve_trigger` | `true` |
| `mode` | `agent` for main run |
| `retrieval.recall_paths` | `2` |
| `retrieval.top_k_filter` | `20` |
| `retrieval.enable_query_enhancement` | `true` |
| `retrieval.enable_high_recall` | `true` |
| `retrieval.enable_reranking` | `true` |

`mode=noagent` is allowed only as an ablation or debugging run. It must be
persisted separately and must not be merged with `mode=agent` accuracy.

## Agent Mode Semantics

`mode=agent` means the runner follows the original agentic retrieval shape:

- run the no-agent initial decomposition/retrieval/answer step;
- use the initial answer as the first thought input for the IRCoT loop;
- iterate up to `config.retrieval.agent.max_steps`;
- stop early when the model emits `So the answer is:`;
- otherwise parse `The new query is:` and retrieve again;
- generate a final answer from the accumulated triples/chunks;
- judge the final answer through `Eval.eval`.

The runner may store model-generated reasoning text in a private debug artifact
when explicitly requested, but the default public result should not persist full
chain-of-thought transcripts. It should persist operational summaries such as
step count, generated query count, retrieval counts, and final answer.

## Job Request

Submit through `POST /v1/jobs` using a dedicated paper benchmark job type, or a
future compatible extension of `type=benchmark` with
`benchmark_kind=paper_aligned`.

Recommended request shape:

```json
{
  "type": "paper_benchmark",
  "paper_benchmark": {
    "dataset": "anony_chs",
    "qa_path": "/abs/path/youtu-graphrag/data/anony_chs/final_qa_pairs.json",
    "graph_path": "/abs/path/youtu-graphrag/output/graphs/anony_chs_full_flash_new.json",
    "chunks_path": "/abs/path/youtu-graphrag/output/chunks/anony_chs_full_flash.txt",
    "schema_path": "/abs/path/youtu-graphrag/schemas/anony_chs.json",
    "output_path": "/abs/path/youtu-graphrag/output/benchmarks/anony_chs_paper_agent.json",
    "checkpoint_path": "/abs/path/youtu-graphrag/output/benchmarks/anony_chs_paper_agent.checkpoint.jsonl",
    "mode": "agent",
    "limit": 20,
    "offset": 0,
    "resume": true,
    "top_k": 20,
    "recall_paths": 2,
    "answer_model": "deepseek-v4-pro",
    "judge_model": "deepseek-v4-pro",
    "llm_base_url": "https://api.deepseek.com",
    "question_timeout_seconds": 300,
    "rate_limit_rpm": 30,
    "max_failures": 5,
    "include_private_traces": false
  }
}
```

Stable fields:

- `dataset`: dataset key. Required.
- `qa_path`: QA JSON file. Required.
- `graph_path`: prebuilt graph JSON. Required.
- `chunks_path`: prebuilt chunks TXT. Required.
- `schema_path`: schema JSON used by GraphQ. Required.
- `cache_dir`: optional FAISS/cache directory override.
- `config_path`: optional original repo config path.
- `output_path`: final result JSON. Required after defaults resolve.
- `checkpoint_path`: append-only per-question checkpoint JSONL.
- `mode`: `agent` or `noagent`; `agent` is the paper main run.
- `limit` / `offset`: subset controls. Full `anony_chs` is 688 rows.
- `resume`: skip checkpointed terminal items when true.
- `top_k`: should resolve to `20` for paper-aligned runs.
- `recall_paths`: should resolve to `2`.
- `question_timeout_seconds`: per-question wall-clock timeout.
- `rate_limit_rpm`: optional model-call budget.
- `max_failures`: stop after this many failed questions.
- `answer_model`, `judge_model`, `llm_base_url`: non-secret model metadata.
- `include_private_traces`: opt-in debug traces. Defaults to false.

The persisted job spec must contain resolved artifact paths and non-secret
model metadata. It must not store API keys.

## Worker Command

The Go runner maps the spec to a Python command:

```bash
$python_bin $script_path \
  --dataset "$dataset" \
  --qa "$qa_path" \
  --graph "$graph_path" \
  --chunks "$chunks_path" \
  --schema "$schema_path" \
  --output "$output_path" \
  --checkpoint "$checkpoint_path" \
  --mode agent \
  --top-k 20 \
  --recall-paths 2 \
  --resume
```

Optional flags:

| Job field | Python flag |
| --- | --- |
| `limit` | `--limit <n>` |
| `offset` | `--offset <n>` |
| `config_path` | `--config <path>` |
| `cache_dir` | `--cache-dir <path>` |
| `answer_model` | `--answer-model <value>` |
| `judge_model` | `--judge-model <value>` |
| `llm_base_url` | `--llm-base-url <url>` |
| `question_timeout_seconds` | `--question-timeout <seconds>` |
| `rate_limit_rpm` | `--rate-limit-rpm <n>` |
| `max_failures` | `--max-failures <n>` |
| `include_private_traces` | `--include-private-traces` |

The preferred DeepSeek environment mapping is:

```bash
export LLM_API_KEY="${DEEPSEEK_API_KEY}"
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-v4-pro"
```

The service may validate that the required key is present, but it must not log,
persist, echo, or include the key in stdout summaries, job specs, events,
checkpoint rows, result artifacts, operation history, or workflow records.

## Output Artifact

Successful worker execution requires `output_path` to exist and parse as
`paper-benchmark-result/v1`:

```json
{
  "schema_version": "paper-benchmark-result/v1",
  "dataset": "anony_chs",
  "benchmark_kind": "paper_aligned",
  "mode": "agent",
  "question_count": 20,
  "answered_count": 20,
  "correct_count": 8,
  "failed_count": 0,
  "accuracy": 0.4,
  "started_at": "2026-05-23T00:00:00Z",
  "finished_at": "2026-05-23T00:30:00Z",
  "duration_ms": 1800000,
  "artifacts": {
    "qa_path": "/abs/path/youtu-graphrag/data/anony_chs/final_qa_pairs.json",
    "graph_path": "/abs/path/youtu-graphrag/output/graphs/anony_chs_full_flash_new.json",
    "chunks_path": "/abs/path/youtu-graphrag/output/chunks/anony_chs_full_flash.txt",
    "schema_path": "/abs/path/youtu-graphrag/schemas/anony_chs.json"
  },
  "paper_config": {
    "constructor_trigger": false,
    "retrieve_trigger": true,
    "mode": "agent",
    "recall_paths": 2,
    "top_k_filter": 20,
    "enable_query_enhancement": true,
    "enable_high_recall": true,
    "enable_reranking": true,
    "agent_max_steps": 5
  },
  "deviations": {
    "graph_source": "industrial_wal_full_flash",
    "skip_communities": true,
    "community_compaction": "skipped"
  },
  "model": {
    "answer_model": "deepseek-v4-pro",
    "judge_model": "deepseek-v4-pro",
    "llm_base_url": "https://api.deepseek.com"
  },
  "anonymized_mapping": {
    "schema_version": "anonymized-mapping-summary/v1",
    "applicable_count": 20,
    "precision": 0.0,
    "recall": 0.0,
    "f1": 0.0,
    "exact_recall": 0.0
  },
  "items": [
    {
      "schema_version": "paper-benchmark-item/v1",
      "id": "qa_0",
      "index": 0,
      "question": "...",
      "gold_answer": "...",
      "predicted_answer": "...",
      "judge": "1",
      "correct": true,
      "mode": "agent",
      "decomposition": {
        "sub_question_count": 2,
        "involved_types": {
          "nodes": [],
          "relations": [],
          "attributes": []
        }
      },
      "retrieval": {
        "context_source": "paper_kt_retriever",
        "triples_count": 20,
        "chunk_count": 3,
        "context_chunk_ids": ["chunk_1"],
        "retrieval_time_ms": 1234
      },
      "agent": {
        "enabled": true,
        "step_count": 2,
        "generated_query_count": 1,
        "reasoning_trace_redacted": true
      },
      "mapping_score": {
        "schema_version": "anonymized-mapping-score/v1",
        "applicable": true,
        "precision": 0.0,
        "recall": 0.0,
        "f1": 0.0,
        "exact_recall": 0.0
      },
      "latency_ms": 90000,
      "error": ""
    }
  ]
}
```

Minimum required fields:

- `schema_version=paper-benchmark-result/v1`
- `dataset`
- `benchmark_kind=paper_aligned`
- `mode`
- `question_count`
- `correct_count`
- `accuracy`
- `paper_config`
- `deviations`
- `items`

For AnonyRAG, result artifacts should retain the deterministic anonymized
mapping metrics from `benchmark-result/v1` so paper accuracy and entity
restoration quality can be interpreted together.

## Checkpoint and Resume

The checkpoint file is append-only JSONL. Each terminal question row uses
`paper-benchmark-checkpoint-item/v1`:

```json
{
  "schema_version": "paper-benchmark-checkpoint-item/v1",
  "id": "qa_0",
  "index": 0,
  "question": "...",
  "gold_answer": "...",
  "predicted_answer": "...",
  "judge": "1",
  "correct": true,
  "mode": "agent",
  "latency_ms": 90000,
  "error": "",
  "finished_at": "2026-05-23T00:00:00Z"
}
```

Resume rules:

- `resume=false`: ignore any existing checkpoint for scheduling.
- `resume=true`: skip checkpoint rows with terminal `id`/`index`.
- malformed checkpoint JSON must fail fast with
  `paper_benchmark_checkpoint_invalid`;
- final `paper-benchmark-result/v1.items` must include resumed and newly
  completed items in QA order;
- completed checkpoint rows must not be re-answered or re-judged.

## Progress and Events

Jobs should emit `paper_benchmark_progress` events:

```json
{
  "schema_version": "paper-benchmark-progress/v1",
  "dataset": "anony_chs",
  "mode": "agent",
  "total": 688,
  "completed": 20,
  "succeeded": 20,
  "failed": 0,
  "correct": 8,
  "accuracy_so_far": 0.4,
  "current_id": "qa_19",
  "checkpoint_path": "/abs/path/output/benchmarks/anony_chs_paper_agent.checkpoint.jsonl"
}
```

Recommended frequency:

- every completed item for `limit <= 20`;
- every `checkpoint_every` items for larger runs;
- always once before terminal `succeeded`, `failed`, or `canceled`.

## Failure Semantics

Stable error meanings:

- `paper_benchmark_invalid_request`: required spec field is missing or invalid.
- `paper_benchmark_missing_artifact`: QA/graph/chunks/schema path is missing.
- `paper_benchmark_llm_unconfigured`: required model/API environment is absent.
- `paper_benchmark_checkpoint_invalid`: checkpoint JSONL cannot be parsed.
- `paper_benchmark_item_timeout`: one question exceeded its timeout.
- `paper_benchmark_judge_invalid`: judge did not return `"1"` or `"0"`.
- `paper_benchmark_failure_budget_exceeded`: `max_failures` was exceeded.
- `paper_benchmark_output_missing`: worker exited `0` without result artifact.
- `paper_benchmark_output_invalid`: result JSON is malformed or wrong schema.
- `paper_benchmark_wrong_context_source`: result used a non-paper retrieval
  source such as `corpus_keyword_overlap`.

Per-item failures should produce terminal checkpoint rows with `error` set.
They should not erase previous successful rows.

## Acceptance Criteria

Phase 33 validation should verify:

- `anony_chs mode=agent limit=20` produces
  `paper-benchmark-result/v1`.
- output records the required paper config and the `skip_communities=true`
  graph-artifact deviation.
- checkpoint/resume skips already completed questions without extra model calls.
- invalid/missing graph/chunks/schema paths fail before model calls.
- invalid judge output fails the item or job with
  `paper_benchmark_judge_invalid`.
- the runner does not silently fall back to `corpus_keyword_overlap` or the
  Go service `/v1/retrieve` smoke path.
- no API key appears in spec, events, checkpoint rows, stdout/stderr summaries,
  result artifact, or operation history.
- `mode=agent` and `mode=noagent` are separate runs with separate output paths.
- release-check, service-smoke, and existing benchmark gates do not regress.
