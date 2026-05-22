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
import subprocess
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import json_repair


SCHEMA_VERSION = "build-graph-result/v1"
LEGACY_WAL_SCHEMA_VERSION = "graph-build-wal-item/v1"
WAL_SCHEMA_VERSION = "graph-build-wal/v1"
COMPACT_SCHEMA_VERSION = "graph-build-wal-compact/v1"


class ChunkExtractionError(RuntimeError):
    def __init__(self, message: str, attempts: int) -> None:
        super().__init__(message)
        self.attempts = attempts


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
    parser.add_argument("--runner-count", type=int, default=1)
    parser.add_argument("--runner-index", type=int, default=-1)
    parser.add_argument("--extract-only", action="store_true")
    parser.add_argument("--llm-rate-limit-rpm", type=int, default=0)
    parser.add_argument("--llm-rate-limit-file", default="")
    parser.add_argument("--llm-max-attempts", type=int, default=1)
    parser.add_argument("--llm-retry-base-seconds", type=float, default=2.0)
    parser.add_argument("--llm-retry-max-seconds", type=float, default=30.0)
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
        line = json.dumps(record, ensure_ascii=False) + "\n"
        fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
        try:
            os.write(fd, line.encode("utf-8"))
            os.fsync(fd)
        finally:
            os.close(fd)


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


def acquire_rate_limit(path: str, rpm: int) -> None:
    if not path or rpm <= 0:
        return
    interval = 60.0 / float(rpm)
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    lock_path = target.with_suffix(target.suffix + ".lock")
    while True:
        try:
            fd = os.open(lock_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o644)
        except FileExistsError:
            time.sleep(0.05)
            continue
        try:
            now = time.time()
            try:
                with open(target, "r", encoding="utf-8") as f:
                    last = float((f.read() or "0").strip() or "0")
            except FileNotFoundError:
                last = 0.0
            wait_for = (last + interval) - now
            if wait_for > 0:
                time.sleep(wait_for)
                now = time.time()
            tmp = target.with_suffix(target.suffix + ".tmp")
            with open(tmp, "w", encoding="utf-8") as f:
                f.write(f"{now:.6f}\n")
            os.replace(tmp, target)
            return
        finally:
            os.close(fd)
            try:
                os.unlink(lock_path)
            except FileNotFoundError:
                pass
        time.sleep(0.05)


def replay_success(builder: Any, record: dict[str, Any]) -> None:
    builder.all_chunks[record["chunk_id"]] = record.get("chunk_text", "")
    apply_extraction(builder, record["chunk_id"], record.get("extraction") or {})


def extract_chunk(builder: Any, item: dict[str, Any], args: argparse.Namespace) -> dict[str, Any]:
    prompt = builder._get_construction_prompt(item["chunk_text"])
    max_attempts = max(int(args.llm_max_attempts or 1), 1)
    base_delay = max(float(args.llm_retry_base_seconds or 0), 0.0)
    max_delay = max(float(args.llm_retry_max_seconds or base_delay), base_delay)
    last_error = "unknown_error"
    for attempt in range(1, max_attempts + 1):
        try:
            acquire_rate_limit(args.llm_rate_limit_file, args.llm_rate_limit_rpm)
            llm_response = builder.extract_with_llm(prompt)
            parsed_response = builder._validate_and_parse_llm_response(prompt, llm_response)
            if not parsed_response:
                raise RuntimeError("invalid_llm_response")
            return {
                **item,
                "extraction": parsed_response,
                "attempts": attempt,
                "token_count": builder.token_cal(prompt + json.dumps(parsed_response, ensure_ascii=False)),
            }
        except Exception as exc:
            last_error = str(exc) or exc.__class__.__name__
            if attempt >= max_attempts:
                break
            delay = min(max_delay, base_delay * (2 ** (attempt - 1)))
            if delay > 0:
                time.sleep(delay)
    raise ChunkExtractionError(f"{last_error} (attempts={max_attempts})", max_attempts)


def child_runner_argv(args: argparse.Namespace, runner_index: int, wal_path: str) -> list[str]:
    argv = [
        sys.executable,
        str(Path(__file__).resolve()),
        "--dataset", args.dataset,
        "--corpus", args.corpus,
        "--graph-output", args.graph_output,
        "--chunks-output", args.chunks_output,
        "--wal", wal_path,
        "--resume",
        "--runner-count", str(max(args.runner_count, 1)),
        "--runner-index", str(runner_index),
        "--extract-only",
        "--max-workers", str(max(args.max_workers, 1)),
        "--config", args.config,
        "--mode", args.mode,
    ]
    if args.llm_rate_limit_rpm > 0:
        argv.extend(["--llm-rate-limit-rpm", str(args.llm_rate_limit_rpm)])
    if args.llm_rate_limit_file:
        argv.extend(["--llm-rate-limit-file", args.llm_rate_limit_file])
    argv.extend([
        "--llm-max-attempts", str(max(args.llm_max_attempts, 1)),
        "--llm-retry-base-seconds", str(max(args.llm_retry_base_seconds, 0.0)),
        "--llm-retry-max-seconds", str(max(args.llm_retry_max_seconds, 0.0)),
    ])
    if args.schema:
        argv.extend(["--schema", args.schema])
    if args.cache_dir:
        argv.extend(["--cache-dir", args.cache_dir])
    if args.skip_communities:
        argv.append("--skip-communities")
    return argv


def run_child_runners(args: argparse.Namespace, wal_path: str) -> list[tuple[int, int, str, str]]:
    runner_count = max(args.runner_count, 1)
    failures: list[tuple[int, int, str, str]] = []
    with ThreadPoolExecutor(max_workers=runner_count) as executor:
        futures = {
            executor.submit(
                subprocess.run,
                child_runner_argv(args, runner_index, wal_path),
                cwd=os.getcwd(),
                text=True,
                capture_output=True,
                check=False,
            ): runner_index
            for runner_index in range(runner_count)
        }
        for future in as_completed(futures):
            runner_index = futures[future]
            proc = future.result()
            if proc.returncode != 0:
                failures.append((runner_index, proc.returncode, proc.stdout, proc.stderr))
            for line in (proc.stdout or "").splitlines():
                line = line.strip()
                if line.startswith("{"):
                    print(line, flush=True)
    return failures


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
    for ordinal, item in enumerate(items):
        item["chunk_ordinal"] = ordinal

    will_spawn_child_runners = args.runner_count > 1 and args.runner_index < 0 and not args.extract_only
    should_replay_existing = not args.extract_only and not will_spawn_child_runners

    succeeded = 0
    skipped = 0
    pending: list[dict[str, Any]] = []
    for item in items:
        existing = wal_latest.get(item["chunk_key"])
        existing_event = str((existing or {}).get("event") or "")
        existing_status = str((existing or {}).get("status") or "")
        if existing and (existing_event == "chunk_succeeded" or existing_status == "succeeded"):
            if should_replay_existing:
                replay_success(builder, existing)
            skipped += 1
            succeeded += 1
        else:
            pending.append(item)

    if args.runner_index >= 0:
        runner_count = max(args.runner_count, 1)
        pending = [item for item in pending if int(item.get("chunk_ordinal", 0)) % runner_count == args.runner_index]

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
            "runner_count": max(args.runner_count, 1),
            "llm_rate_limit_rpm": args.llm_rate_limit_rpm,
            "llm_rate_limit_file": args.llm_rate_limit_file,
            "runner_index": args.runner_index,
            "extract_only": args.extract_only,
            "llm_max_attempts": max(args.llm_max_attempts, 1),
            "llm_retry_base_seconds": max(args.llm_retry_base_seconds, 0.0),
            "llm_retry_max_seconds": max(args.llm_retry_max_seconds, 0.0),
        },
    }, wal_lock, wal_sequence)

    if will_spawn_child_runners:
        failures = run_child_runners(args, wal_path)
        if failures:
            append_wal(wal_path, {
                "schema_version": WAL_SCHEMA_VERSION,
                "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
                "dataset": args.dataset,
                "event": "run_failed",
                "status": "failed",
                "payload": {
                    "runner_count": max(args.runner_count, 1),
                    "failed_runners": [
                        {"runner_index": idx, "return_code": code, "stderr": stderr[-2000:]}
                        for idx, code, _stdout, stderr in failures
                    ],
                },
                "finished_at": now_iso(),
            }, wal_lock, wal_sequence)
            for idx, code, _stdout, stderr in failures:
                print(f"graph_build_runner_failed: runner={idx} exit={code}: {stderr[-1000:]}", file=sys.stderr)
            return 3

        wal_latest, max_sequence = load_wal(wal_path)
        wal_sequence["value"] = max_sequence
        builder = KTBuilder(args.dataset, args.schema, mode=args.mode, config=config)
        succeeded = 0
        skipped = 0
        pending = []
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
        if pending:
            append_wal(wal_path, {
                "schema_version": WAL_SCHEMA_VERSION,
                "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
                "dataset": args.dataset,
                "event": "run_failed",
                "status": "failed",
                "payload": {
                    "reason": "missing_runner_chunk_results",
                    "pending_chunks": len(pending),
                    "runner_count": max(args.runner_count, 1),
                    "llm_rate_limit_rpm": args.llm_rate_limit_rpm,
                    "llm_max_attempts": max(args.llm_max_attempts, 1),
                },
                "finished_at": now_iso(),
            }, wal_lock, wal_sequence)
            print(f"graph_build_missing_runner_chunks: {len(pending)}", file=sys.stderr)
            return 3

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
            result = extract_chunk(builder, item, args)
            return result, None
        except Exception as exc:
            attempts = getattr(exc, "attempts", max(args.llm_max_attempts, 1))
            return None, {
                "schema_version": WAL_SCHEMA_VERSION,
                **item,
                "run_id": os.getenv("YOUTU_RAG_JOB_ID", ""),
                "dataset": args.dataset,
                "event": "chunk_failed",
                "status": "failed",
                "error": str(exc),
                "payload": {"error": str(exc), "attempts": attempts},
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
                "payload": {
                    "token_count": result.get("token_count", 0),
                    "attempts": result.get("attempts", 1),
                },
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

    if args.extract_only:
        result = {
            "schema_version": "build-graph-runner-result/v1",
            "dataset": args.dataset,
            "wal_path": wal_path,
            "total_chunks": len(items),
            "runner_count": max(args.runner_count, 1),
            "runner_index": args.runner_index,
            "succeeded_chunks": succeeded,
            "skipped_chunks": skipped,
            "finished_at": now_iso(),
        }
        print(json.dumps(result, ensure_ascii=False), flush=True)
        return 0

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
            "runner_count": max(args.runner_count, 1),
            "llm_rate_limit_rpm": args.llm_rate_limit_rpm,
            "llm_max_attempts": max(args.llm_max_attempts, 1),
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
            "runner_count": max(args.runner_count, 1),
            "llm_rate_limit_rpm": args.llm_rate_limit_rpm,
            "llm_max_attempts": max(args.llm_max_attempts, 1),
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
        "runner_count": max(args.runner_count, 1),
        "llm_rate_limit_rpm": args.llm_rate_limit_rpm,
        "llm_max_attempts": max(args.llm_max_attempts, 1),
        "skip_communities": args.skip_communities,
        "started_at": started_at,
        "finished_at": now_iso(),
    }
    print(json.dumps(result, ensure_ascii=False), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
