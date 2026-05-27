# youtu-rag-service

Long-running Go service for operating Youtu-RAG.

This repository started as a small Go retriever migration, but it is now the
service shell around the original Python Youtu-RAG stack. Go owns the stable
HTTP API, configuration, dataset registry, job/workflow lifecycle, artifact
metadata, persistence, release checks, and local smoke scripts. Python remains
the model-heavy worker/sidecar layer for embedding/FAISS primitives, graph
construction, document parsing, and LLM answer generation.

Current service capabilities:

- HTTP service: health, readiness, version, retrieve, jobs, workflows,
  sidecar health, datasets, schemas, artifacts, and operation history.
- Dataset lifecycle: import prepared corpus/schema files, create datasets from
  documents, rebuild graph/chunks/cache, delete managed datasets safely, and
  audit operations after restart.
- Workers and workflows: `retrieve`, `parse_documents`, `generate_golden`,
  `build_graph`, `answer`, `build_and_answer`, and `create_dataset`.
- Retriever parity: Go-native graph/chunk loading and path1 candidate
  generation, with Python sidecar primitives for vector retrieval/reranking.
- Release surface: OpenAPI contract, profile/env validation, `.env.example`,
  fake service smoke, real demo retrieval smoke, and regression gates.

The important architecture decision is intentional: this is not a full rewrite
of every Python model component into Go. The maintainable boundary is Go for
service orchestration and Python for model-heavy execution.

## Layout

```text
cmd/youtu-retriever/          CLI entrypoint
cmd/youtu-rag-service/        Long-running HTTP service entrypoint
internal/dataset/             Graph loaders
internal/chunks/              output/chunks/*.txt loader
internal/config/              Environment-backed service configuration
internal/svc/                 HTTP routing and service orchestration
internal/jobs/                File-backed async job records
internal/workflows/           File-backed workflow records
internal/workers/             Python worker command wrappers
internal/graphtext/           Python-parity node text helpers
internal/retrieval/           Retrieve contract and deterministic core
scripts/                      Smoke, gate, oracle, and comparison scripts
docs/                         Architecture, roadmap, release, and test docs
docs/contracts/               Service/API/worker/workflow contracts
testdata/                     Phase 1 oracle fixtures
```

For a plain-language architecture walkthrough, read
`docs/architecture_overview.md`.

For the next service-oriented Youtu-RAG roadmap, read
`docs/youtu_rag_service_plan.md`. For the latest gap map against the original
Python repo and the workflow orchestration plan, read
`docs/youtu_rag_gap_map.md`. For the service testing track, read
`docs/service_test_strategy.md`. For dataset benchmark preparation and the
current Python-vs-service benchmark boundary, read `docs/benchmark_guide.md`.
The machine-readable HTTP release surface is tracked in `docs/openapi.yaml`,
with release notes in `docs/release_surface.md`.

## Test

```bash
go test ./...
```

## Service

The long-running service entrypoint follows the `ggsrv-layout` shape at a
lighter weight: `cmd/youtu-rag-service` owns process startup, `internal/config`
owns environment configuration, and `internal/svc` owns HTTP request handling.
Service profile and startup validation semantics are documented in
`docs/contracts/service_profile.md`. It currently exposes these service
surfaces:

- `GET /healthz`
- `GET /readyz`
- `GET /v1/version`
- `GET /v1/datasets`
- `POST /v1/datasets/import`
- `GET /v1/datasets/{dataset}`
- `GET /v1/datasets/{dataset}/schema`
- `PUT /v1/datasets/{dataset}/schema`
- `DELETE /v1/datasets/{dataset}`
- `POST /v1/datasets/{dataset}/rebuild`
- `GET /v1/datasets/{dataset}/artifacts`
- `GET /v1/sidecars`
- `GET /v1/sidecars/vector/health`
- `POST /v1/retrieve`
- `POST /v1/jobs`
- `GET /v1/jobs/{job_id}`
- `GET /v1/jobs/{job_id}/events`
- `POST /v1/jobs/{job_id}/cancel`
- `POST /v1/workflows`
- `GET /v1/workflows`
- `GET /v1/workflows/{workflow_id}`
- `GET /v1/workflows/{workflow_id}/steps`
- `GET /v1/workflows/{workflow_id}/steps/{step_name}`
- `GET /v1/workflows/{workflow_id}/events`
- `POST /v1/workflows/{workflow_id}/cancel`

Before starting a long-running process, validate the selected service profile:

```bash
YOUTU_RAG_PROFILE=demo \
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
YOUTU_RAG_GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json \
YOUTU_RAG_CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt \
YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765 \
YOUTU_RAG_MODE=native-path1-rerank \
make service-check
```

The `--check-config` report uses `service-config-check/v1` and exits non-zero
when a required profile dependency is missing. Profiles are:

- `local`: permissive developer default; warns on missing optional paths.
- `demo`: requires demo graph/chunks artifacts and sidecar when the mode needs
  one.
- `production`: requires artifact root, sidecar URL, worker cwd, Python binary,
  and all configured worker scripts.

Run the service with explicit demo artifacts:

```bash
YOUTU_RAG_PROFILE=demo \
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
YOUTU_RAG_GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json \
YOUTU_RAG_CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt \
YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765 \
YOUTU_RAG_MODE=native-path1-rerank \
make service-run
```

For the same demo defaults plus startup validation, use:

```bash
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765 \
make service-local
```

For a self-contained service smoke test that does not require the Python
sidecar or model-heavy workers, run:

```bash
make service-smoke
```

The smoke script creates a temporary tiny artifact root, validates startup
configuration, starts the Go service on `127.0.0.1:18080`, and exercises
health/ready, dataset/artifact registry, schema validation/update, dataset
import, retrieve, async retrieve job, and job events. Use
`SMOKE_ADDR=127.0.0.1:18081` to avoid a local port conflict, or
`KEEP_SMOKE_ARTIFACTS=1` to inspect the generated fixture files and service log.

For a real demo smoke that uses the sibling `youtu-graphrag` demo graph,
chunks, FAISS cache, and Python vector sidecar, see
`docs/contracts/real_demo_smoke.md`. That smoke validates real retrieval through
the Go HTTP service without requiring an LLM API key:

```bash
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag make demo-service-smoke
```

Set `YOUTU_RAG_VALIDATE_ON_START=true` to make `make service-run` fail before
binding HTTP when the current profile is not ready.

Then call the retrieve API:

```bash
curl -s http://127.0.0.1:8080/v1/retrieve \
  -H 'content-type: application/json' \
  -d '{
    "dataset": "demo",
    "question": "When was the person who Messi'\''s goals in Copa del Rey compared to get signed by Barcelona?",
    "top_k": 20
  }'
```

For longer-running workflows, submit the same retrieve request as an async job.
The first skeleton is process-local, with JSON job records persisted under
`YOUTU_RAG_JOB_ROOT` (default: `$YOUTU_RAG_ARTIFACT_ROOT/output/jobs`). It is
meant to lock the service contract before adding durable queues or external
workers:

```bash
curl -s http://127.0.0.1:8080/v1/jobs \
  -H 'content-type: application/json' \
  -d '{
    "type": "retrieve",
    "retrieve": {
      "dataset": "demo",
      "question": "When was the person who Messi'\''s goals in Copa del Rey compared to get signed by Barcelona?",
      "top_k": 20
    }
  }'
```

Then inspect the job and its event stream:

```bash
curl -s http://127.0.0.1:8080/v1/jobs/<job_id>
curl -s http://127.0.0.1:8080/v1/jobs/<job_id>/events
curl -s -X POST http://127.0.0.1:8080/v1/jobs/<job_id>/cancel
```

For product-level orchestration, submit a workflow. The first workflow chains a
`build_graph` child job into an `answer` child job and records artifact handoff
from graph/chunks outputs into answer inputs:

```bash
curl -s http://127.0.0.1:8080/v1/workflows \
  -H 'content-type: application/json' \
  -d '{
    "type": "build_and_answer",
    "build_and_answer": {
      "dataset": "demo",
      "question": "Who signed with Barcelona?",
      "answer_mode": "noagent",
      "top_k": 20
    }
  }'
```

Then inspect the workflow and its event stream:

```bash
curl -s http://127.0.0.1:8080/v1/workflows
curl -s http://127.0.0.1:8080/v1/workflows/<workflow_id>
curl -s http://127.0.0.1:8080/v1/workflows/<workflow_id>/steps
curl -s http://127.0.0.1:8080/v1/workflows/<workflow_id>/steps/build_graph
curl -s http://127.0.0.1:8080/v1/workflows/<workflow_id>/events
curl -s -X POST http://127.0.0.1:8080/v1/workflows/<workflow_id>/cancel
```

The same job envelope also supports Python-backed long-running workers for
document parsing, golden generation, graph construction, and final answer
generation. The Go service owns job status, persistence, artifacts, and worker
process execution; Python keeps the model-heavy logic:

```bash
curl -s http://127.0.0.1:8080/v1/jobs \
  -H 'content-type: application/json' \
  -d '{
    "type": "parse_documents",
    "parse_documents": {
      "dataset": "news_2026",
      "document_paths": ["/abs/path/incoming/a.pdf"]
    }
  }'
```

Other job types use the same endpoint: `retrieve`, `generate_golden`,
`build_graph`, `answer`, and `benchmark`.

Dataset and artifact registry endpoints report what the service can see:

```bash
curl -s http://127.0.0.1:8080/v1/datasets
curl -s http://127.0.0.1:8080/v1/datasets/demo/artifacts
curl -s http://127.0.0.1:8080/v1/datasets/demo/schema
```

Dataset lifecycle operations are recorded separately so operators can audit
imports, create_dataset workflows, rebuilds, and deletes after restart:

```bash
curl -s http://127.0.0.1:8080/v1/dataset-operations
curl -s http://127.0.0.1:8080/v1/datasets/demo/operations
curl -s http://127.0.0.1:8080/v1/dataset-operations/<operation_id>
```

Operation records persist under `YOUTU_RAG_DATASET_OPS_ROOT` by defaulting to
`$YOUTU_RAG_ARTIFACT_ROOT/output/dataset_operations`. See
`docs/contracts/dataset_operations.md`.

To import a prepared corpus/schema pair into service-managed artifact roots:

```bash
curl -s http://127.0.0.1:8080/v1/datasets/import \
  -H 'content-type: application/json' \
  -d '{
    "dataset": "news_2026",
    "corpus_path": "/abs/path/incoming/corpus.json",
    "schema_path": "/abs/path/incoming/schema.json"
  }'
```

The import copies files into `data/uploaded/<dataset>/corpus.json` and
`schemas/<dataset>.json`, writes `dataset-import/v1` metadata under
`output/datasets`, and makes the dataset visible to existing registry and
`build_graph` paths. See `docs/contracts/dataset_import.md`.

Schemas can also be managed directly through
`GET/PUT /v1/datasets/{dataset}/schema` and `POST /v1/schemas/validate`.
The schema management contract, validation rules, version/hash metadata, and
default fallback semantics are documented in
`docs/contracts/schema_management.md`.

To delete a service-managed dataset and its managed outputs:

```bash
curl -s -X DELETE 'http://127.0.0.1:8080/v1/datasets/news_2026?dry_run=true'
curl -s -X DELETE http://127.0.0.1:8080/v1/datasets/news_2026
```

Delete defaults to datasets with service metadata and only removes managed
paths. Use `force=true` to clean known managed paths when metadata is already
missing, and `include_outputs=false` to keep graph/chunks/cache/golden/trace
outputs. See `docs/contracts/dataset_lifecycle.md`.

To rebuild graph/chunks/cache for a managed dataset:

```bash
curl -s http://127.0.0.1:8080/v1/datasets/news_2026/rebuild \
  -H 'content-type: application/json' \
  -d '{"overwrite_outputs": true}'
```

Rebuild validates managed corpus/schema/metadata, optionally cleans graph,
chunks, and cache outputs, then submits a `build_graph` async job. Use
`dry_run=true` to inspect the plan without deleting outputs or creating a job.

By default the registry looks for a sibling `../youtu-graphrag` checkout. In a
clean clone elsewhere, set `YOUTU_RAG_ARTIFACT_ROOT` or the individual root
variables (`YOUTU_RAG_SCHEMA_ROOT`, `YOUTU_RAG_GRAPH_ROOT`,
`YOUTU_RAG_CHUNKS_ROOT`, `YOUTU_RAG_CACHE_ROOT`) explicitly.

Sidecar lifecycle endpoints report whether the Python vector sidecar is
configured and whether the dataset cache health endpoint is reachable:

```bash
curl -s http://127.0.0.1:8080/v1/sidecars
curl -s 'http://127.0.0.1:8080/v1/sidecars/vector/health?dataset=demo'
```

If the Python sidecar is running, run the demo parity gates:

```bash
SIDECAR_URL=http://127.0.0.1:8765 scripts/run_demo_gates.sh
```

The default demo gate paths assume `youtu-rag-service` and `youtu-graphrag`
are sibling directories. From a clean clone elsewhere, pass explicit artifact
paths:

```bash
GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json \
CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt \
GOLDEN=/abs/path/youtu-graphrag/output/retrieval_golden/demo.json \
SIDECAR_URL=http://127.0.0.1:8765 \
scripts/run_demo_gates.sh
```

## CLI

The CLI accepts either direct flags or a `retrieve` subcommand for compatibility with the migration harness:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20
```

When a Python vector sidecar is running, pass `--sidecar-url` to enable chunk FAISS retrieval:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --dataset demo \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20 \
  --sidecar-url http://127.0.0.1:8765
```

The preferred strategy entrypoint is `--mode`. Supported modes:

| Mode | Purpose |
| --- | --- |
| `native` | Go graph/chunk loader without sidecar strategy flags. |
| `sidecar` | Existing low-level sidecar chunk/triple search through `--sidecar-url`. |
| `runtime-trace` | Fetch full Python authority trace from sidecar. |
| `path2-detrace` | Fetch full trace for path1 and path2 primitive separately. |
| `primitive-merge` | Fetch path1/path2 primitives and merge in Go. |
| `rerank-merge` | Phase 8 path: fetch path1 raw candidates, rerank them through sidecar, fetch path2 primitive, and merge in Go. |
| `native-path1-rerank` | Current Phase 9 path: build path1 raw candidates in Go, rerank them through sidecar, fetch path2 primitive, and merge in Go. |

For Python-authoritative Phase 4 parity, pass the triple trace generated by
`youtu-graphrag/scripts/export_triple_trace.py`:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --dataset demo \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20 \
  --sidecar-url http://127.0.0.1:8765 \
  --triple-trace ../youtu-graphrag/output/retrieval_traces/demo_triple_trace.json
```

If the Python sidecar exposes `/v1/retrieval/triple-trace`, Go can fetch that
authority trace at runtime instead of reading a pre-generated file:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --dataset demo \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20 \
  --sidecar-url http://127.0.0.1:8765 \
  --mode runtime-trace
```

For Phase 7 primitive-merge parity, Go can fetch path1 and path2 authority
primitives separately, then merge/sort/top-k locally without using the full
triple trace:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --dataset demo \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20 \
  --sidecar-url http://127.0.0.1:8765 \
  --mode primitive-merge
```

For Phase 8 rerank-only parity, Go asks the path1 primitive for raw one-hop
candidates, sends those candidates to the rerank-only primitive, and still
merges locally with path2. This keeps Go from consuming path1's pre-reranked
authority output directly:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --dataset demo \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20 \
  --sidecar-url http://127.0.0.1:8765 \
  --mode rerank-merge
```

For Phase 9 Go-native path1 candidate generation, Go no longer calls
`/v1/retrieval/path1-triples`. It uses node/relation FAISS search plus graph
expansion to build path1 raw candidates locally, sends those candidates to
`/v1/retrieval/rerank-triples`, then merges with path2:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --dataset demo \
  --question "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?" \
  --top-k 20 \
  --sidecar-url http://127.0.0.1:8765 \
  --mode native-path1-rerank
```

It outputs a bare `RetrieveResult` JSON object:

```json
{
  "triples": [],
  "chunk_ids": [],
  "chunk_contents": [],
  "debug": {
    "strategies": []
  }
}
```

## Golden Comparison

Compare Go CLI output against a Python `retriever-golden/v1` fixture:

```bash
python3 scripts/compare_retrieval_golden.py \
  --golden ../youtu-graphrag/output/retrieval_golden/demo.json \
  --actual /tmp/go_retrieval_actual.json \
  --record-id qa_1 \
  --mode chunk
```

Available modes:

- `loader`: chunk id/content coverage, ignoring order
- `chunk`: ordered chunks plus `chunk_retrieval_results`
- `triple`: triples only
- `full`: complete normalized result

Generate a regression report:

```bash
python3 scripts/retrieval_regression_report.py \
  --golden ../youtu-graphrag/output/retrieval_golden/demo.json \
  --actual /tmp/go_retrieval_actual.json \
  --record-id qa_1
```

Current Phase 9 expected status with `--mode native-path1-rerank`: `loader`,
`chunk`, `triple`, and `full` all pass against `demo.json`; the debug strategy
should be `go_path1_rerank_path2_primitive_merge`.

The retrieval output schema is tracked in
`docs/contracts/retrieve_result.schema.json`; sidecar primitive boundaries are
documented in `docs/contracts/sidecar_primitives.md`. The long-running service
job contract is documented in `docs/contracts/service_jobs.md`; worker command
contracts are documented in `docs/contracts/generate_golden_worker.md`,
`docs/contracts/build_graph_worker.md`, and
`docs/contracts/answer_worker.md`. Graph extraction WAL, multi-runner
extraction, and replay-only community compaction are documented in
`docs/contracts/graph_construction_wal.md`,
`docs/contracts/graph_extraction_multi_runner.md`, and
`docs/contracts/graph_community_compaction.md`. The Phase 9 Go-native path1 raw
candidate parity contract is documented in
`docs/contracts/path1_candidate_generation.md`.
The Phase 14 workflow contract is documented in `docs/contracts/workflows.md`.
Dataset schema management is documented in
`docs/contracts/schema_management.md`. Dataset benchmark service contracts are
documented in `docs/contracts/benchmark_worker.md`; the stricter Phase 33
paper-aligned GraphQ/KTRetriever/Eval path is documented in
`docs/contracts/paper_aligned_benchmark.md`, with operator guidance in
`docs/benchmark_guide.md`.

## Dataset Benchmarking

For one-off dataset benchmark experiments, start with
`docs/benchmark_guide.md`. The short version is:

- `make demo-service-smoke` checks the service/retrieval demo path with real
  Python sidecar artifacts.
- The original Python `youtu-graphrag/main.py` remains the closest path to the
  paper-style dataset benchmark today because it already loops over QA pairs,
  generates answers, and runs the LLM judge.
- This Go service now has a durable `benchmark` job/workflow runner. After
  preparing AnonyRAG, a tiny real-model smoke is:

  ```bash
  scripts/prepare_anonyrag.py --artifact-root /abs/path/youtu-graphrag
  export LLM_API_KEY="${DEEPSEEK_API_KEY}"
  YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag make benchmark-smoke
  ```
- For paper-aligned AnonyRAG evaluation, use
  `docs/contracts/paper_aligned_benchmark.md`: prebuilt graph/chunks, original
  Python `GraphQ` + `KTRetriever` + `Eval`, `mode=agent` for the main run, and
  checkpoint/progress around the long loop.
- The current full AnonyRAG-CHS reference run is documented in
  `docs/benchmark_guide.md`: DeepSeek V4 Flash, completed community
  compaction, 688/688 questions, `371/688 = 53.92%` accuracy, and
  anonymized mapping F1 `0.7678`. Treat this as an industrialized
  paper-method-aligned run, not a strict reproduction of the paper's
  DeepSeek-V3-0324/Qwen3-32B numbers.

When using DeepSeek for answer/judge experiments, map the existing key into the
Youtu-RAG environment names instead of writing secrets to disk:

```bash
export LLM_API_KEY="${DEEPSEEK_API_KEY}"
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-v4-pro"
```

To prepare the public AnonyRAG dataset locally:

```bash
python3 scripts/prepare_anonyrag.py \
  --artifact-root /abs/path/youtu-graphrag
```

This downloads the Hugging Face parquet files and writes
`data/anony_chs` / `data/anony_eng` JSON files in the original
`youtu-graphrag` layout.

To prepare the other public paper datasets in the same layout:

```bash
python3 scripts/prepare_paper_datasets.py \
  --artifact-root /abs/path/youtu-graphrag
```

This downloads HotpotQA, 2WikiMultiHopQA, MuSiQue, and GraphRAG-Bench JSON
files into `youtu-graphrag/data/paper_raw/`, then writes the config-compatible
`hotpot`, `2wiki`, `musique`, and `graphrag-bench` paths documented in
`docs/benchmark_guide.md`.
