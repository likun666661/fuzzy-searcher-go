# Answer Worker Contract

`answer` is the service job boundary for final answer generation. The Go
service owns the job API, lifecycle, persistence, worker process, stdout/stderr
capture, and artifact metadata. Python owns the model-heavy workflow:
retrieval-time reasoning, decomposition, IRCoT/no-agent answer generation, and
LLM calls.

The first integration is command-based so it can later be replaced by a Python
sidecar endpoint or queue worker without changing the external service job
shape.

## Job Spec

Submit an answer job through `POST /v1/jobs`:

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

Stable fields:

- `dataset`: dataset name. Defaults to the service default dataset.
- `question`: user question. Required.
- `output_path`: answer JSON path. Defaults to
  `$YOUTU_RAG_ARTIFACT_ROOT/output/answers/{dataset}.json`.
- `mode`: Python answer mode, such as `noagent`, `ircot`, or a future project
  mode. Defaults to `YOUTU_RAG_MODE`.
- `top_k`: retrieval depth passed to the worker. Defaults to `20`.
- `graph_path`: graph artifact path. Defaults from the dataset artifact
  registry.
- `chunks_path`: chunk artifact path. Defaults from the dataset artifact
  registry.
- `config_path`: optional Python config path.
- `involved_types`: optional JSON string forwarded to Python.
- `python_bin`: per-job Python binary override.
- `script_path`: per-job answer worker script override.
- `working_dir`: per-job worker cwd override.

The persisted `job.spec` contains resolved worker command fields so a failed
job can be diagnosed without reconstructing service environment variables.

## Service Configuration

The Go service resolves worker defaults from environment-backed config:

| Purpose | Environment variable | Default |
| --- | --- | --- |
| Artifact root | `YOUTU_RAG_ARTIFACT_ROOT` | `../youtu-graphrag` |
| Python binary | `YOUTU_RAG_PYTHON` | `$YOUTU_RAG_ARTIFACT_ROOT/.venv/bin/python` |
| Answer script | `YOUTU_RAG_ANSWER_SCRIPT` | `$YOUTU_RAG_ARTIFACT_ROOT/scripts/answer_worker.py` |
| Worker cwd | `YOUTU_RAG_WORKER_CWD` | `$YOUTU_RAG_ARTIFACT_ROOT` |

## Worker Command

Go invokes:

```bash
$python_bin $script_path \
  --dataset "$dataset" \
  --question "$question" \
  --output "$output_path" \
  --mode "$mode" \
  --top-k "$top_k" \
  --graph "$graph_path" \
  --chunks "$chunks_path"
```

Optional flags are included only when configured:

- `--config "$config_path"`
- `--involved-types "$involved_types"`

The service sets conservative native-thread defaults:

```text
TOKENIZERS_PARALLELISM=false
OMP_NUM_THREADS=1
MKL_NUM_THREADS=1
VECLIB_MAXIMUM_THREADS=1
NUMEXPR_NUM_THREADS=1
```

## Output Artifact

Successful worker execution requires an answer JSON file at `output_path` with
this minimum shape:

```json
{
  "schema_version": "answer-output/v1",
  "dataset": "demo",
  "question": "Who signed with Barcelona?",
  "answer": "..."
}
```

The Python worker can include extra fields such as citations, retrieved
chunks, reasoning traces, decomposition steps, prompts, token counts, or model
metadata. The Go service validates only `schema_version=answer-output/v1` at
this layer; richer answer-output schema validation can be added later.

## Inline Job Result

Completed jobs return a compact inline result:

```json
{
  "schema_version": "answer-result/v1",
  "dataset": "demo",
  "question": "Who signed with Barcelona?",
  "output_path": "/abs/path/youtu-graphrag/output/answers/demo.json",
  "stdout": "{...}",
  "stderr": ""
}
```

The answer body remains on disk as the `answer` artifact.

## Artifact States

`answer` jobs report:

- `graph`: optional input `graph_json`, status `configured`.
- `chunks`: optional input `chunks_txt`, status `configured`.
- `answer`: output `answer_json`, `schema_version=answer-output/v1`.

Answer artifact status transitions:

- `pending`: job accepted.
- `running`: worker process started.
- `written`: worker succeeded and answer output passed schema validation.
- `missing`: worker exited `0` but the expected answer output was absent.
- `failed`: worker process failed or output schema validation failed.

## Events

Expected job events:

- `queued`
- `running`
- `worker_started`
- `artifact_answer_written` on success
- `succeeded`, `failed`, or `canceled`

Failure events must preserve stderr or validation details in the job `error`
field.

## Acceptance Criteria

Phase 13 validation should assert:

- `POST /v1/jobs` accepts `type=answer` and returns `service-job/v1`.
- persisted `job.spec` contains resolved `dataset/question/output_path/mode`
  and worker command fields.
- success moves `answer` artifact to `written` and survives service restart.
- worker failure moves `answer` artifact to `failed` and preserves stderr in
  `job.error` and events.
- worker success without output moves `answer` artifact to `missing`.
- output with any schema other than `answer-output/v1` fails the job.
