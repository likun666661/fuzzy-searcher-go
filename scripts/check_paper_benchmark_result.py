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


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--result", required=True)
    parser.add_argument("--checkpoint", default="")
    parser.add_argument("--progress", default="")
    parser.add_argument("--dataset", default="")
    parser.add_argument("--mode", choices=["agent", "noagent"], default="")
    parser.add_argument("--limit", type=int, default=0)
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

    deviations = result.get("deviations") or {}
    require(deviations.get("skip_communities") is True, "skip_communities deviation must be true", errors)
    require(deviations.get("community_compaction") == "skipped", "community compaction deviation missing", errors)

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
            require((item.get("mapping_score") or {}).get("schema_version") == "anonymized-mapping-score/v1", f"item {idx} bad mapping_score", errors)
            if not args.allow_private_traces:
                require("detail" not in item, f"item {idx} contains private detail trace", errors)
                require("thoughts" not in item, f"item {idx} contains private thoughts", errors)
            require("detail_summary" in item, f"item {idx} missing detail_summary", errors)

    checkpoint_path = Path(args.checkpoint or artifacts.get("checkpoint_path") or "")
    if str(checkpoint_path):
        require(checkpoint_path.exists(), f"checkpoint missing: {checkpoint_path}", errors)
        if checkpoint_path.exists():
            check_no_secret(checkpoint_path.read_text(encoding="utf-8"), errors)
            rows = 0
            with checkpoint_path.open("r", encoding="utf-8") as f:
                for line_no, line in enumerate(f, 1):
                    line = line.strip()
                    if not line:
                        continue
                    rows += 1
                    try:
                        row = json.loads(line)
                    except json.JSONDecodeError as exc:
                        errors.append(f"checkpoint line {line_no} invalid JSON: {exc}")
                        continue
                    require(row.get("schema_version") == "paper-benchmark-checkpoint-item/v1", f"checkpoint line {line_no} bad schema", errors)
            require(rows >= int(result.get("completed_count") or 0), "checkpoint rows fewer than completed_count", errors)

    progress_path = Path(args.progress or artifacts.get("progress_path") or "")
    if str(progress_path):
        require(progress_path.exists(), f"progress missing: {progress_path}", errors)
        if progress_path.exists():
            progress = load_json(progress_path)
            check_no_secret(progress_path.read_text(encoding="utf-8"), errors)
            require(progress.get("schema_version") == "paper-benchmark-progress/v1", "bad progress schema", errors)
            require(progress.get("completed") == result.get("completed_count"), "progress completed mismatch", errors)

    if errors:
        for error in errors:
            print(f"paper benchmark result check failed: {error}", file=sys.stderr)
        return 1
    print(f"paper benchmark result ok: {result.get('dataset')} {result.get('mode')} n={result.get('question_count')} accuracy={result.get('accuracy')}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
