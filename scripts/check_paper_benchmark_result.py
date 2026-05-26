#!/usr/bin/env python3
"""Validate a paper-aligned benchmark result artifact.

This is intentionally standard-library only so release/smoke checks can run in
clean clones. It validates the external artifact contract, not model accuracy.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any


def load_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def require(condition: bool, message: str, errors: list[str]) -> None:
    if not condition:
        errors.append(message)


def check_no_secret(body: str, errors: list[str]) -> None:
    for name in ("LLM_API_KEY", "DEEPSEEK_API_KEY"):
        value = os.getenv(name, "")
        if value:
            require(value not in body, f"artifact leaks {name}", errors)


def checkpoint_ids(path: Path, errors: list[str], label: str) -> list[str]:
    ids: list[str] = []
    if not path.exists():
        errors.append(f"{label} checkpoint missing: {path}")
        return ids
    with path.open("r", encoding="utf-8") as f:
        for line_no, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as exc:
                errors.append(f"{label} checkpoint line {line_no} invalid JSON: {exc}")
                continue
            require(row.get("schema_version") == "paper-benchmark-checkpoint-item/v1", f"{label} checkpoint line {line_no} bad schema", errors)
            ids.append(str(row.get("id") or ""))
    return ids


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--result", required=True)
    parser.add_argument("--checkpoint", default="")
    parser.add_argument("--progress", default="")
    parser.add_argument("--dataset", default="")
    parser.add_argument("--mode", choices=["agent", "noagent"], default="")
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--prompt-mode", choices=["reject", "open"], default="")
    parser.add_argument("--community-compaction", choices=["skipped", "completed"], default="skipped")
    parser.add_argument("--allow-private-traces", action="store_true")
    args = parser.parse_args()

    result_path = Path(args.result)
    errors: list[str] = []
    try:
        result = load_json(result_path)
    except Exception as exc:  # pragma: no cover - exercised from shell.
        print(f"paper benchmark result check failed: read result: {exc}", file=sys.stderr)
        return 1

    body = result_path.read_text(encoding="utf-8")
    check_no_secret(body, errors)

    require(result.get("schema_version") == "paper-benchmark-result/v1", "bad result schema_version", errors)
    require(result.get("benchmark_kind") == "paper_aligned", "bad benchmark_kind", errors)
    if args.dataset:
        require(result.get("dataset") == args.dataset, f"dataset != {args.dataset}", errors)
    if args.mode:
        require(result.get("mode") == args.mode, f"mode != {args.mode}", errors)
    if args.limit > 0:
        require(result.get("question_count") == args.limit, f"question_count != {args.limit}", errors)

    for key in ("question_count", "completed_count", "answered_count", "failed_count", "correct_count", "accuracy"):
        require(key in result, f"missing result field {key}", errors)
    require(isinstance(result.get("accuracy"), (int, float)), "accuracy must be numeric", errors)

    paper_config = result.get("paper_config") or {}
    require(paper_config.get("constructor_trigger") is False, "constructor_trigger must be false", errors)
    require(paper_config.get("retrieve_trigger") is True, "retrieve_trigger must be true", errors)
    require(paper_config.get("recall_paths") == 2, "recall_paths must be 2", errors)
    require(paper_config.get("top_k_filter") == 20, "top_k_filter must be 20", errors)
    require(paper_config.get("enable_query_enhancement") is True, "query enhancement must be enabled", errors)
    require(paper_config.get("enable_high_recall") is True, "high recall must be enabled", errors)
    require(paper_config.get("enable_reranking") is True, "reranking must be enabled", errors)

    parameters = result.get("parameters") or {}
    require(isinstance(parameters.get("llm_max_attempts"), int), "llm_max_attempts must be recorded", errors)
    require(isinstance(parameters.get("retry_failed"), bool), "retry_failed must be recorded", errors)
    require(isinstance(parameters.get("llm_retry_count"), int), "llm_retry_count must be recorded", errors)
    if parameters.get("shard_count") is not None:
        require(isinstance(parameters.get("shard_count"), int), "shard_count must be numeric", errors)
    if args.prompt_mode:
        require(parameters.get("prompt_mode") == args.prompt_mode, f"prompt_mode != {args.prompt_mode}", errors)

    method_profile = result.get("method_profile") or {}
    require(method_profile.get("schema_version") == "youtu-method-profile/v1", "bad method_profile schema", errors)
    if args.prompt_mode:
        require(method_profile.get("prompt_mode") == args.prompt_mode, "method_profile prompt_mode mismatch", errors)
    require(method_profile.get("runtime_profile"), "missing method_profile runtime_profile", errors)
    sharding = result.get("sharding")
    if sharding is not None:
        require(isinstance(sharding, dict), "sharding must be an object", errors)
        if isinstance(sharding, dict):
            require(sharding.get("schema_version") == "paper-benchmark-sharding/v1", "bad sharding schema", errors)
            shard_count = sharding.get("shard_count")
            require(isinstance(shard_count, int), "sharding shard_count must be numeric", errors)
            for key in ("shard_outputs", "shard_checkpoints", "shard_progress"):
                values = sharding.get(key)
                require(isinstance(values, list), f"sharding {key} must be a list", errors)
                if isinstance(values, list) and isinstance(shard_count, int):
                    require(len(values) == shard_count, f"sharding {key} length must match shard_count", errors)
            require(sharding.get("completed_ids") == result.get("completed_count"), "sharding completed_ids must match completed_count", errors)

    deviations = result.get("deviations") or {}
    expect_compacted = args.community_compaction == "completed"
    require(deviations.get("skip_communities") is (not expect_compacted), f"skip_communities deviation must be {not expect_compacted}", errors)
    require(deviations.get("community_compaction") == args.community_compaction, "community compaction deviation mismatch", errors)
    if expect_compacted:
        compaction_wal = deviations.get("compaction_wal_path")
        require(bool(compaction_wal), "compacted benchmark must reference compaction_wal_path", errors)
        require(bool(compaction_wal and Path(compaction_wal).exists()), "compaction_wal_path does not exist", errors)

    artifacts = result.get("artifacts") or {}
    for key in ("qa_path", "graph_path", "chunks_path", "schema_path", "checkpoint_path", "progress_path"):
        require(artifacts.get(key), f"missing artifact path {key}", errors)
    for key in ("qa_path", "graph_path", "chunks_path", "schema_path"):
        path = artifacts.get(key)
        require(bool(path and Path(path).exists()), f"artifact path does not exist: {key}", errors)

    mapping = result.get("anonymized_mapping") or {}
    require(mapping.get("schema_version") == "anonymized-mapping-summary/v1", "bad mapping summary schema", errors)
    for key in ("precision", "recall", "f1", "exact_recall"):
        require(isinstance(mapping.get(key), (int, float)), f"mapping {key} must be numeric", errors)

    items = result.get("items")
    require(isinstance(items, list), "items must be a list", errors)
    if isinstance(items, list):
        require(len(items) == result.get("completed_count"), "items length must match completed_count", errors)
        require(len(items) == result.get("question_count"), "items length must match question_count after completed smoke", errors)
        for idx, item in enumerate(items):
            require(item.get("schema_version") == "paper-benchmark-item/v1", f"item {idx} bad schema", errors)
            require(item.get("mode") == result.get("mode"), f"item {idx} mode mismatch", errors)
            require(item.get("judge") in {"0", "1"}, f"item {idx} judge must be 0/1", errors)
            require(isinstance(item.get("llm_call_count"), int), f"item {idx} missing llm_call_count", errors)
            require(isinstance(item.get("llm_retry_count"), int), f"item {idx} missing llm_retry_count", errors)
            require((item.get("mapping_score") or {}).get("schema_version") == "anonymized-mapping-score/v1", f"item {idx} bad mapping_score", errors)
            if not args.allow_private_traces:
                require("detail" not in item, f"item {idx} contains private detail trace", errors)
                require("thoughts" not in item, f"item {idx} contains private thoughts", errors)
            require("detail_summary" in item, f"item {idx} missing detail_summary", errors)

    checkpoint_path = Path(args.checkpoint or artifacts.get("checkpoint_path") or "")
    main_checkpoint_ids: list[str] = []
    if str(checkpoint_path):
        require(checkpoint_path.exists(), f"checkpoint missing: {checkpoint_path}", errors)
        if checkpoint_path.exists():
            check_no_secret(checkpoint_path.read_text(encoding="utf-8"), errors)
            main_checkpoint_ids = checkpoint_ids(checkpoint_path, errors, "main")
            require(len(main_checkpoint_ids) == len(set(main_checkpoint_ids)), "main checkpoint contains duplicate ids", errors)
            require(len(main_checkpoint_ids) >= int(result.get("completed_count") or 0), "checkpoint rows fewer than completed_count", errors)

    if isinstance(sharding, dict):
        shard_paths = sharding.get("shard_checkpoints")
        if isinstance(shard_paths, list):
            all_shard_ids: list[str] = []
            for index, path in enumerate(shard_paths):
                ids = checkpoint_ids(Path(str(path)), errors, f"shard {index}")
                require(len(ids) == len(set(ids)), f"shard {index} checkpoint contains duplicate ids", errors)
                all_shard_ids.extend(ids)
            require(len(all_shard_ids) == len(set(all_shard_ids)), "QA ids must appear in at most one shard checkpoint", errors)
            if main_checkpoint_ids:
                require(set(main_checkpoint_ids) == set(all_shard_ids), "main checkpoint ids must equal merged shard checkpoint ids", errors)

    progress_path = Path(args.progress or artifacts.get("progress_path") or "")
    if str(progress_path):
        require(progress_path.exists(), f"progress missing: {progress_path}", errors)
        if progress_path.exists():
            progress = load_json(progress_path)
            check_no_secret(progress_path.read_text(encoding="utf-8"), errors)
            require(progress.get("schema_version") == "paper-benchmark-progress/v1", "bad progress schema", errors)
            require(progress.get("completed") == result.get("completed_count"), "progress completed mismatch", errors)
            if isinstance(sharding, dict):
                require(progress.get("shard_count") == sharding.get("shard_count"), "progress shard_count mismatch", errors)
                shards = progress.get("shards")
                require(isinstance(shards, list), "progress shards must be a list for sharded result", errors)
                if isinstance(shards, list) and isinstance(sharding.get("shard_count"), int):
                    require(len(shards) == sharding.get("shard_count"), "progress shards length mismatch", errors)

    if errors:
        for error in errors:
            print(f"paper benchmark result check failed: {error}", file=sys.stderr)
        return 1
    print(f"paper benchmark result ok: {result.get('dataset')} {result.get('mode')} n={result.get('question_count')} accuracy={result.get('accuracy')}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
