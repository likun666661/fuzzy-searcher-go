# Sidecar Primitive Contracts

The Go retriever treats Python as a narrow vector/rerank sidecar. The current
main path is `rerank-merge`: Go requests path1 raw candidates, sends them to a
rerank-only primitive, requests path2 rescored triples, then performs the final
merge/sort/top-k locally.

## Retrieval Modes

| CLI mode | Required sidecar endpoint(s) | Debug strategy |
| --- | --- | --- |
| `runtime-trace` | `POST /v1/retrieval/triple-trace` | `python_triple_trace` |
| `path2-detrace` | `POST /v1/retrieval/triple-trace`, `POST /v1/retrieval/path2-triples` | `path2_detrace_merge` |
| `primitive-merge` | `POST /v1/retrieval/path1-triples`, `POST /v1/retrieval/path2-triples` | `path1_path2_primitive_merge` |
| `rerank-merge` | `POST /v1/retrieval/path1-triples`, `POST /v1/retrieval/rerank-triples`, `POST /v1/retrieval/path2-triples` | `path1_rerank_path2_primitive_merge` |

`rerank-merge` is the current preferred parity mode. `runtime-trace` and
`primitive-merge` remain regression gates.

Phase 9 narrows path1 further: Go should produce raw path1 candidates and call
only `POST /v1/retrieval/rerank-triples` for scoring. The Python parity contract
for Go-native raw candidate generation is documented in
`docs/contracts/path1_candidate_generation.md`.

## `path1-triples/v1`

Request:

```json
{
  "dataset": "demo",
  "question": "question text",
  "top_k": 20,
  "include_raw": true,
  "involved_types": {
    "nodes": [],
    "relations": [],
    "attributes": []
  }
}
```

Response fields Go consumes:

- `schema_version`: must equal `path1-triples/v1`.
- `raw_one_hop_triples_count`: expected raw candidate count.
- `raw_one_hop_triples`: raw caller-owned candidates for `rerank-merge`.
- `reranked_triples`: Python-authoritative path1 output for
  `primitive-merge`.

For Phase 9, `raw_one_hop_triples` is also the authority fixture for checking
Go-native path1 candidate generation.

## `rerank-triples/v1`

Request:

```json
{
  "dataset": "demo",
  "question": "question text",
  "top_k": 20,
  "triples": [
    {
      "key": "head_id\trelation\ttail_id",
      "head_id": "head_id",
      "relation": "relation",
      "tail_id": "tail_id",
      "formatted_without_score": "(head, relation, tail)",
      "chunk_ids": ["chunk_id"]
    }
  ]
}
```

Response fields Go consumes:

- `schema_version`: must equal `rerank-triples/v1`.
- `stats.input_triples`: expected to match the raw candidate count.
- `reranked_triples`: scored path1 triples that Go merges with path2.

## `path2-triples/v1`

Request:

```json
{
  "dataset": "demo",
  "question": "question text",
  "top_k": 20,
  "threshold": 0.1,
  "include_candidates": false,
  "include_index_hits": false
}
```

Response fields Go consumes:

- `schema_version`: must equal `path2-triples/v1`.
- `rescored_triples`: Python-authoritative path2 triples that Go merges with
  path1 output.

## Trace-Compatible Triple Item

All triple arrays use the same item shape:

```json
{
  "rank": 1,
  "source": "rerank_scored",
  "key": "head_id\trelation\ttail_id",
  "head_id": "head_id",
  "relation": "relation",
  "tail_id": "tail_id",
  "score": 0.65,
  "formatted_triple": "(head, relation, tail) [score: 0.650]",
  "formatted_without_score": "(head, relation, tail)",
  "chunk_ids": ["chunk_id"]
}
```

Go requires either `formatted_triple` or `formatted_without_score` plus
`score`; empty formatted triples are ignored. Relations `represented_by` and
`kw_filter_by` are filtered from public output.
