# Youtu-RAG Gap Map And Phase 14 Plan

This document compares the current Go service with the original
`youtu-graphrag` repository and defines the next service-oriented step.

The goal is not a full Python-to-Go rewrite. The goal is a long-running,
maintainable Go service that can operate the Youtu-RAG workflow while keeping
model-heavy Python pieces behind explicit worker and sidecar contracts.

## Current Go Service State

The current Go repo already covers the core service shell:

- HTTP service entrypoint: `cmd/youtu-rag-service`.
- Dataset and artifact discovery:
  - `GET /v1/datasets`
  - `GET /v1/datasets/{dataset}`
  - `GET /v1/datasets/{dataset}/artifacts`
- Sidecar health:
  - `GET /v1/sidecars`
  - `GET /v1/sidecars/vector/health`
- Retrieval:
  - sync `POST /v1/retrieve`
  - async `retrieve` job
  - current main retrieval mode: `native-path1-rerank`
- Long-running jobs:
  - `retrieve`
  - `generate_golden`
  - `build_graph`
  - `answer`
- File-backed job records under `YOUTU_RAG_JOB_ROOT`.
- Stable job envelope: `service-job/v1`.
- Stable artifact metadata for inputs and outputs.
- Regression gates around worker success, failure, missing output, bad schema,
  restart readback, and sidecar/no-fallback behavior.

This means the Go repo is no longer just a fuzzy-search CLI. It is now a
service shell with several durable job types.

## What The Original Python Repo Still Has

The original `youtu-graphrag` repo still has several product capabilities that
are not fully represented by the Go service.

### 1. Upload And Dataset Creation

Python source:

- `backend.py`
- `utils/document_parser.py`

Original behavior:

- Accepts uploaded `.txt`, `.md`, `.json`, `.pdf`, `.docx`, and `.doc` files.
- Decodes text with encoding fallback.
- Parses PDF/DOCX/DOC through optional parser stacks.
- Creates `data/uploaded/<dataset>/corpus.json`.
- Invents dataset names and avoids collisions.

Current Go state:

- Go can discover datasets and artifacts from filesystem roots.
- Go does not yet own upload, document parsing, corpus writing, or dataset
  creation.

Recommended boundary:

- Go should own upload API, dataset identity, artifact metadata, and job/status
  records.
- Python can remain a `parse_documents` worker for complex PDF/DOC parsing.

### 2. Schema Management

Python source:

- `backend.py`
- `schemas/*.json`
- `config/base_config.yaml`

Original behavior:

- Upload custom schema for non-demo datasets.
- Pick dataset-specific schema when available.
- Fall back to demo schema.

Current Go state:

- Go artifact registry can see schema files.
- Go does not yet expose schema upload/update/delete APIs.
- Go does not validate schema content beyond file presence.

Recommended boundary:

- Go should own schema CRUD and schema artifact status.
- Python workers should consume schema paths from Go-resolved job specs.

### 3. Graph Construction

Python source:

- `models/constructor/kt_gen.py`
- `utils/tree_comm.py`
- `backend.py`
- `main.py`

Original behavior:

- Clears old graph/chunk/cache artifacts.
- Chunks corpus.
- Calls LLM to extract attributes, triples, entity types, and optional schema
  evolution.
- Deduplicates triples.
- Builds community / super-node structure.
- Writes graph, chunks, and FAISS-ready cache inputs.
- Streams construction progress to WebSocket clients.

Current Go state:

- Go has a `build_graph` job that can execute a Python worker command.
- Go records graph/chunks/cache artifacts and validates graph/chunks outputs.
- Go does not yet define or run a parent workflow that clears stale artifacts,
  builds graph, then triggers downstream retrieval/golden/answer steps.
- Go does not yet stream progress externally.

Recommended boundary:

- Keep Python graph construction.
- Put graph construction inside a Go-owned workflow step with explicit artifact
  handoff and failure semantics.

### 4. Retrieval Core

Python source:

- `models/retriever/enhanced_kt_retriever.py`
- `models/retriever/faiss_filter.py`
- `scripts/vector_sidecar.py`

Original behavior:

- Builds/loads node/relation/triple/community/chunk FAISS caches.
- Searches path1 and path2.
- Reranks triples.
- Retrieves chunks.
- Formats retrieval output for prompts and UI.

Current Go state:

- This is the most mature migrated part.
- Go loads graph/chunks.
- Go calls vector sidecar for embeddings/FAISS/rerank/path2.
- Go now builds path1 raw candidates natively.
- Demo `loader/chunk/triple/full` gates are green.

Remaining gap:

- Retrieval is integrated as a standalone API/job, but not yet as a step inside
  a larger workflow.

Recommended boundary:

- Do not spend Phase 14 on more native retrieval internals.
- Use retrieval as a workflow step and protect existing gates.

### 5. Question Decomposition

Python source:

- `models/retriever/agentic_decomposer.py`
- `main.py`
- `backend.py`

Original behavior:

- Reads schema.
- Calls LLM to split complex questions into sub-questions.
- Returns involved node/relation/attribute types.
- Falls back to the original question when decomposition fails.

Current Go state:

- There is no first-class `decompose` job yet.
- `answer` job can hide decomposition inside a Python worker.

Recommended boundary:

- For service maintainability, create a future `decompose` worker contract only
  if we need inspectable sub-question artifacts.
- Do not migrate LLM decomposition logic into Go.

### 6. Answer Generation And IRCoT

Python source:

- `main.py`
- `backend.py`
- `utils/call_llm_api.py`

Original behavior:

- No-agent mode:
  - decompose question
  - retrieve per sub-question
  - merge triples/chunks
  - generate final answer
- Agent mode:
  - run initial no-agent retrieval/answer
  - iterate IRCoT steps
  - LLM either produces final answer or a new query
  - new query triggers additional retrieval
- Returns reasoning steps and visualization payloads in the web backend.

Current Go state:

- Go has an `answer` job that can execute a Python answer worker.
- Go records answer artifacts and validates `answer-output/v1`.
- Go does not yet make decomposition / retrieval / IRCoT steps visible as
  workflow steps.

Recommended boundary:

- Keep LLM answer generation in Python.
- Expose answer generation as either:
  - a single `answer` worker, for simple product integration; or
  - a workflow composed from `decompose`, `retrieve`, and `answer` steps, when
    step-level observability matters.

### 7. Web UI Progress And Visualization

Python source:

- `backend.py`
- `frontend/index.html`

Original behavior:

- WebSocket progress for upload, graph construction, reconstruction, and QA.
- Graph visualization endpoint.
- Sub-question visualization.
- Retrieved triples graph.
- Reasoning flow timeline.

Current Go state:

- Go has job events and artifact metadata.
- Go does not yet expose WebSocket/SSE progress.
- Go has no graph visualization endpoint.
- Go has no frontend integration plan.

Recommended boundary:

- Start with job events as the durable progress source.
- Add SSE/WebSocket later as a presentation layer over workflow/job events.
- Add graph visualization endpoint after workflow artifacts are reliable.

### 8. Evaluation

Python source:

- `utils/eval.py`
- `main.py`

Original behavior:

- Calls an LLM judge to compare generated answers with gold answers.

Current Go state:

- No `evaluate` job yet.

Recommended boundary:

- Add later as an optional worker job type.
- It should consume `answer` artifact and gold QA artifact, then produce an
  `evaluation-result/v1` artifact.

## Main Missing Piece Now

The biggest missing piece is not another individual worker.

The missing piece is workflow orchestration.

Today the Go service can run individual jobs:

```text
build_graph
generate_golden
retrieve
answer
```

But the original product workflow is closer to:

```text
upload/prepare corpus
  -> optionally upload/choose schema
  -> build graph
  -> build retrieval cache / generate golden / retrieve
  -> answer
  -> show progress, artifacts, reasoning, and final result
```

If these steps stay disconnected, the service will be hard to operate:

- users must know which job to run next;
- artifact paths must be copied manually;
- failures do not clearly tell which downstream steps are invalid;
- restart recovery is per job, not per workflow;
- there is no parent object that says "this dataset/question run is done".

## Phase 14 Plan: Workflow Layer

Phase 14 should add a workflow layer above jobs.

Detailed workflow contract: `docs/contracts/workflows.md`.

### Goal

Introduce a stable workflow contract that can chain existing job types without
rewriting their internals.

The workflow layer should answer:

- What overall operation is being performed?
- Which child jobs ran?
- Which artifacts passed from one step to the next?
- Which step failed?
- If the process restarts, what can be recovered?
- If a workflow is canceled, which child jobs should be canceled?

### First Workflow Type: `build_and_answer`

The first workflow can be minimal:

```text
build_and_answer
  step 1: build_graph
  step 2: answer
```

Inputs:

- `dataset`
- `question`
- optional `corpus_path`
- optional `schema_path`
- optional `graph_output_path`
- optional `chunks_output_path`
- optional `cache_dir`
- optional `answer_output_path`
- optional answer `mode` and `top_k`

Artifact handoff:

- `build_graph.graph_output_path` becomes `answer.graph_path`.
- `build_graph.chunks_output_path` becomes `answer.chunks_path`.
- `build_graph.cache_dir` remains available for vector sidecar/cache health.

### Workflow Envelope

Use a new `workflow/v1` envelope instead of overloading `service-job/v1`.

Recommended shape:

```json
{
  "schema_version": "workflow/v1",
  "id": "wf_...",
  "type": "build_and_answer",
  "status": "running",
  "spec": {},
  "steps": [
    {
      "name": "build_graph",
      "job_id": "job_...",
      "status": "succeeded",
      "input_artifacts": [],
      "output_artifacts": []
    },
    {
      "name": "answer",
      "job_id": "job_...",
      "status": "running",
      "input_artifacts": [],
      "output_artifacts": []
    }
  ],
  "artifacts": [],
  "created_at": "...",
  "started_at": "...",
  "finished_at": "...",
  "error": ""
}
```

### API Surface

Add:

- `POST /v1/workflows`
- `GET /v1/workflows/{workflow_id}`
- `GET /v1/workflows/{workflow_id}/events`
- `POST /v1/workflows/{workflow_id}/cancel`

Do not remove the existing job API. Workflows should be built on top of jobs.

### Failure Semantics

For the first version:

- If `build_graph` fails, stop the workflow; do not run `answer`.
- If `answer` fails, workflow fails but keeps graph/chunks artifacts from the
  successful build step.
- If workflow is canceled while a child job is running, cancel that child job
  and mark workflow canceled.
- On service restart:
  - completed workflows load as-is;
  - running workflows are marked failed/interrupted unless we add a durable
    resume executor.

This is conservative and easy to test.

### Phase 14 Tasks

Suggested split:

1. **Phase 14A: Define workflow contract and gap map**
   - This document is the first cut.
   - Add `docs/contracts/workflows.md`.
   - Update README.

2. **Phase 14B: Implement file-backed workflow manager skeleton**
   - Add `internal/workflows`.
   - Persist `workflow-record/v1`.
   - Expose workflow HTTP endpoints.
   - First workflow may be a skeleton with explicit step records.

3. **Phase 14C: Implement `build_and_answer` workflow**
   - Submit `build_graph` child job.
   - On success, submit `answer` child job using graph/chunks handoff.
   - Record child job refs and artifact transfer.

4. **Phase 14D: Workflow validation gates**
   - @Gawain should verify:
     - workflow envelope
     - child job refs
     - artifact handoff
     - build failure stops answer
     - answer failure preserves build artifacts
     - cancel propagation
     - restart readback / interrupted running workflow
     - existing single-job gates do not regress

## What We Should Not Do Next

Do not prioritize these before workflow:

- Migrating document parsing internals to Go.
- Migrating LLM decomposition to Go.
- Migrating LLM answer generation to Go.
- Replacing Python FAISS/embedding model stack.
- Building frontend features before workflow/artifact state is stable.

Those may be useful later, but workflow orchestration is the stronger service
foundation now.

## Practical Definition Of Done For Phase 14

Phase 14 is done when:

- The repo documents the gap map and workflow contract.
- Go exposes workflow endpoints.
- A `build_and_answer` workflow can create child jobs.
- The workflow record shows step statuses and artifact handoff.
- Restart readback works for completed workflows.
- Interrupted running workflows are marked failed/interrupted.
- Tests prove failure/cancel behavior.
- Existing job tests and demo gates still pass.

## Gap-To-Test Lane Matrix

Use this matrix to turn gaps into gates.

| Capability area | API contract | Artifact contract | Lifecycle contract | Worker validation | Smoke |
| --- | --- | --- | --- | --- | --- |
| Upload / dataset creation | future `POST /v1/datasets` or upload endpoint returns stable dataset record | corpus artifact exists with dataset/name/path/status | upload job succeeds/fails with clear error; duplicate names resolved | parser stderr and unsupported file errors preserved | list dataset after upload |
| Schema management | future schema CRUD endpoint returns stable schema record | schema artifact status changes to written/missing/failed | schema update survives restart | bad JSON schema rejected | dataset artifact endpoint shows schema |
| Graph construction | existing `build_graph` job contract | corpus/schema inputs; graph/chunks/cache outputs | success, missing output, bad graph JSON, worker failure, restart readback | stderr and invalid output schema captured | existing `make test` |
| Retrieval | existing `POST /v1/retrieve` and `retrieve` job | graph/chunks inputs; inline `retrieve_result` output | failed retrieve keeps stable error/events | sidecar no-fallback and mode strategy checked | demo `loader/chunk/triple/full` gates |
| Answer generation | existing `answer` job contract | graph/chunks inputs; answer output artifact | success, missing output, bad schema, worker failure, restart readback | answer worker stdout/stderr and schema validation | answer job fake-worker tests |
| Workflow orchestration | future workflow endpoints return `workflow/v1` | build outputs are handed to answer inputs; final answer artifact visible | child job refs, failure propagation, cancel propagation, interrupted restart | existing child job worker gates still pass | `make test` and demo gates |
| Progress / UI | future SSE/WebSocket over workflow events | no new artifact requirement | clients can recover from event stream | n/a | curl workflow events |
| Evaluation | future `evaluate` job contract | answer/gold inputs; evaluation output artifact | bad judge output fails job | LLM judge stderr/schema captured | fake evaluator worker test |
