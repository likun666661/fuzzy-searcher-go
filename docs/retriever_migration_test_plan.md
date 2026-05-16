# Go Retriever Migration Test Plan

This plan covers Phase 1C: black-box contract and golden parity checks between
the current Python retriever and the Go retriever CLI. It deliberately avoids
owning the Go retriever implementation or the Python sidecar API.

## Scope

- Validate that Go CLI output conforms to the agreed retrieval JSON contract.
- Compare Go CLI output with Python golden fixtures generated from
  `KTRetriever.process_retrieval_results(question, top_k, involved_types)`.
- Produce deterministic diff output that can be used in CI or during local
  migration work.
- Keep non-deterministic fields such as timings and raw debug metadata out of
  strict parity checks.

## Upstream Inputs

Phase 1C consumes outputs from the other Phase 1 tasks:

- Phase 1A provides the Go CLI and `RetrieveRequest`/`RetrieveResult` structs.
- Phase 1B provides Python golden fixtures and sidecar boundary docs.

The first test fixture should target the existing demo artifacts:

- Python graph: `../youtu-graphrag/output/graphs/demo_new.json`
- Python chunks: `../youtu-graphrag/output/chunks/demo.txt`
- Python cache: `../youtu-graphrag/retriever/faiss_cache_new/demo`
- Go snapshot/oracle data: `testdata/`

## Contract

The comparison harness expects both golden and actual files to normalize to:

```json
{
  "schema_version": "retriever-golden/v1",
  "dataset": "demo",
  "top_k": 20,
  "python_entrypoint": "models.retriever.enhanced_kt_retriever.KTRetriever.process_retrieval_results",
  "inputs": {},
  "environment": {},
  "records": [
    {
      "id": "demo_messi_barcelona_001",
      "question": "When was ...?",
      "involved_types": {
        "nodes": ["person", "organization"],
        "relations": [],
        "attributes": []
      },
      "retrieval": {
        "triples": ["[Lionel Messi, signed by, FC Barcelona]"],
        "chunk_ids": ["0", "4"],
        "chunk_contents_by_id": {
          "0": "...",
          "4": "..."
        },
        "chunk_retrieval_results": []
      }
    }
  ]
}
```

The authoritative fixture schema lives in
`../youtu-graphrag/docs/retriever_golden.schema.json`. The harness compares
`records[*].retrieval` against the Go CLI JSON. It normalizes
`chunk_contents_by_id` into Go's ordered `chunk_contents` list using
`chunk_ids`.

## Parity Rules

- `request.question`, `request.top_k`, and `request.involved_types` must match.
- `result.chunk_ids` are compared as ordered lists by default.
- `result.chunk_contents` are compared as ordered lists by default.
- `result.triples` are compared as ordered lists after normalizing each triple
  to `subject`, `relation`, `object`, optional `score`, and optional `source`.
- Float scores are compared with a small absolute tolerance.
- Unknown extra fields are allowed unless `--strict-extra-fields` is passed.
- Ignored paths can be configured with repeated `--ignore-path` flags.

## Test Matrix

Initial golden cases should cover:

- Simple entity lookup with one obvious answer.
- Multi-hop question using `involved_types`.
- Chunk-heavy question where answer evidence comes from chunk retrieval.
- Triple-heavy question where graph edges dominate the result.
- Query with no high-confidence answer to lock down empty/low-recall behavior.
- Non-ASCII query text, once Phase 1B confirms Python fixture stability.

## Local Workflow

Generate Python golden fixtures from Phase 1B, then run Go CLI output into a
temporary JSON file:

```bash
go run ./cmd/youtu-retriever retrieve \
  --graph ../youtu-graphrag/output/graphs/demo_new.json \
  --chunks ../youtu-graphrag/output/chunks/demo.txt \
  --question "..." \
  --top-k 10 \
  > /tmp/go_retrieval_actual.json
```

Compare:

```bash
python3 scripts/compare_retrieval_golden.py \
  --golden ../youtu-graphrag/output/retrieval_golden/demo.json \
  --actual /tmp/go_retrieval_actual.json \
  --record-id demo_messi_barcelona_001 \
  --mode full
```

The script exits `0` on parity and non-zero with a path-oriented diff on
mismatch.

## Phase 2 Modes

The harness supports layered compare modes so sidecar integration can be
accepted incrementally:

- `loader`: compares chunk id coverage and chunk contents by id, ignoring order.
  This guards graph/chunk loading and basic chunk id extraction.
- `chunk`: compares ordered chunk ids, ordered chunk contents, and
  `chunk_retrieval_results`. This is the first Phase 2B sidecar gate.
- `triple`: compares only triples after normalizing Python and Go triple string
  formats.
- `full`: compares the full normalized retrieval result.

Recommended Phase 2 command sequence:

```bash
python3 scripts/compare_retrieval_golden.py \
  --golden ../youtu-graphrag/output/retrieval_golden/demo.json \
  --actual /tmp/go_retrieval_actual.json \
  --record-id qa_1 \
  --mode loader

python3 scripts/compare_retrieval_golden.py \
  --golden ../youtu-graphrag/output/retrieval_golden/demo.json \
  --actual /tmp/go_retrieval_actual.json \
  --record-id qa_1 \
  --mode chunk

python3 scripts/compare_retrieval_golden.py \
  --golden ../youtu-graphrag/output/retrieval_golden/demo.json \
  --actual /tmp/go_retrieval_actual.json \
  --record-id qa_1 \
  --mode triple
```

For Phase 2B, `loader` should remain green and `chunk` should become green
after the Go retriever consumes sidecar chunk FAISS results. `triple` and
`full` are expected to remain red until vector/triple rerank work lands.

## CI Gate

The first CI gate should be permissive:

- Run `go test ./...`.
- Run the comparison harness for checked-in retrieval fixtures.
- Fail on missing required fields, request mismatch, or deterministic result
  mismatch.
- Ignore debug timings and allow extra debug metadata.

Once the Go retriever behavior stabilizes, tighten ordering and extra-field
rules case by case.
