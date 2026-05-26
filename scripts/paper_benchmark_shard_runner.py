#!/usr/bin/env python3
"""Run paper benchmark workers as independent shard processes."""

from __future__ import annotations

import argparse
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

from paper_benchmark_worker import (
    CHECKPOINT_SCHEMA_VERSION,
    PROGRESS_SCHEMA_VERSION,
    build_preflight_result,
    build_result,
    load_checkpoint,
    load_qa,
    now_iso,
    validate_input_paths,
    write_progress,
    write_result,
)


STOP_REQUESTED = False


def handle_stop(_signum: int, _frame: Any) -> None:
    global STOP_REQUESTED
    STOP_REQUESTED = True


signal.signal(signal.SIGINT, handle_stop)
signal.signal(signal.SIGTERM, handle_stop)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run a paper benchmark through multiple shard workers.")
    parser.add_argument("--original-root", required=True)
    parser.add_argument("--dataset", default="anony_chs")
    parser.add_argument("--qa", required=True)
    parser.add_argument("--graph", required=True)
    parser.add_argument("--chunks", required=True)
    parser.add_argument("--schema", required=True)
    parser.add_argument("--cache-dir", default="retriever/faiss_cache_new")
    parser.add_argument("--config", default="config/base_config.yaml")
    parser.add_argument("--output", required=True)
    parser.add_argument("--checkpoint", default="")
    parser.add_argument("--progress", default="")
    parser.add_argument("--mode", choices=["agent", "noagent"], default="agent")
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--offset", type=int, default=0)
    parser.add_argument("--top-k", type=int, default=20)
    parser.add_argument("--recall-paths", type=int, default=2)
    parser.add_argument("--max-agent-steps", type=int, default=5)
    parser.add_argument("--llm-timeout-seconds", type=float, default=180.0)
    parser.add_argument("--llm-max-attempts", type=int, default=3)
    parser.add_argument("--llm-retry-base-seconds", type=float, default=2.0)
    parser.add_argument("--llm-retry-max-seconds", type=float, default=30.0)
    parser.add_argument("--prompt-mode", choices=["reject", "open"], default="reject")
    parser.add_argument("--community-compaction", choices=["skipped", "completed"], default="skipped")
    parser.add_argument("--compaction-wal", default="")
    parser.add_argument("--include-private-traces", action="store_true")
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--retry-failed", action="store_true")
    parser.add_argument("--preflight-only", action="store_true")
    parser.add_argument("--runtime-preflight-only", action="store_true")
    parser.add_argument("--shard-count", type=int, default=1)
    parser.add_argument("--progress-poll-seconds", type=float, default=10.0)
    return parser.parse_args()


def shard_ranges(total: int, shard_count: int) -> list[tuple[int, int]]:
    shard_count = max(1, shard_count)
    ranges: list[tuple[int, int]] = []
    base = total // shard_count
    extra = total % shard_count
    start = 0
    for shard_index in range(shard_count):
        count = base + (1 if shard_index < extra else 0)
        if count > 0:
            ranges.append((start, count))
        start += count
    return ranges


def shard_path(path: str, shard_index: int, shard_count: int) -> str:
    target = Path(path)
    return str(target.with_name(f"{target.stem}.shard-{shard_index:02d}-of-{shard_count:02d}{target.suffix}"))


def read_checkpoint_records(path: str) -> dict[str, dict[str, Any]]:
    if not path or not Path(path).exists():
        return {}
    return load_checkpoint(path)


def write_checkpoint_records(path: str, records: list[dict[str, Any]]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_suffix(target.suffix + ".tmp")
    with tmp.open("w", encoding="utf-8") as f:
        for record in records:
            f.write(json.dumps(record, ensure_ascii=False) + "\n")
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, target)


def item_sort_key(record: dict[str, Any]) -> tuple[int, str]:
    item = record.get("item") if isinstance(record.get("item"), dict) else {}
    try:
        ordinal = int(item.get("ordinal") or 0)
    except (TypeError, ValueError):
        ordinal = 0
    return (ordinal, str(record.get("id") or ""))


def seed_shard_checkpoint(base_checkpoint: str, shard_checkpoint: str, shard_ids: set[str]) -> None:
    records: dict[str, dict[str, Any]] = {}
    for source in (base_checkpoint, shard_checkpoint):
        for item_id, record in read_checkpoint_records(source).items():
            if item_id in shard_ids:
                records[item_id] = record
    write_checkpoint_records(shard_checkpoint, sorted(records.values(), key=item_sort_key))


def child_command(args: argparse.Namespace, shard_index: int, shard_count: int, start: int, count: int) -> list[str]:
    worker = Path(__file__).with_name("paper_benchmark_worker.py")
    output = shard_path(args.output, shard_index, shard_count)
    checkpoint = shard_path(args.checkpoint, shard_index, shard_count)
    progress = shard_path(args.progress, shard_index, shard_count)
    cmd = [
        sys.executable,
        str(worker),
        "--original-root",
        args.original_root,
        "--dataset",
        args.dataset,
        "--qa",
        args.qa,
        "--graph",
        args.graph,
        "--chunks",
        args.chunks,
        "--schema",
        args.schema,
        "--cache-dir",
        args.cache_dir,
        "--config",
        args.config,
        "--output",
        output,
        "--checkpoint",
        checkpoint,
        "--progress",
        progress,
        "--mode",
        args.mode,
        "--offset",
        str(args.offset + start),
        "--limit",
        str(count),
        "--top-k",
        str(args.top_k),
        "--recall-paths",
        str(args.recall_paths),
        "--max-agent-steps",
        str(args.max_agent_steps),
        "--llm-timeout-seconds",
        str(args.llm_timeout_seconds),
        "--llm-max-attempts",
        str(args.llm_max_attempts),
        "--llm-retry-base-seconds",
        str(args.llm_retry_base_seconds),
        "--llm-retry-max-seconds",
        str(args.llm_retry_max_seconds),
        "--prompt-mode",
        args.prompt_mode,
        "--community-compaction",
        args.community_compaction,
        "--compaction-wal",
        args.compaction_wal,
        "--resume",
    ]
    if args.include_private_traces:
        cmd.append("--include-private-traces")
    if args.retry_failed:
        cmd.append("--retry-failed")
    if args.runtime_preflight_only:
        cmd.append("--runtime-preflight-only")
    return cmd


def read_json(path: str) -> dict[str, Any]:
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    return data if isinstance(data, dict) else {}


def aggregate_progress(args: argparse.Namespace, total: int, shard_count: int, started_at: str) -> None:
    completed = answered = failed = correct = llm_retry_count = 0
    shards: list[dict[str, Any]] = shard_progress_summaries(args, shard_count)
    for shard in shards:
        completed += int(shard.get("completed") or 0)
        answered += int(shard.get("answered") or 0)
        failed += int(shard.get("failed") or 0)
        correct += int(shard.get("correct") or 0)
        llm_retry_count += int(shard.get("llm_retry_count") or 0)
    write_progress(args.progress, {
        "schema_version": PROGRESS_SCHEMA_VERSION,
        "dataset": args.dataset,
        "mode": args.mode,
        "prompt_mode": args.prompt_mode,
        "total": total,
        "completed": completed,
        "answered": answered,
        "failed": failed,
        "correct": correct,
        "accuracy": correct / total if total else 0.0,
        "llm_retry_count": llm_retry_count,
        "shard_count": shard_count,
        "shards": shards,
        "started_at": started_at,
        "updated_at": now_iso(),
    })


def shard_progress_summaries(args: argparse.Namespace, shard_count: int) -> list[dict[str, Any]]:
    shards: list[dict[str, Any]] = []
    for index in range(shard_count):
        progress_path = shard_path(args.progress, index, shard_count)
        if not Path(progress_path).exists():
            shards.append({"shard_index": index, "completed": 0})
            continue
        progress = read_json(progress_path)
        shards.append({
            "shard_index": index,
            "completed": int(progress.get("completed") or 0),
            "total": int(progress.get("total") or 0),
            "answered": int(progress.get("answered") or 0),
            "correct": int(progress.get("correct") or 0),
            "failed": int(progress.get("failed") or 0),
            "llm_retry_count": int(progress.get("llm_retry_count") or 0),
            "updated_at": progress.get("updated_at", ""),
        })
    return shards


def terminate_children(children: list[subprocess.Popen[Any]]) -> None:
    for child in children:
        if child.poll() is None:
            child.terminate()
    deadline = time.time() + 10
    for child in children:
        while child.poll() is None and time.time() < deadline:
            time.sleep(0.2)
    for child in children:
        if child.poll() is None:
            child.kill()


def merge_results(args: argparse.Namespace, qa_items: list[dict[str, Any]], shard_count: int, started_at: str, started_time: float) -> dict[str, Any]:
    records: dict[str, dict[str, Any]] = {}
    for index in range(shard_count):
        checkpoint_path = shard_path(args.checkpoint, index, shard_count)
        records.update(read_checkpoint_records(checkpoint_path))

    ordered_records = []
    results = []
    qa_ids = {str(item["_id"]) for item in qa_items}
    for qa in qa_items:
        record = records.get(str(qa["_id"]))
        if not record:
            continue
        if record.get("schema_version") == CHECKPOINT_SCHEMA_VERSION:
            ordered_records.append(record)
        item = record.get("item")
        if isinstance(item, dict):
            results.append(item)
    write_checkpoint_records(args.checkpoint, ordered_records)

    result = build_result(args, results, len(qa_items), args.checkpoint, args.progress, started_at, started_time)
    result["parameters"]["shard_count"] = shard_count
    result["parameters"]["shard_mode"] = "contiguous_range"
    result["method_profile"]["runtime_profile"] = "industrial_wal_checkpointed_sharded"
    result["sharding"] = {
        "schema_version": "paper-benchmark-sharding/v1",
        "shard_count": shard_count,
        "mode": "contiguous_range",
        "completed_ids": len({str(item.get("id")) for item in results} & qa_ids),
        "shard_outputs": [shard_path(args.output, index, shard_count) for index in range(shard_count)],
        "shard_checkpoints": [shard_path(args.checkpoint, index, shard_count) for index in range(shard_count)],
        "shard_progress": [shard_path(args.progress, index, shard_count) for index in range(shard_count)],
    }
    write_result(args.output, result)
    write_progress(args.progress, {
        "schema_version": PROGRESS_SCHEMA_VERSION,
        "dataset": args.dataset,
        "mode": args.mode,
        "prompt_mode": args.prompt_mode,
        "total": len(qa_items),
        "completed": result["completed_count"],
        "answered": result["answered_count"],
        "failed": result["failed_count"],
        "correct": result["correct_count"],
        "accuracy": result["accuracy"],
        "llm_retry_count": result["parameters"].get("llm_retry_count", 0),
        "shard_count": shard_count,
        "shards": shard_progress_summaries(args, shard_count),
        "updated_at": now_iso(),
    })
    return result


def main() -> int:
    args = parse_args()
    validate_input_paths(args)
    args.shard_count = max(1, int(args.shard_count or 1))
    args.checkpoint = args.checkpoint or str(Path(args.output).with_suffix(".checkpoint.jsonl"))
    args.progress = args.progress or str(Path(args.output).with_suffix(".progress.json"))

    qa_items = load_qa(args.qa, args.offset, args.limit)
    if args.preflight_only or args.shard_count <= 1:
        if args.shard_count <= 1:
            print("paper benchmark shard runner requires --shard-count > 1", file=sys.stderr)
            return 2
        result = build_preflight_result(args, len(qa_items), args.checkpoint, args.progress, {
            "shard_count": args.shard_count,
            "shard_mode": "contiguous_range",
            "shards": [
                {"shard_index": index, "offset": args.offset + start, "limit": count}
                for index, (start, count) in enumerate(shard_ranges(len(qa_items), args.shard_count))
            ],
        })
        write_result(args.output, result)
        print(json.dumps(result, ensure_ascii=False), flush=True)
        return 0

    ranges = shard_ranges(len(qa_items), args.shard_count)
    started_time = time.time()
    started_at = now_iso()
    children: list[subprocess.Popen[Any]] = []
    for index, (start, count) in enumerate(ranges):
        shard_items = qa_items[start : start + count]
        seed_shard_checkpoint(args.checkpoint, shard_path(args.checkpoint, index, args.shard_count), {str(item["_id"]) for item in shard_items})
        cmd = child_command(args, index, args.shard_count, start, count)
        print(json.dumps({
            "schema_version": "paper-benchmark-shard-started/v1",
            "shard_index": index,
            "shard_count": args.shard_count,
            "offset": args.offset + start,
            "limit": count,
            "checkpoint": shard_path(args.checkpoint, index, args.shard_count),
        }, ensure_ascii=False), flush=True)
        children.append(subprocess.Popen(cmd))

    try:
        while any(child.poll() is None for child in children):
            if STOP_REQUESTED:
                terminate_children(children)
                break
            aggregate_progress(args, len(qa_items), args.shard_count, started_at)
            time.sleep(max(float(args.progress_poll_seconds or 10.0), 1.0))
    finally:
        if STOP_REQUESTED:
            terminate_children(children)

    aggregate_progress(args, len(qa_items), args.shard_count, started_at)
    hard_failures = [child.returncode for child in children if child.returncode not in (0, 2)]
    result = merge_results(args, qa_items, args.shard_count, started_at, started_time)
    print(json.dumps({
        "schema_version": PROGRESS_SCHEMA_VERSION,
        "dataset": args.dataset,
        "mode": args.mode,
        "prompt_mode": args.prompt_mode,
        "completed": result["completed_count"],
        "total": result["question_count"],
        "correct": result["correct_count"],
        "failed": result["failed_count"],
        "accuracy": result["accuracy"],
        "shard_count": args.shard_count,
        "hard_failures": hard_failures,
    }, ensure_ascii=False), flush=True)
    if hard_failures or STOP_REQUESTED:
        return 1
    if result["completed_count"] < result["question_count"]:
        return 2
    return 0 if result["failed_count"] == 0 else 2


if __name__ == "__main__":
    raise SystemExit(main())
