# Graph Update / Upsert Gap Analysis

This note records the current engineering boundary for graph mutation,
rebuild, and future upsert support.

## Finding

The original `youtu-graphrag` repository is optimized for research-style full
construction, not online mutation:

- `main.py` clears FAISS cache, graph output, chunk output, and logs before
  construction.
- `KTBuilder.build_knowledge_graph()` processes the corpus, runs level-4
  community construction, then writes the graph and chunks at the end.
- Retrieval caches are rebuilt or dropped through best-effort consistency
  checks. There is no dataset manifest, chunk version graph, transactional
  artifact commit, or upsert/delete API.

The service is already better for long-running jobs because it adds job
records, artifacts, WAL, multi-runner extraction, rate limiting, checkpointed
benchmarking, and replay-only community compaction. However, before this phase
the graph extraction WAL could still incorrectly reuse old chunk extraction
when schema, corpus, chunking, or mode changed.

## Fix Applied

`scripts/build_graph_worker.py` now writes and validates a
`graph-build-input-manifest/v1`:

- dataset;
- construction mode;
- corpus sha256;
- schema sha256;
- chunking settings;
- total chunk count;
- ordered chunk hash digest;
- final manifest sha256.

When `--resume` is used, the worker scans manifest-bearing WAL rows and fails
fast with `graph_build_wal_stale` if the current manifest differs. This prevents
schema/corpus/chunking changes from silently reusing stale LLM extraction.

Legacy WAL files without manifests remain readable, but new production WALs
should always contain this manifest.

## Current Rebuild Semantics

The service currently supports safe full rebuild:

- managed dataset import owns corpus/schema/metadata;
- `POST /v1/datasets/{dataset}/rebuild` resolves managed corpus/schema;
- rebuild clears graph/chunks/cache outputs when overwrite is enabled;
- `build_graph` can resume expensive chunk extraction through WAL;
- community compaction can be rerun as a replay-only stage without re-extracting
  chunks.

This is still not true online upsert. It is a safe rebuild/resume model.

## Why Upsert Is Still Hard

True upsert needs more than appending graph JSON:

- chunk identity must be stable across corpus edits;
- one source document may expand into multiple chunks;
- one chunk may produce many nodes, edges, attributes, and schema additions;
- community detection and summaries depend on the global graph;
- FAISS/text/chunk caches need versioned invalidation and atomic refresh;
- deletion must remove graph facts, chunks, embeddings, cache entries, and
  community memberships without leaving dangling evidence.

The current worker key is effectively `doc_index:chunk_index:text_hash`. That
is correct with the manifest guard, but poor for partial reuse if a document is
inserted near the front of the corpus because downstream ordinals shift.

## Recommended Next Design

Move from "dataset rebuild" to "dataset revision + graph delta":

1. Write a `dataset-manifest/v1` for every imported corpus:
   document id, source path, text sha256, chunking config, chunk ids, schema
   hash, and dataset revision.
2. Write a `graph-delta-wal/v1`:
   per chunk `added/updated/deleted`, extraction payload, affected node/edge
   ids, and reverse indexes.
3. Change chunk identity to stable document ids:
   `dataset_revision + document_id + chunk_index + text_hash`, or
   content-addressed chunk ids with duplicate occurrence disambiguation.
4. Add a compaction planner:
   unchanged chunks replay from prior WAL, changed chunks re-extract, deleted
   chunks tombstone facts, then materialize a new graph revision.
5. Make cache publication atomic:
   build cache into a revisioned directory and switch a pointer/manifest after
   graph/chunks/cache all validate.
6. Expose service operations:
   `POST /v1/datasets/{dataset}/documents:upsert`,
   `DELETE /v1/datasets/{dataset}/documents/{document_id}`,
   and `POST /v1/datasets/{dataset}/graph:compact`.

Until this exists, production should treat graph changes as managed rebuilds
with WAL resume and manifest stale protection, not as online in-place updates.
