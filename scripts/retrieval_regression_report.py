#!/usr/bin/env python3
"""Generate a layered retrieval regression report.

This script is intentionally a thin reporting wrapper around
compare_retrieval_golden.py. It keeps the pass/fail semantics identical while
adding Phase 3 diagnostics such as triple top-N overlap.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

import compare_retrieval_golden as golden_compare


MODES = ("loader", "chunk", "triple", "full")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Report layered golden regression status for Go retriever output."
    )
    parser.add_argument("--golden", required=True, type=Path, help="Python golden JSON")
    parser.add_argument("--actual", required=True, type=Path, help="Go actual JSON")
    parser.add_argument(
        "--record-id",
        default=None,
        help="record id to compare when using retriever-golden/v1 fixtures",
    )
    parser.add_argument(
        "--top-n",
        type=int,
        action="append",
        default=[5, 10, 20],
        help="triple top-N overlap cutoff; can be repeated",
    )
    parser.add_argument(
        "--float-tolerance",
        type=float,
        default=1e-5,
        help="absolute tolerance for numeric score comparison",
    )
    parser.add_argument(
        "--ignore-path",
        action="append",
        default=[],
        help="dot path to ignore; can be repeated",
    )
    parser.add_argument(
        "--strict-extra-fields",
        action="store_true",
        help="fail when actual contains fields not present in golden",
    )
    parser.add_argument(
        "--format",
        choices=("markdown", "json"),
        default="markdown",
        help="report output format",
    )
    parser.add_argument(
        "--fail-on",
        action="append",
        choices=MODES,
        default=[],
        help="exit 1 if the selected mode fails; can be repeated",
    )
    return parser.parse_args()


def load_targets(args: argparse.Namespace) -> tuple[dict[str, Any], dict[str, Any]]:
    golden = golden_compare.load_json(args.golden)
    actual = golden_compare.load_json(args.actual)
    golden_target, actual_target, errors = golden_compare.comparison_targets(
        golden, actual, args.record_id
    )
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        raise SystemExit(2)
    return golden_compare.normalize(golden_target), golden_compare.normalize(actual_target)


def mode_diffs(
    golden_target: dict[str, Any],
    actual_target: dict[str, Any],
    args: argparse.Namespace,
) -> dict[str, list[str]]:
    ignored = set(golden_compare.DEFAULT_IGNORES)
    ignored.update(args.ignore_path)

    result: dict[str, list[str]] = {}
    for mode in MODES:
        result[mode] = golden_compare.compare(
            golden_compare.result_for_mode(golden_target, mode),
            golden_compare.result_for_mode(actual_target, mode),
            float_tolerance=args.float_tolerance,
            ignored=ignored,
            strict_extra_fields=args.strict_extra_fields,
        )
    return result


def result_payload(target: dict[str, Any]) -> dict[str, Any]:
    result = target.get("result")
    return result if isinstance(result, dict) else {}


def triple_key(value: Any) -> str:
    normalized = golden_compare.normalize_triple(value)
    return json.dumps(normalized, ensure_ascii=False, sort_keys=True)


def triple_label(value: Any) -> str:
    normalized = golden_compare.normalize_triple(value)
    if isinstance(normalized, dict):
        return (
            f"{normalized.get('subject', '')} | "
            f"{normalized.get('relation', '')} | "
            f"{normalized.get('object', '')}"
        )
    return str(normalized)


def triple_summary(golden_result: dict[str, Any], actual_result: dict[str, Any], top_ns: list[int]) -> dict[str, Any]:
    golden_triples = golden_result.get("triples", [])
    actual_triples = actual_result.get("triples", [])
    if not isinstance(golden_triples, list):
        golden_triples = []
    if not isinstance(actual_triples, list):
        actual_triples = []

    golden_keys = [triple_key(item) for item in golden_triples]
    actual_keys = [triple_key(item) for item in actual_triples]
    actual_key_set = set(actual_keys)

    overlap = []
    for top_n in sorted(set(top_ns)):
        golden_top = set(golden_keys[:top_n])
        actual_top = set(actual_keys[:top_n])
        count = len(golden_top & actual_top)
        denominator = min(top_n, len(golden_top)) or 1
        overlap.append(
            {
                "top_n": top_n,
                "overlap": count,
                "golden_count": len(golden_top),
                "actual_count": len(actual_top),
                "golden_coverage": count / denominator,
            }
        )

    missing = [
        triple_label(item)
        for item, key in zip(golden_triples, golden_keys)
        if key not in actual_key_set
    ]
    extra = [
        triple_label(item)
        for item, key in zip(actual_triples, actual_keys)
        if key not in set(golden_keys)
    ]

    return {
        "golden_count": len(golden_triples),
        "actual_count": len(actual_triples),
        "exact_overlap": len(set(golden_keys) & set(actual_keys)),
        "top_n_overlap": overlap,
        "missing_examples": missing[:5],
        "extra_examples": extra[:5],
    }


def chunk_summary(golden_result: dict[str, Any], actual_result: dict[str, Any]) -> dict[str, Any]:
    golden_ids = [str(item) for item in golden_result.get("chunk_ids", [])]
    actual_ids = [str(item) for item in actual_result.get("chunk_ids", [])]
    golden_chunk_results = golden_result.get("chunk_retrieval_results", [])
    actual_chunk_results = actual_result.get("chunk_retrieval_results", [])
    if not isinstance(golden_chunk_results, list):
        golden_chunk_results = []
    if not isinstance(actual_chunk_results, list):
        actual_chunk_results = []

    return {
        "golden_chunk_ids": golden_ids,
        "actual_chunk_ids": actual_ids,
        "chunk_id_set_match": set(golden_ids) == set(actual_ids),
        "chunk_id_order_match": golden_ids == actual_ids,
        "golden_chunk_retrieval_results": len(golden_chunk_results),
        "actual_chunk_retrieval_results": len(actual_chunk_results),
    }


def build_report(
    golden_target: dict[str, Any],
    actual_target: dict[str, Any],
    diffs_by_mode: dict[str, list[str]],
    args: argparse.Namespace,
) -> dict[str, Any]:
    golden_result = result_payload(golden_target)
    actual_result = result_payload(actual_target)
    return {
        "golden": str(args.golden),
        "actual": str(args.actual),
        "record_id": args.record_id,
        "modes": {
            mode: {
                "passed": not diffs,
                "diff_count": len(diffs),
                "first_differences": diffs[:5],
            }
            for mode, diffs in diffs_by_mode.items()
        },
        "chunks": chunk_summary(golden_result, actual_result),
        "triples": triple_summary(golden_result, actual_result, args.top_n),
    }


def print_markdown(report: dict[str, Any]) -> None:
    print("# Retrieval Regression Report")
    print()
    print(f"- Golden: `{report['golden']}`")
    print(f"- Actual: `{report['actual']}`")
    if report.get("record_id"):
        print(f"- Record: `{report['record_id']}`")
    print()

    print("## Gates")
    print()
    print("| Mode | Status | Diff count |")
    print("| --- | --- | ---: |")
    for mode in MODES:
        data = report["modes"][mode]
        status = "pass" if data["passed"] else "fail"
        print(f"| `{mode}` | {status} | {data['diff_count']} |")
    print()

    print("## Chunks")
    chunks = report["chunks"]
    print()
    print(f"- Golden chunk ids: `{','.join(chunks['golden_chunk_ids'])}`")
    print(f"- Actual chunk ids: `{','.join(chunks['actual_chunk_ids'])}`")
    print(f"- Chunk id set match: `{chunks['chunk_id_set_match']}`")
    print(f"- Chunk id order match: `{chunks['chunk_id_order_match']}`")
    print(
        "- Chunk retrieval results: "
        f"`{chunks['actual_chunk_retrieval_results']}` actual / "
        f"`{chunks['golden_chunk_retrieval_results']}` golden"
    )
    print()

    print("## Triples")
    triples = report["triples"]
    print()
    print(f"- Triple count: `{triples['actual_count']}` actual / `{triples['golden_count']}` golden")
    print(f"- Exact overlap: `{triples['exact_overlap']}`")
    print()
    print("| Top N | Overlap | Golden count | Actual count | Golden coverage |")
    print("| ---: | ---: | ---: | ---: | ---: |")
    for item in triples["top_n_overlap"]:
        print(
            f"| {item['top_n']} | {item['overlap']} | {item['golden_count']} | "
            f"{item['actual_count']} | {item['golden_coverage']:.2f} |"
        )
    print()

    if triples["missing_examples"]:
        print("Missing golden triple examples:")
        for item in triples["missing_examples"]:
            print(f"- `{item}`")
        print()
    if triples["extra_examples"]:
        print("Extra actual triple examples:")
        for item in triples["extra_examples"]:
            print(f"- `{item}`")
        print()

    failing_modes = [
        f"`{mode}`"
        for mode in MODES
        if not report["modes"][mode]["passed"]
    ]
    if failing_modes:
        print(f"Failing modes: {', '.join(failing_modes)}")
        print()


def main() -> int:
    args = parse_args()
    golden_target, actual_target = load_targets(args)
    diffs_by_mode = mode_diffs(golden_target, actual_target, args)
    report = build_report(golden_target, actual_target, diffs_by_mode, args)

    if args.format == "json":
        print(json.dumps(report, indent=2, ensure_ascii=False))
    else:
        print_markdown(report)

    failed_required = [mode for mode in args.fail_on if diffs_by_mode[mode]]
    return 1 if failed_required else 0


if __name__ == "__main__":
    raise SystemExit(main())
