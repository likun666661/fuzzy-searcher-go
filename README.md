# fuzzy-searcher-go

Go retriever scaffold for the Youtu-GraphRAG migration.

This repository is an incremental Go migration of the large Python retriever
core. The current implementation can run through several migration modes:

- native Go graph/chunk loading with Python vector sidecar support
- trace-backed parity mode, where Python emits an authoritative
  `triple-trace/v1` file and Go uses it to match the current Python public
  retrieval output exactly

- load Youtu-GraphRAG graph JSON and chunk files
- expose a stable `RetrieveRequest` / `RetrieveResult` contract
- provide a deterministic Go retriever core
- consume Python sidecar chunk/triple retrieval output
- provide a CLI for black-box comparison against Python golden fixtures/traces
- provide harness/docs for migration testing

The trace-backed path is intentionally a migration bridge. It keeps the public
contract green while native Go path1/path2 triple rerank behavior is replaced
piece by piece.

## Layout

```text
cmd/youtu-retriever/          CLI entrypoint
internal/dataset/             Graph loaders
internal/chunks/              output/chunks/*.txt loader
internal/graphtext/           Python-parity node text helpers
internal/retrieval/           Retrieve contract and deterministic core
scripts/                      Oracle export and golden comparison scripts
docs/                         Migration test plan
docs/contracts/               JSON/schema and sidecar primitive contracts
testdata/                     Phase 1 oracle fixtures
```

For a plain-language architecture walkthrough, read
`docs/architecture_overview.md`.

For the next service-oriented Youtu-RAG roadmap, read
`docs/youtu_rag_service_plan.md`. For the service testing track, read
`docs/service_test_strategy.md`.

## Test

```bash
go test ./...
```

## Service

The long-running service entrypoint follows the `ggsrv-layout` shape at a
lighter weight: `cmd/youtu-rag-service` owns process startup, `internal/config`
owns environment configuration, and `internal/svc` owns HTTP request handling.
It currently exposes the first service milestone:

- `GET /healthz`
- `GET /readyz`
- `GET /v1/version`
- `GET /v1/datasets`
- `GET /v1/datasets/{dataset}`
- `GET /v1/datasets/{dataset}/artifacts`
- `GET /v1/sidecars`
- `GET /v1/sidecars/vector/health`
- `POST /v1/retrieve`
- `POST /v1/jobs`
- `GET /v1/jobs/{job_id}`
- `GET /v1/jobs/{job_id}/events`
- `POST /v1/jobs/{job_id}/cancel`

Run it with explicit demo artifacts:

```bash
YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag \
YOUTU_RAG_GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json \
YOUTU_RAG_CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt \
YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765 \
YOUTU_RAG_MODE=native-path1-rerank \
make service-run
```

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

Dataset and artifact registry endpoints report what the service can see:

```bash
curl -s http://127.0.0.1:8080/v1/datasets
curl -s http://127.0.0.1:8080/v1/datasets/demo/artifacts
```

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

The default demo gate paths assume `fuzzy-searcher-go` and `youtu-graphrag`
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
contracts are documented in `docs/contracts/generate_golden_worker.md` and
`docs/contracts/build_graph_worker.md`. The Phase 9 Go-native path1 raw
candidate parity contract is documented in
`docs/contracts/path1_candidate_generation.md`.
