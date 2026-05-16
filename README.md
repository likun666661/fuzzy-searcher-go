# fuzzy-searcher-go

Go retriever scaffold for the Youtu-GraphRAG migration.

This repository is Phase 1 of replacing the large Python retriever core with a smaller Go core. The current scope is intentionally narrow:

- load Youtu-GraphRAG graph JSON and chunk files
- expose a stable `RetrieveRequest` / `RetrieveResult` contract
- provide a deterministic Go retriever core
- provide a CLI for black-box comparison against Python golden fixtures
- provide harness/docs for migration testing

The current Go implementation does not yet implement Python embedding/FAISS retrieval or triple reranking. Those are Phase 2 sidecar integration work.

## Layout

```text
cmd/youtu-retriever/          CLI entrypoint
internal/dataset/             Graph loaders
internal/chunks/              output/chunks/*.txt loader
internal/graphtext/           Python-parity node text helpers
internal/retrieval/           Retrieve contract and deterministic core
scripts/                      Oracle export and golden comparison scripts
docs/                         Migration test plan
testdata/                     Phase 1 oracle fixtures
```

## Test

```bash
go test ./...
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

Generate a Phase 3 regression report:

```bash
python3 scripts/retrieval_regression_report.py \
  --golden ../youtu-graphrag/output/retrieval_golden/demo.json \
  --actual /tmp/go_retrieval_actual.json \
  --record-id qa_1
```

Phase 2B expected status: `loader` and `chunk` pass, while `triple` and `full`
remain red until triple/vector reranking is wired into the Go core.
