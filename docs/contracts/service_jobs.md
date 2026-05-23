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
- `type`: job type. Current implemented types are `retrieve`,
  `parse_documents`, `generate_golden`, `build_graph`, `answer`, and
  `benchmark`.
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

## Implemented Job: generate_golden

`generate_golden` turns the existing Python retriever golden generator into a
tracked service job. Go owns the job envelope, lifecycle events, persistence,
worker command execution, stdout/stderr capture, and artifact metadata. Python
continues to own the retriever runtime and fixture generation logic.

Submit:

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

The service stores this as a typed `GenerateGoldenSpec` in `job.spec`.
Minimum stable fields are `dataset`, `limit`, and `output_path`. The resolved
worker command fields are also persisted:

```json
{
  "dataset": "demo",
  "output_path": "/abs/path/youtu-graphrag/output/retrieval_golden/demo.json",
  "limit": 1,
  "python_bin": "/abs/path/youtu-graphrag/.venv/bin/python",
  "script_path": "/abs/path/youtu-graphrag/scripts/generate_retriever_golden.py",
  "working_dir": "/abs/path/youtu-graphrag"
}
```

`generate_golden` jobs report a `golden_fixture` output artifact:

- `name=golden_fixture`
- `role=output`
- `kind=retriever_golden_json`
- `schema_version=retriever-golden/v1`
- `path=job.spec.output_path`
- `status=pending` while the job is running and `written` after successful
  output validation

Completed jobs return a small inline `generate-golden-result/v1` result with
the dataset, output path, and captured stdout/stderr. The full fixture remains
on disk as the `golden_fixture` artifact.

The detailed Python worker command contract is defined in
`docs/contracts/generate_golden_worker.md`.

## Implemented Job: parse_documents

`parse_documents` turns raw source files into a tracked corpus JSON artifact.
Go owns the job envelope, lifecycle events, persistence, worker command
execution, stdout/stderr capture, and artifact metadata. Python continues to
own document parsing internals such as encoding fallback, PDF/DOCX extraction,
markdown cleanup, OCR, and future parser-specific dependencies.

Submit:

```json
{
  "type": "parse_documents",
  "parse_documents": {
    "dataset": "news_2026",
    "document_paths": [
      "/abs/path/incoming/a.pdf",
      "/abs/path/incoming/b.md"
    ],
    "output_path": "/abs/path/youtu-graphrag/data/uploaded/news_2026/corpus.json"
  }
}
```

The service stores this as a typed `ParseDocumentsSpec` in `job.spec`. The
resolved worker command fields are also persisted:

```json
{
  "dataset": "news_2026",
  "document_paths": ["/abs/path/incoming/a.pdf"],
  "output_path": "/abs/path/youtu-graphrag/data/uploaded/news_2026/corpus.json",
  "python_bin": "/abs/path/youtu-graphrag/.venv/bin/python",
  "script_path": "/abs/path/youtu-graphrag/scripts/parse_documents_worker.py",
  "working_dir": "/abs/path/youtu-graphrag"
}
```

`parse_documents` jobs report:

- `document_1`, `document_2`, ... input artifacts with `kind=source_document`.
- `corpus` output artifact with `kind=corpus_json`,
  `schema_version=corpus-json/v1`, and status `pending` -> `written`,
  `missing`, or `failed`.

Completed jobs return a small inline `parse-documents-result/v1` result with
the dataset, output path, document paths, and captured stdout/stderr. The corpus
JSON remains on disk as the `corpus` artifact and can feed dataset import or
`build_graph`.

The detailed Python worker command contract is defined in
`docs/contracts/parse_documents_worker.md`.

## Implemented Job: build_graph

`build_graph` turns Python graph construction into a tracked service job. Go
owns the job envelope, lifecycle events, persistence, worker command execution,
stdout/stderr capture, and artifact metadata. Python continues to own chunking,
LLM extraction, schema-aware graph construction, and graph/chunk file writing.

Submit:

```json
{
  "type": "build_graph",
  "build_graph": {
    "dataset": "demo",
    "corpus_path": "/abs/path/youtu-graphrag/data/demo/demo_corpus.json",
    "schema_path": "/abs/path/youtu-graphrag/schemas/demo.json",
    "graph_output_path": "/abs/path/youtu-graphrag/output/graphs/demo_new.json",
    "chunks_output_path": "/abs/path/youtu-graphrag/output/chunks/demo.txt",
    "cache_dir": "/abs/path/youtu-graphrag/retriever/faiss_cache_new/demo"
  }
}
```

The service stores this as a typed `BuildGraphSpec` in `job.spec`. The resolved
worker command fields are persisted with the job:

```json
{
  "dataset": "demo",
  "corpus_path": "/abs/path/youtu-graphrag/data/demo/demo_corpus.json",
  "schema_path": "/abs/path/youtu-graphrag/schemas/demo.json",
  "graph_output_path": "/abs/path/youtu-graphrag/output/graphs/demo_new.json",
  "chunks_output_path": "/abs/path/youtu-graphrag/output/chunks/demo.txt",
  "cache_dir": "/abs/path/youtu-graphrag/retriever/faiss_cache_new/demo",
  "python_bin": "/abs/path/youtu-graphrag/.venv/bin/python",
  "script_path": "/abs/path/youtu-graphrag/scripts/build_graph_worker.py",
  "working_dir": "/abs/path/youtu-graphrag"
}
```

`build_graph` jobs report these artifacts:

- `corpus`: input `corpus_json`, status `configured`.
- `schema`: input `schema_json`, status `configured`.
- `graph`: output `graph_json`, `schema_version=youtu-graph/v1`, starts
  `pending`, moves to `written`.
- `chunks`: output `chunks_txt`, starts `pending`, moves to `written`.
- `cache`: output `faiss_cache_dir`, starts `pending`, moves to `written`
  when prepared by the runner.
- `graph_wal`: output `graph_construction_wal_jsonl`,
  `schema_version=graph-build-wal/v1`, starts `pending`, moves to `running`
  while Python extracts chunks, and moves to `written` after the WAL contains a
  terminal run row. The WAL is append-only durable partial state for expensive
  chunk extraction.

Completed jobs return a small inline `build-graph-result/v1` result with
dataset, graph output path, chunks output path, cache dir, and captured
stdout/stderr. The large graph/chunks artifacts remain on disk.

The detailed Python worker command contract is defined in
`docs/contracts/build_graph_worker.md`. The Phase 28 resumability contract is
defined in `docs/contracts/graph_construction_wal.md`.

## Implemented Job: answer

`answer` turns final answer generation into a tracked service job. Go owns the
job envelope, lifecycle events, persistence, worker command execution,
stdout/stderr capture, and artifact metadata. Python continues to own
retrieval-time reasoning, decomposition/IRCoT/no-agent logic, prompts, LLM
calls, and final answer file writing.

Submit:

```json
{
  "type": "answer",
  "answer": {
    "dataset": "demo",
    "question": "Who signed with Barcelona?",
    "mode": "noagent",
    "top_k": 20,
    "graph_path": "/abs/path/youtu-graphrag/output/graphs/demo_new.json",
    "chunks_path": "/abs/path/youtu-graphrag/output/chunks/demo.txt",
    "output_path": "/abs/path/youtu-graphrag/output/answers/demo.json"
  }
}
```

The service stores this as a typed `AnswerSpec` in `job.spec`. The resolved
worker command fields are persisted with the job:

```json
{
  "dataset": "demo",
  "question": "Who signed with Barcelona?",
  "output_path": "/abs/path/youtu-graphrag/output/answers/demo.json",
  "mode": "noagent",
  "top_k": 20,
  "graph_path": "/abs/path/youtu-graphrag/output/graphs/demo_new.json",
  "chunks_path": "/abs/path/youtu-graphrag/output/chunks/demo.txt",
  "python_bin": "/abs/path/youtu-graphrag/.venv/bin/python",
  "script_path": "/abs/path/youtu-graphrag/scripts/answer_worker.py",
  "working_dir": "/abs/path/youtu-graphrag"
}
```

`answer` jobs report these artifacts:

- `graph`: optional input `graph_json`, status `configured`.
- `chunks`: optional input `chunks_txt`, status `configured`.
- `answer`: output `answer_json`, `schema_version=answer-output/v1`, starts
  `pending`, moves to `written` after successful output validation.

Completed jobs return a small inline `answer-result/v1` result with dataset,
question, answer output path, and captured stdout/stderr. The answer payload
itself remains on disk as the `answer` artifact.

The detailed Python worker command contract is defined in
`docs/contracts/answer_worker.md`.

## Implemented Job: benchmark

`benchmark` turns dataset QA evaluation into a tracked service job. Go owns the
job envelope, lifecycle events, persistence, worker command execution,
stdout/stderr capture, and artifact metadata. Python continues to own
retrieval/answer/judge internals and LLM calls.

Submit:

```json
{
  "type": "benchmark",
  "benchmark": {
    "dataset": "anony_eng",
    "qa_path": "/abs/path/youtu-graphrag/data/anony_eng/final_qa_pairs.json",
    "output_path": "/abs/path/youtu-graphrag/output/benchmarks/anony_eng_smoke.json",
    "limit": 20,
    "mode": "noagent",
    "retrieve_url": "http://127.0.0.1:18083",
    "retrieve_mode": "native-path1-rerank",
    "sidecar_url": "http://127.0.0.1:8765",
    "top_k": 20,
    "answer_model": "deepseek-v4-pro",
    "judge_model": "deepseek-v4-pro",
    "llm_base_url": "https://api.deepseek.com"
  }
}
```

Benchmark jobs report `qa` and optional graph/chunks/schema/cache input
artifacts plus a `benchmark_result` output artifact with
`schema_version=benchmark-result/v1`. Completed jobs return a compact inline
`benchmark-job-result/v1` summary; the full item-level result stays on disk.
Long benchmark runs should emit `benchmark_progress` events and may write a
`benchmark_checkpoint` JSONL artifact for resume.

For service-backed GraphRAG retrieval evaluation, set `retrieve_url` and
`retrieve_mode`. Without those fields, the default worker may use a lightweight
corpus keyword-overlap context baseline; that mode is useful for service smoke
but must not be reported as the Youtu-GraphRAG/rerank chain.

For paper-aligned evaluation, use the stricter Phase 33 contract in
`docs/contracts/paper_aligned_benchmark.md`. That path must call the original
Python GraphQ + KTRetriever + Eval chain and should emit
`paper-benchmark-result/v1`.

The detailed Python worker and workflow contract is defined in
`docs/contracts/benchmark_worker.md`.

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
