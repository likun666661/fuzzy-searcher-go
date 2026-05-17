# parse_documents Worker Contract

This document defines the Phase 17 `parse_documents` job boundary.

Go owns the service API, job lifecycle, persistence, artifact metadata, worker
process execution, and output validation. Python owns document parsing details
such as encoding fallback, PDF/DOCX extraction, markdown cleanup, and any future
OCR or LLM-assisted parsing.

## Job Request

```http
POST /v1/jobs
content-type: application/json
```

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

Minimum stable fields:

- `dataset`: dataset name.
- `document_paths`: one or more raw source documents.

Recommended fields:

- `output_path`: corpus JSON output path. Defaults to
  `$YOUTU_RAG_CORPUS_ROOT/uploaded/<dataset>/corpus.json`.
- `config_path`: Python parser config.
- `mode`: parser mode.
- `python_bin`: per-job Python executable override.
- `script_path`: per-job parse worker script override.
- `working_dir`: per-job worker cwd override.

The persisted `job.spec` must contain the resolved worker command fields used
by the runner, including `python_bin`, `script_path`, `working_dir`, and
`output_path`.

## Service Configuration

| Config | Environment | Default |
| --- | --- | --- |
| Python binary | `YOUTU_RAG_PYTHON` | `$YOUTU_RAG_ARTIFACT_ROOT/.venv/bin/python` |
| Parse documents script | `YOUTU_RAG_PARSE_DOCUMENTS_SCRIPT` | `$YOUTU_RAG_ARTIFACT_ROOT/scripts/parse_documents_worker.py` |
| Worker cwd | `YOUTU_RAG_WORKER_CWD` | `$YOUTU_RAG_ARTIFACT_ROOT` |
| Corpus root | `YOUTU_RAG_CORPUS_ROOT` | `$YOUTU_RAG_ARTIFACT_ROOT/data` |

## Worker Command

The Go runner maps the job spec to this command:

```bash
${python_bin} ${script_path} \
  --dataset "${dataset}" \
  --output "${output_path}" \
  --document "/abs/path/incoming/a.pdf" \
  --document "/abs/path/incoming/b.md"
```

Optional fields append:

| Job field | Python flag |
| --- | --- |
| `config_path` | `--config <path>` |
| `mode` | `--mode <value>` |

The worker process runs with conservative native-thread defaults unless a
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
- `output_path` exists;
- `output_path` is parseable JSON;
- stdout/stderr are captured into the inline job result.

The first service milestone accepts either the legacy corpus array shape or a
future object shape. The key contract is that downstream `build_graph` receives
a valid corpus JSON file.

## Job Artifacts

Expected artifacts:

- `document_1`, `document_2`, ...: input `source_document`, status
  `configured`.
- `corpus`: output `corpus_json`, `schema_version=corpus-json/v1`, starts
  `pending`, moves to `written` after output validation.

## Inline Result

Completed jobs return:

```json
{
  "schema_version": "parse-documents-result/v1",
  "dataset": "news_2026",
  "output_path": "/abs/path/data/uploaded/news_2026/corpus.json",
  "document_paths": ["/abs/path/incoming/a.pdf"],
  "stdout": "parsed 1 document",
  "stderr": ""
}
```

The corpus file is not embedded in `job.result`; it is tracked as an output
artifact and consumed by dataset import/build graph paths.

## Lifecycle Events

Stable event names:

- `queued`
- `running`
- `worker_started`
- `artifact_corpus_written`
- `succeeded`
- `failed`
- `interrupted`

## Acceptance Criteria

Phase 17 acceptance should verify:

- `POST /v1/jobs` accepts `type=parse_documents`.
- response envelope is `service-job/v1`.
- persisted `job.spec` contains resolved worker command and output path.
- raw document input artifacts are listed.
- corpus output artifact starts `pending`, then moves to `written` on success.
- missing corpus output marks the job failed with artifact status `missing`.
- invalid corpus JSON fails output validation.
- completed job survives service restart.
- existing dataset import, build graph, answer, workflow, and demo gates do not
  regress.
