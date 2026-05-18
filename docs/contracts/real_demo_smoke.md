# Real Demo Service Smoke Contract

This document defines the Phase 25 real demo smoke boundary. It is different
from `make service-smoke`: the existing service smoke uses tiny fake artifacts
and no Python sidecar, while this smoke uses the real `youtu-graphrag` demo
artifacts and the Python vector sidecar.

The goal is not to call a real LLM yet. The goal is to prove that the Go
service, Python sidecar, real demo graph/chunks/cache, and HTTP retrieval path
work together end to end.

## Required Checkouts

The default local layout is sibling directories:

```text
workspace-for-youtu-graph/
  fuzzy-searcher-go/
  youtu-graphrag/
```

The smoke may run from any location if explicit paths are provided.

Required `youtu-graphrag` paths:

| Artifact | Default path |
| --- | --- |
| artifact root | `../youtu-graphrag` |
| Python executable | `../youtu-graphrag/.venv/bin/python` |
| sidecar script | `../youtu-graphrag/scripts/vector_sidecar.py` |
| demo graph | `../youtu-graphrag/output/graphs/demo_new.json` |
| demo chunks | `../youtu-graphrag/output/chunks/demo.txt` |
| demo schema | `../youtu-graphrag/schemas/demo.json` |
| demo FAISS/cache dir | `../youtu-graphrag/retriever/faiss_cache_new/demo` |
| optional golden fixture | `../youtu-graphrag/output/retrieval_golden/demo.json` |

The sidecar must be able to load sentence-transformers, torch, FAISS, and the
demo cache. If Python dependencies are missing, the smoke should fail before
starting the Go service and print the missing dependency or sidecar health
error.

## Environment

Stable environment variables for the smoke:

| Variable | Default | Meaning |
| --- | --- | --- |
| `YOUTU_RAG_ARTIFACT_ROOT` | `../youtu-graphrag` | Real Python repo/artifact root. |
| `YOUTU_RAG_GRAPH` | `$YOUTU_RAG_ARTIFACT_ROOT/output/graphs/demo_new.json` | Demo graph path. |
| `YOUTU_RAG_CHUNKS` | `$YOUTU_RAG_ARTIFACT_ROOT/output/chunks/demo.txt` | Demo chunks path. |
| `YOUTU_RAG_DATASET` | `demo` | Dataset id. |
| `YOUTU_RAG_MODE` | `native-path1-rerank` | Current real demo service mode. |
| `YOUTU_RAG_PROFILE` | `demo` | Strict demo startup profile. |
| `YOUTU_RAG_SIDECAR_URL` | `http://127.0.0.1:8765` | Python vector sidecar URL. |
| `YOUTU_RAG_HTTP_ADDR` | `127.0.0.1:18082` | Go service smoke address. |
| `YOUTU_RAG_VALIDATE_ON_START` | `true` | Fail fast on missing demo config. |
| `DEMO_SMOKE_QUESTION` | current demo QA question | Question sent to `/v1/retrieve`. |
| `DEMO_SMOKE_TOP_K` | `20` | Retrieval top_k. |

The canonical demo question is:

```text
When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?
```

The smoke must not require `DEEPSEEK_API_KEY` or any LLM key. It exercises
retrieval, not answer generation.

## Startup Order

The one-command real demo smoke should perform these steps:

1. Resolve and print the artifact root, graph, chunks, schema, cache, sidecar
   URL, and Go service URL.
2. Validate required files/directories exist.
3. Start the Python sidecar unless an already-running sidecar at
   `YOUTU_RAG_SIDECAR_URL` passes `/v1/datasets/demo/cache`.
4. Wait for sidecar cache health to report the demo cache is usable.
5. Run `go run ./cmd/youtu-rag-service --check-config`.
6. Start the Go service with `YOUTU_RAG_PROFILE=demo`,
   `YOUTU_RAG_MODE=native-path1-rerank`, and the resolved graph/chunks/sidecar.
7. Wait for `/healthz` and `/readyz`.
8. Call `/v1/sidecars/vector/health?dataset=demo`.
9. Call `/v1/retrieve` with the demo question.
10. Optionally submit an async `retrieve` job and wait for it to succeed.
11. Stop processes that the smoke started and print log locations when it
    fails.

If the sidecar or Go service was already running before the smoke, the script
should avoid killing that pre-existing process.

## Expected Retrieval Contract

The synchronous request:

```json
{
  "dataset": "demo",
  "question": "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?",
  "top_k": 20,
  "mode": "native-path1-rerank"
}
```

Expected response shape:

- response is HTTP `200`;
- response contains `triples`, `chunk_ids`, `chunk_contents`,
  `chunk_retrieval_results`, and `debug`;
- `chunk_ids` contains the real demo ids `0FCIUkTr`, `CbHylu8o`, and
  `rpQTmzHn`;
- `chunk_retrieval_results` has length `3`;
- `triples` has length `17` for the current demo golden;
- `debug.strategies` includes the current native path1/rerank/path2 merge
  strategy, expected as `go_path1_rerank_path2_primitive_merge` unless the Go
  service intentionally renames it in a documented phase;
- no `--triple-trace` file is required.

If `output/retrieval_golden/demo.json` is present, the smoke may run the
existing regression report and require `loader/chunk/triple/full` zero diff.
If the golden fixture is missing, the smoke should still validate the shape and
chunk/triple counts above.

## Async Job Contract

The optional async job request uses the same retrieval payload:

```json
{
  "type": "retrieve",
  "retrieve": {
    "dataset": "demo",
    "question": "When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?",
    "top_k": 20,
    "mode": "native-path1-rerank"
  }
}
```

Expected job behavior:

- `POST /v1/jobs` returns HTTP `202` and a `service-job/v1` record;
- job reaches `succeeded`;
- job result contains a retrieve result matching the synchronous shape;
- `GET /v1/jobs/{job_id}/events` includes lifecycle events.

## Failure Diagnostics

The smoke should fail with a focused diagnostic before continuing when:

- `youtu-graphrag` artifact root is missing;
- `.venv/bin/python` is missing or cannot import required sidecar dependencies;
- `scripts/vector_sidecar.py` is missing;
- graph/chunks/schema/cache artifacts are missing;
- sidecar starts but `/v1/datasets/demo/cache` fails or reports missing cache;
- Go config check fails;
- `/readyz` is not ready;
- `/v1/retrieve` fails or returns a shape inconsistent with this contract.

On failure, the smoke should print:

- resolved artifact paths;
- sidecar URL and Go service URL;
- sidecar log path when the script started sidecar;
- Go service log path;
- the failed HTTP status/body when available.

## Non-Goals

- No graph construction.
- No LLM answer generation.
- No API key requirement.
- No mutation of real demo graph/chunks/cache/golden artifacts.
- No benchmark/latency threshold beyond "completes within the smoke timeout".

## Acceptance Criteria

Phase 25 validation should verify:

- missing artifact/root cases fail with explicit diagnostics;
- sidecar cache health is checked before Go retrieval;
- Go service starts under `demo` profile with strict validation;
- synchronous `/v1/retrieve` succeeds against real demo artifacts;
- optional async retrieve job succeeds against real demo artifacts;
- existing `make service-smoke`, `make release-check`, `go test ./...`, and
  demo gate scripts do not regress.

