# build_graph Worker Contract

This document defines the Phase 12 `build_graph` job boundary. Go owns the
service API, job lifecycle, persistence, artifact metadata, and worker process
execution. Python owns graph construction internals such as chunking, LLM
extraction, schema-aware construction, graph formatting, and any future cache
building.

The first worker integration is command-based so it can later be replaced with
a Python sidecar endpoint or queue worker without changing the external
`service-job/v1` envelope.

Phase 28 extends this worker with a chunk-level write-ahead log (WAL) so long
LLM extraction runs can resume after interruption. The detailed WAL contract is
defined in `docs/contracts/graph_construction_wal.md`.

## Job Request

```http
POST /v1/jobs
content-type: application/json
```

```json
{
  "type": "build_graph",
  "build_graph": {
    "dataset": "demo",
    "corpus_path": "/abs/path/youtu-graphrag/data/demo/demo_corpus.json",
    "schema_path": "/abs/path/youtu-graphrag/schemas/demo.json",
    "graph_output_path": "/abs/path/youtu-graphrag/output/graphs/demo_new.json",
    "chunks_output_path": "/abs/path/youtu-graphrag/output/chunks/demo.txt",
    "cache_dir": "/abs/path/youtu-graphrag/retriever/faiss_cache_new/demo",
    "wal_path": "/abs/path/youtu-graphrag/output/graph_wal/demo.jsonl",
    "resume": true,
    "max_workers": 5
  }
}
```

Minimum stable fields:

- `dataset`: dataset name.
- `corpus_path`: input corpus JSON.
- `graph_output_path`: output graph JSON.
- `chunks_output_path`: output chunk text file.

Recommended fields:

- `schema_path`: input schema JSON.
- `cache_dir`: retrieval cache directory prepared or refreshed by the worker.
- `wal_path`: append-only graph construction WAL JSONL. Defaults under
  `output/graph_wal/{dataset}.jsonl`.
- `resume`: reuse terminal `chunk_succeeded` WAL records and retry interrupted
  chunks. Default should be true for service-managed builds.
- `max_workers`: bounded chunk extraction concurrency. WAL appends remain
  serialized by the worker.
- `skip_communities`: future optional flag to skip community/level-4 indexing
  during compaction when the smoke target is chunk WAL/resume rather than full
  community graph quality.
- `config_path`: Python config file.
- `mode`: Python construction mode such as `agent` or `noagent`.
- `python_bin`: per-job Python executable override.
- `script_path`: per-job build graph worker script override.
- `working_dir`: per-job worker cwd override.

The persisted `job.spec` must contain the resolved worker command fields used by
the runner, including `python_bin`, `script_path`, `working_dir`,
`graph_output_path`, and `chunks_output_path`.

`schema_path` should be the managed schema path resolved by the service, not an
arbitrary worker-discovered file. If a default schema fallback is used, the job
spec or artifacts must record `fallback=true`. See
`docs/contracts/schema_management.md`.

## Service Configuration

| Config | Environment | Default |
| --- | --- | --- |
| Python binary | `YOUTU_RAG_PYTHON` | `$YOUTU_RAG_ARTIFACT_ROOT/.venv/bin/python` |
| Build graph script | `YOUTU_RAG_BUILD_GRAPH_SCRIPT` | `$YOUTU_RAG_ARTIFACT_ROOT/scripts/build_graph_worker.py` |
| Worker cwd | `YOUTU_RAG_WORKER_CWD` | `$YOUTU_RAG_ARTIFACT_ROOT` |
| Corpus root | `YOUTU_RAG_CORPUS_ROOT` | `$YOUTU_RAG_ARTIFACT_ROOT/data` |
| Schema root | `YOUTU_RAG_SCHEMA_ROOT` | `$YOUTU_RAG_ARTIFACT_ROOT/schemas` |
| Graph root | `YOUTU_RAG_GRAPH_ROOT` | `$YOUTU_RAG_ARTIFACT_ROOT/output/graphs` |
| Chunks root | `YOUTU_RAG_CHUNKS_ROOT` | `$YOUTU_RAG_ARTIFACT_ROOT/output/chunks` |
| Cache root | `YOUTU_RAG_CACHE_ROOT` | `$YOUTU_RAG_ARTIFACT_ROOT/retriever/faiss_cache_new` |

## Worker Command

The Go runner maps the job spec to this command:

```bash
${python_bin} ${script_path} \
  --dataset "${dataset}" \
  --corpus "${corpus_path}" \
  --graph-output "${graph_output_path}" \
  --chunks-output "${chunks_output_path}" \
  --wal "${wal_path}" \
  --resume
```

Optional fields append:

| Job field | Python flag |
| --- | --- |
| `schema_path` | `--schema <path>` |
| `cache_dir` | `--cache-dir <path>` |
| `wal_path` | `--wal <path>` |
| `resume=true` | `--resume` |
| `max_workers` | `--max-workers <n>` |
| `config_path` | `--config <path>` |
| `mode` | `--mode <value>` |

The worker process should run with conservative native-thread defaults unless a
future worker explicitly overrides them:

```text
TOKENIZERS_PARALLELISM=false
OMP_NUM_THREADS=1
MKL_NUM_THREADS=1
VECLIB_MAXIMUM_THREADS=1
NUMEXPR_NUM_THREADS=1
```

## Output Validation

Successful worker execution requires:

- process exit code `0`;
- `wal_path` exists, is parseable JSONL, and contains a terminal
  `run_succeeded` row when WAL is configured;
- `graph_output_path` exists and is parseable JSON;
- `chunks_output_path` exists and is a file;
- stdout/stderr are captured into the inline job result for diagnosis.

If the worker exits `0` but graph/chunks output is missing, the job fails and
the relevant output artifact should move to `missing`, not `written`.

## Job Artifacts

Expected artifacts:

- `corpus`: input `corpus_json`, status `configured`.
- `schema`: input `schema_json`, status `configured`.
- `graph`: output `graph_json`, `schema_version=youtu-graph/v1`, starts
  `pending`, moves to `written` after output validation.
- `chunks`: output `chunks_txt`, starts `pending`, moves to `written`.
- `cache`: output `faiss_cache_dir`, starts `pending`; first implementation
  marks it `written` when a cache directory is configured/prepared.
- `graph_wal`: output `graph_construction_wal_jsonl`,
  `schema_version=graph-build-wal/v1`, starts `pending`, moves to `running`
  when the worker starts, and moves to `written` only after a successful or
  canceled terminal WAL row. See `docs/contracts/graph_construction_wal.md`.

## Inline Result

Completed jobs return:

```json
{
  "schema_version": "build-graph-result/v1",
  "dataset": "demo",
  "graph_output_path": "/abs/path/output/graphs/demo_new.json",
  "chunks_output_path": "/abs/path/output/chunks/demo.txt",
  "cache_dir": "/abs/path/retriever/faiss_cache_new/demo",
  "wal_path": "/abs/path/output/graph_wal/demo.jsonl",
  "stdout": "{\"ok\": true}",
  "stderr": ""
}
```

The graph/chunk files are not embedded in `job.result`; they are tracked as
artifacts.

## Lifecycle Events

Stable event names:

- `queued`
- `running`
- `worker_started`
- `graph_wal_started`
- `graph_wal_resumed`
- `graph_chunk_started`
- `graph_chunk_succeeded`
- `graph_chunk_failed`
- `graph_compaction_started`
- `artifact_graph_written`
- `graph_wal_failed`
- `succeeded`
- `failed`
- `interrupted`

## Acceptance Criteria

Phase 12 acceptance should verify:

- `POST /v1/jobs` accepts `type=build_graph`.
- response envelope is `service-job/v1`.
- persisted `job.spec` contains resolved worker command and artifact paths.
- graph/chunks/cache output artifacts start as `pending`.
- graph WAL artifact starts as `pending`, moves to `running`, and ends as
  `written` or `failed` according to the WAL terminal state.
- success marks graph/chunks/cache `written` and survives service restart.
- resume skips chunks with existing `chunk_succeeded` WAL records.
- interrupted chunks with `chunk_started` but no terminal row are retried.
- malformed or stale WAL rows fail with explicit WAL errors.
- missing graph/chunks output marks the job failed with artifact status
  `missing`.
- invalid graph JSON fails output validation.
- existing retrieve and `generate_golden` gates do not regress.
