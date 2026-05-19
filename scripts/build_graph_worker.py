#!/usr/bin/env python3
"""Resumable graph construction worker for the Go Youtu-RAG service.

The original youtu-graphrag constructor writes the graph only after all chunks
finish. This worker adds a chunk-level append-only WAL so expensive extraction
can resume after interruption without re-calling the LLM for completed chunks.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import threading
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import json_repair


SCHEMA_VERSION = "build-graph-result/v1"
LEGACY_WAL_SCHEMA_VERSION = "graph-build-wal-item/v1"
WAL_SCHEMA_VERSION = "graph-build-wal/v1"
COMPACT_SCHEMA_VERSION = "graph-build-wal-compact/v1"


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build a Youtu-RAG graph with WAL resume.")
    parser.add_argument("--dataset", required=True)
    parser.add_argument("--corpus", required=True)
    parser.add_argument("--graph-output", required=True)
    parser.add_argument("--chunks-output", required=True)
    parser.add_argument("--schema", default="")
    parser.add_argument("--cache-dir", default="")
    parser.add_argument("--wal", default="")
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--max-workers", type=int, default=1)
    parser.add_argument("--skip-communities", action="store_true")
    parser.add_argument("--config", default="config/base_config.yaml")
    parser.add_argument("--mode", default="noagent")
    return parser.parse_args()


def sha256_text(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def load_json(path: str) -> Any:
    with open(path, "r", encoding="utf-8") as f:
        return json_repair.load(f)


def append_wal(path: str, record: dict[str, Any], lock: threading.Lock, seq: dict[str, int] | None = None) -> None:
    if not path:
        return
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with lock:
        if record.get("schema_version") == WAL_SCHEMA_VERSION and seq is not None:
            seq["value"] += 1
            record.setdefault("sequence", seq["value"])
            record.setdefault("time", now_iso())
        with open(target, "a", encoding="utf-8") as f:
            json.dump(record, f, ensure_ascii=False)
            f.write("\n")
            f.flush()
            os.fsync(f.fileno())


def load_wal(path: str) -> tuple[dict[str, dict[str, Any]], int]:
    if not path or not os.path.exists(path):
        return {}, 0
    latest: dict[str, dict[str, Any]] = {}
    max_sequence = 0
    with open(path, "r", encoding="utf-8") as f:
        for line_no, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                raise SystemExit(f"graph_build_wal_invalid: line {line_no}: {exc}") from exc
            schema = record.get("schema_version")
            if schema == COMPACT_SCHEMA_VERSION:
                continue
            if schema not in {WAL_SCHEMA_VERSION, LEGACY_WAL_SCHEMA_VERSION}:
                raise SystemExit(f"graph_build_wal_invalid: line {line_no}: unexpected schema {schema!r}")
            if isinstance(record.get("sequence"), int):
                max_sequence = max(max_sequence, record["sequence"])
            event = str(record.get("event") or "")
            if schema == WAL_SCHEMA_VERSION and event.startswith("run_"):
                continue
            key = str(record.get("chunk_key") or "")
            if not key:
                raise SystemExit(f"graph_build_wal_invalid: line {line_no}: missing chunk_key")
            latest[key] = record
    return latest, max_sequence


def configure_import_path() -> None:
    cwd = os.getcwd()
    if cwd not in sys.path:
        sys.path.insert(0, cwd)


def load_config(args: argparse.Namespace):
    configure_import_path()
    from config import ConfigManager

    config = ConfigManager(args.config)
    dataset_cfg = {
        "corpus_path": args.corpus,
        "qa_path": "",
        "schema_path": args.schema or config.get_dataset_config(args.dataset).schema_path,
        "graph_output": args.graph_output,
    }
    overrides: dict[str, Any] = {
        "datasets": {args.dataset: dataset_cfg},
        "triggers": {"constructor_trigger": True, "retrieve_trigger": False, "mode": args.mode},
        "construction": {"mode": args.mode, "max_workers": max(args.max_workers, 1)},
    }
    if args.schema:
        overrides["datasets"][args.dataset]["schema_path"] = args.schema
    config.override_config(overrides)
    return config


def document_chunks(builder: Any, doc: Any) -> list[str]:
    if builder.dataset_name in builder.datasets_no_chunk:
        text = f"{doc.get('title', '')} {doc.get('text', '')}".strip() if isinstance(doc, dict) else str(doc)
        return [text] if text else []
    raw_text = str(doc)
    if isinstance(doc, dict):
        raw_text = f"{doc.get('title', '')} {doc.get('text', '')}".strip()
    chunk_size = getattr(builder.config.construction, "chunk_size", 1000)
    overlap = getattr(builder.config.construction, "overlap", 200)
    min_tail_tokens = getattr(builder.config.construction, "min_tail_tokens", 100)
    return builder._split_text_with_overlap(raw_text, chunk_size, overlap, min_tail_tokens)


def chunk_record(dataset: str, doc_index: int, chunk_index: int, chunk: str) -> dict[str, Any]:
    digest = sha256_text(chunk)
    return {
        "chunk_key": f"{doc_index}:{chunk_index}:{digest}",
        "chunk_id": f"{dataset}_{doc_index:06d}_{chunk_index:03d}_{digest[:8]}",
        "chunk_sha256": digest,
        "doc_index": doc_index,
        "chunk_index": chunk_index,
        "chunk_text": chunk,
    }


def apply_extraction(builder: Any, chunk_id: str, parsed_response: dict[str, Any]) -> None:
    extracted_attr = parsed_response.get("attributes", {}) if isinstance(parsed_response, dict) else {}
    extracted_triples = parsed_response.get("triples", []) if isinstance(parsed_response, dict) else []
    entity_types = parsed_response.get("entity_types", {}) if isinstance(parsed_response, dict) else {}
    if builder.mode == "agent":
        new_schema_types = parsed_response.get("new_schema_types", {}) if isinstance(parsed_response, dict) else {}
        if new_schema_types:
            builder._update_schema_with_new_types(new_schema_types)
        with builder.lock:
            builder._process_attributes_agent(extracted_attr, chunk_id, entity_types)
            builder._process_triples_agent(extracted_triples, chunk_id, entity_types)
        return

    attr_nodes, attr_edges = builder._process_attributes(extracted_attr, chunk_id, entity_types)
    triple_nodes, triple_edges = builder._process_triples(extracted_triples, chunk_id, entity_types)
    with builder.lock:
        for node_id, node_data in attr_nodes + triple_nodes:
            builder.graph.add_node(node_id, **node_data)
        for u, v, relation in attr_edges + triple_edges:
            builder.graph.add_edge(u, v, relation=relation)


def replay_success(builder: Any, record: dict[str, Any]) -> None:
    builder.all_chunks[record["chunk_id"]] = record.get("chunk_text", "")
    apply_extraction(builder, record["chunk_id"], record.get("extraction") or {})


def extract_chunk(builder: Any, item: dict[str, Any]) -> dict[str, Any]:
    prompt = builder._get_construction_prompt(item["chunk_text"])
    llm_response = builder.extract_with_llm(prompt)
    parsed_response = builder._validate_and_parse_llm_response(prompt, llm_response)
    if not parsed_response:
        raise RuntimeError("invalid_llm_response")
    return {
        **item,
        "extraction": parsed_response,
        "token_count": builder.token_cal(prompt + json.dumps(parsed_response, ensure_ascii=False)),
    }


def write_chunks(path: str, chunks: dict[str, str]) -> None:
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        for chunk_id, chunk_text in chunks.items():
            escaped = chunk_text.replace("\n", "\\n").replace("\t", "\\t")
            f.write(f"id: {chunk_id}\tChunk: {escaped}\n")


def main() -> int:
    args = parse_args()
    config = load_config(args)

    from models.constructor.kt_gen import KTBuilder

    builder = KTBuilder(args.dataset, args.schema, mode=args.mode, config=config)
    docs = load_json(args.corpus)
    if not isinstance(docs, list):
        raise SystemExit(f"graph_build_invalid_corpus: expected list at {args.corpus}")

    wal_path = args.wal or str(Path(args.graph_output).with_suffix(".wal.jsonl"))
    if not args.resume and os.path.exists(wal_path):
        os.remove(wal_path)
    if args.resume:
        wal_latest, max_sequence = load_wal(wal_path)
    else:
        wal_latest, max_sequence = {}, 0
    wal_sequence = {"value": max_sequence}
    wal_lock = threading.Lock()
    started_at = now_iso()

    items: list[dict[str, Any]] = []
    for doc_index, doc in enumerate(docs):
        for chunk_index, chunk in enumerate(document_chunks(builder, doc)):
            if not chunk.strip():
                continue
            items.append(chunk_record(args.dataset, doc_index, chunk_index, chunk))

    succeeded = 0
    skipped = 0
    pending: list[dict[str, Any]] = []
    for item in items:
        existing = wal_latest.get(item["chunk_key"])
        existing_event = str((existing or {}).get("event") or "")
        existing_status = str((existing or {}).get("status") or "")
        if existing and (existing_event == "chunk_succeeded" or existing_status == "succeeded"):
            replay_success(builder, existing)
            skipped += 1
            succeeded += 1
        else:
            pending.append(item)

    append_wal(wal_path, {
        "schema_version": WAL_SCHEMA_VERSION,
        "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
        "dataset": args.dataset,
        "event": "run_resumed" if args.resume and max_sequence else "run_started",
        "status": "resumed" if args.resume and max_sequence else "started",
        "payload": {
            "total_chunks": len(items),
            "succeeded_chunks": succeeded,
            "skipped_chunks": skipped,
            "pending_chunks": len(pending),
            "max_workers": max(args.max_workers, 1),
            "mode": args.mode,
            "skip_communities": args.skip_communities,
        },
    }, wal_lock, wal_sequence)

    def run_one(item: dict[str, Any]) -> tuple[dict[str, Any] | None, dict[str, Any] | None]:
        append_wal(wal_path, {
            "schema_version": WAL_SCHEMA_VERSION,
            **item,
            "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
            "dataset": args.dataset,
            "event": "chunk_started",
            "status": "started",
            "started_at": now_iso(),
            "payload": {},
        }, wal_lock, wal_sequence)
        try:
            result = extract_chunk(builder, item)
            return result, None
        except Exception as exc:
            return None, {
                "schema_version": WAL_SCHEMA_VERSION,
                **item,
                "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
                "dataset": args.dataset,
                "event": "chunk_failed",
                "status": "failed",
                "error": str(exc),
                "payload": {"error": str(exc)},
                "finished_at": now_iso(),
            }

    failed = 0
    with ThreadPoolExecutor(max_workers=max(args.max_workers, 1)) as executor:
        futures = {executor.submit(run_one, item): item for item in pending}
        for future in as_completed(futures):
            result, failure = future.result()
            if failure:
                failed += 1
                append_wal(wal_path, failure, wal_lock, wal_sequence)
                continue
            assert result is not None
            replay_success(builder, {
                "chunk_id": result["chunk_id"],
                "chunk_text": result["chunk_text"],
                "extraction": result["extraction"],
            })
            succeeded += 1
            append_wal(wal_path, {
                "schema_version": WAL_SCHEMA_VERSION,
                "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
                "dataset": args.dataset,
                "event": "chunk_succeeded",
                "status": "succeeded",
                "finished_at": now_iso(),
                "payload": {"token_count": result.get("token_count", 0)},
                **result,
            }, wal_lock, wal_sequence)
            print(json.dumps({
                "schema_version": "graph-build-progress/v1",
                "dataset": args.dataset,
                "completed": succeeded + failed,
                "total": len(items),
                "succeeded": succeeded,
                "failed": failed,
                "skipped": skipped,
            }, ensure_ascii=False), flush=True)

    if failed:
        print(f"graph_build_failed_chunks: {failed}", file=sys.stderr)
        return 3

    append_wal(wal_path, {
        "schema_version": WAL_SCHEMA_VERSION,
        "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
        "dataset": args.dataset,
        "event": "run_compacting",
        "status": "compacting",
        "payload": {
            "wal_path": wal_path,
            "graph_output_path": args.graph_output,
            "chunks_output_path": args.chunks_output,
            "total_chunks": len(items),
            "succeeded_chunks": succeeded,
            "skipped_chunks": skipped,
            "skip_communities": args.skip_communities,
        },
    }, wal_lock, wal_sequence)
    builder.triple_deduplicate()
    if not args.skip_communities:
        builder.process_level4()
    write_chunks(args.chunks_output, builder.all_chunks)
    graph_output = builder.format_output()
    Path(args.graph_output).parent.mkdir(parents=True, exist_ok=True)
    with open(args.graph_output, "w", encoding="utf-8") as f:
        json.dump(graph_output, f, ensure_ascii=False, indent=2)
        f.write("\n")
    if args.cache_dir:
        Path(args.cache_dir).mkdir(parents=True, exist_ok=True)

    compact = {
        "schema_version": WAL_SCHEMA_VERSION,
        "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
        "dataset": args.dataset,
        "event": "run_succeeded",
        "status": "compacted",
        "payload": {
            "wal_path": wal_path,
            "graph_output_path": args.graph_output,
            "chunks_output_path": args.chunks_output,
            "total_chunks": len(items),
            "succeeded_chunks": succeeded,
            "skipped_chunks": skipped,
            "skip_communities": args.skip_communities,
        },
        "finished_at": now_iso(),
    }
    append_wal(wal_path, compact, wal_lock, wal_sequence)
    result = {
        "schema_version": SCHEMA_VERSION,
        "dataset": args.dataset,
        "graph_output_path": args.graph_output,
        "chunks_output_path": args.chunks_output,
        "cache_dir": args.cache_dir,
        "wal_path": wal_path,
        "total_chunks": len(items),
        "succeeded_chunks": succeeded,
        "skipped_chunks": skipped,
        "skip_communities": args.skip_communities,
        "started_at": started_at,
        "finished_at": now_iso(),
    }
    print(json.dumps(result, ensure_ascii=False), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
