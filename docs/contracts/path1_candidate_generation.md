# Path1 Raw Candidate Generation Contract

This is the Phase 9 contract for moving path1 raw candidate generation from
Python authority into Go. The goal is to let Go produce the raw triples that are
sent to `POST /v1/retrieval/rerank-triples`, while Python remains responsible
only for embedding-based rerank scoring.

Reference implementation:

```text
youtu-graphrag/models/retriever/enhanced_kt_retriever.py
  KTRetriever._node_relation_retrieval(question_embed, question)
```

The parity reference remains `POST /v1/retrieval/path1-triples`:

- `top_nodes`
- `top_relations`
- `raw_one_hop_triples_count`
- `raw_one_hop_triples`

## Inputs

Go-native path1 candidate generation needs:

- graph JSON loaded into the same directed multigraph semantics as Python;
- node text using Python `_get_node_text(node)`;
- node properties/chunk id using Python node metadata conventions;
- question embedding from the configured sentence-transformers model;
- node FAISS hits from the sidecar `node` index;
- relation FAISS hits from the sidecar `relation` index;
- keyword extraction equivalent to Python spaCy extraction, or a temporary
  sidecar/provenance source for exact keyword parity;
- node embedding similarity scores equivalent to Python
  `_batch_calculate_entity_similarities`.

For the demo fixture, `top_k=20` and node-search `search_k=min(top_k*3, 50)`.

## Candidate Node Selection

Python path1 first finds candidate nodes, scores them, and selects `top_nodes`.

1. Encode the raw question as `question_embed`.
2. Transform the embedding with `faiss_retriever.transform_vector`.
3. Search the node FAISS index with `search_k=min(top_k*3, 50)`.
4. Extract keywords from `question.lower()` using spaCy:
   - include non-stop tokens longer than 2 characters;
   - include tokens with entity type;
   - include `NOUN`, `PROPN`, `ADJ`, and `VERB` tokens;
   - include named entities longer than 2 characters;
   - deduplicate with `list(set(keywords))`.
5. Search keyword nodes with the node text index:
   - node text is lowercased Python `_get_node_text(node)`;
   - index tokens are `node_text_lower.split()`;
   - only token length greater than 2 is indexed;
   - each keyword returns at most 50 nodes;
   - stop once the accumulated keyword node set exceeds 200 nodes.
6. Score FAISS candidate nodes and keyword-only nodes with cosine similarity to
   `question_embed`.
7. Drop keyword-only nodes with score `<= 0.05`.
8. Merge FAISS-scored nodes and keyword-scored nodes, sort by score descending,
   then select up to `top_k` nodes with score `> 0.05`.

Important parity notes:

- Python uses sets in keyword extraction and keyword node collection, so raw
  order is not stable across hash seeds.
- Final `top_nodes` ordering is score-descending, but equal-score ties inherit
  Python's input order.
- For exact gates, compare the produced `top_nodes` with
  `path1-triples/v1.top_nodes` before comparing raw triples.

## Relation Selection

Python searches the relation FAISS index with the transformed question
embedding and `top_k`.

The resulting `top_relations` are consumed by relation-matched triple
generation. For exact parity, compare Go `top_relations` with
`path1-triples/v1.top_relations`.

## Raw Triple Sources

Python builds raw path1 triples from three sources, then deduplicates with a
set:

```python
all_triples = list({
    triple for triple in one_hop_triples + path_triples + relation_triples
})
```

This means the final raw candidate order is intentionally not stable. Phase 9
Go should first target set parity, then use deterministic ordering only as an
implementation detail before sending candidates to the rerank primitive.

### Neighbor Expansion

Reference: `_optimized_neighbor_expansion(top_nodes, question_embed)`.

For each top node:

1. Read outgoing graph neighbors with `graph.neighbors(node)`.
2. Add every neighbor to a shared `all_neighbors` set.
3. Add edge queries `(node, n)` and `(n, node)` for every node currently in the
   accumulated `all_neighbors` set.
4. For every edge query, call `graph.get_edge_data(u, v)`.
5. If edge data exists, take the first edge record and read `relation`.
6. Append `(u, relation, v)` when relation is non-empty.

Parity notes:

- `all_neighbors` is shared across top nodes, not reset per node.
- `edge_queries` is a set, so ordering is not stable.
- Python takes the first edge value from NetworkX edge data for multiedges.

### Path-Based Search

Reference: `_path_based_search(top_nodes, keywords, max_depth=2)`.

For each start node:

1. DFS through outgoing `graph.neighbors(node)`.
2. Use a single shared `visited` set across all start nodes.
3. Stop traversal when `depth > max_depth` or node is already visited.
4. Lowercase Python `_get_node_text(node)` and check whether any keyword is a
   substring.
5. When a keyword matches, append triples for each adjacent pair in the current
   DFS path where `graph.get_edge_data(u, v)` has a relation.
6. Continue traversal while `depth < max_depth`.

Parity notes:

- The shared `visited` set means earlier start nodes suppress later paths.
- Keyword order can vary because Python deduplicates keywords with `set`.

### Relation-Matched Triples

Reference: `_get_relation_matched_triples(top_nodes, all_relations)`.

For every graph edge `(u, v, data)`:

- include `(u, data["relation"], v)` when the relation is in `top_relations`;
- require either `u` or `v` to be in `top_nodes`.

Graph edge iteration order follows the loaded graph's insertion order.

## Output Contract For Go

The raw candidate item sent to `/v1/retrieval/rerank-triples` should use the
trace-compatible shape where possible:

```json
{
  "key": "head_id\trelation\ttail_id",
  "head_id": "head_id",
  "relation": "relation",
  "tail_id": "tail_id",
  "formatted_without_score": "(head text [props], relation, tail text [props])",
  "chunk_ids": ["chunk_id"]
}
```

The rerank primitive only requires `head_id`, `relation`, and `tail_id`; the
other fields are for debugging and gate reports.

## Phase 9 Acceptance

Before enabling a Go-native path1 candidate mode as the preferred path:

1. Keep existing `runtime-trace`, `primitive-merge`, and `rerank-merge` gates
   green.
2. Compare Go-native raw candidate set against
   `path1-triples/v1.raw_one_hop_triples`.
3. Report:
   - `top_nodes` exact/order match;
   - `top_relations` exact/order match;
   - raw candidate count;
   - raw candidate set overlap;
   - rerank input count;
   - reranked count;
   - final `loader/chunk/triple/full` gate status.
4. Treat raw candidate order differences separately from set differences.
5. Only require final `triple/full` zero-diff once raw candidate set parity and
   rerank output parity are both stable.

