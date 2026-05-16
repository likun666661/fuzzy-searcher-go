#!/usr/bin/env python3
"""Compare Python retrieval golden JSON with Go retriever JSON output.

The script intentionally uses only the Python standard library so it can run in
CI before the Python retriever environment is available.
"""

from __future__ import annotations

import argparse
import json
import math
import sys
from pathlib import Path
from typing import Any


DEFAULT_IGNORES = {
    "result.debug.timings_ms",
}

REQUIRED_FIXTURE_TOP_LEVEL = ("version", "case_id", "request", "result")
REQUIRED_RETRIEVER_GOLDEN_TOP_LEVEL = ("schema_version", "records")
REQUIRED_REQUEST = ("question", "top_k")
REQUIRED_RESULT = ("triples", "chunk_ids", "chunk_contents")
REQUIRED_GOLDEN_RETRIEVAL_RESULT = ("triples", "chunk_ids", "chunk_contents_by_id")
REQUIRED_TRIPLE = ("subject", "relation", "object")


def load_json(path: Path) -> Any:
    try:
        with path.open("r", encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        raise SystemExit(f"file not found: {path}")
    except json.JSONDecodeError as exc:
        raise SystemExit(f"invalid JSON in {path}: {exc}") from exc


def path_join(parent: str, child: str) -> str:
    return child if not parent else f"{parent}.{child}"


def is_ignored(path: str, ignored: set[str]) -> bool:
    return path in ignored or any(path.startswith(prefix + ".") for prefix in ignored)


def require_fixture_shape(name: str, data: Any) -> list[str]:
    errors: list[str] = []
    if not isinstance(data, dict):
        return [f"{name}: expected object at top level"]

    for key in REQUIRED_FIXTURE_TOP_LEVEL:
        if key not in data:
            errors.append(f"{name}: missing {key}")

    request = data.get("request")
    if isinstance(request, dict):
        for key in REQUIRED_REQUEST:
            if key not in request:
                errors.append(f"{name}: missing request.{key}")
    elif "request" in data:
        errors.append(f"{name}: request must be object")

    result = data.get("result")
    if isinstance(result, dict):
        for key in REQUIRED_RESULT:
            if key not in result:
                errors.append(f"{name}: missing result.{key}")
        triples = result.get("triples", [])
        if isinstance(triples, list):
            for index, triple in enumerate(triples):
                if isinstance(triple, str):
                    continue
                if not isinstance(triple, dict):
                    errors.append(f"{name}: result.triples.{index} must be object or string")
                    continue
                for key in REQUIRED_TRIPLE:
                    if key not in triple:
                        errors.append(f"{name}: missing result.triples.{index}.{key}")
        elif "triples" in result:
            errors.append(f"{name}: result.triples must be array")
    elif "result" in data:
        errors.append(f"{name}: result must be object")

    return errors


def require_result_shape(name: str, data: Any) -> list[str]:
    errors: list[str] = []
    if not isinstance(data, dict):
        return [f"{name}: expected object at result level"]

    for key in REQUIRED_RESULT:
        if key not in data:
            errors.append(f"{name}: missing {key}")
    triples = data.get("triples", [])
    if isinstance(triples, list):
        for index, triple in enumerate(triples):
            if isinstance(triple, str):
                continue
            if not isinstance(triple, dict):
                errors.append(f"{name}: triples.{index} must be object or string")
                continue
            for key in REQUIRED_TRIPLE:
                if key not in triple:
                    errors.append(f"{name}: missing triples.{index}.{key}")
    elif "triples" in data:
        errors.append(f"{name}: triples must be array")
    return errors


def require_retriever_golden_shape(name: str, data: Any, record_id: str | None) -> list[str]:
    errors: list[str] = []
    if not isinstance(data, dict):
        return [f"{name}: expected object at top level"]

    for key in REQUIRED_RETRIEVER_GOLDEN_TOP_LEVEL:
        if key not in data:
            errors.append(f"{name}: missing {key}")

    records = data.get("records")
    if not isinstance(records, list) or not records:
        errors.append(f"{name}: records must be a non-empty array")
        return errors

    try:
        select_record(data, record_id)
    except ValueError as exc:
        errors.append(f"{name}: {exc}")
        return errors

    for index, record in enumerate(records):
        if not isinstance(record, dict):
            errors.append(f"{name}: records.{index} must be object")
            continue
        if "retrieval" not in record:
            errors.append(f"{name}: records.{index} missing retrieval")
            continue
        retrieval = record["retrieval"]
        if not isinstance(retrieval, dict):
            errors.append(f"{name}: records.{index}.retrieval must be object")
            continue
        for key in REQUIRED_GOLDEN_RETRIEVAL_RESULT:
            if key not in retrieval:
                errors.append(f"{name}: records.{index}.retrieval missing {key}")
    return errors


def is_retriever_golden(data: Any) -> bool:
    return isinstance(data, dict) and data.get("schema_version") == "retriever-golden/v1"


def is_fixture(data: Any) -> bool:
    return isinstance(data, dict) and "result" in data and "request" in data


def is_bare_result(data: Any) -> bool:
    return isinstance(data, dict) and all(key in data for key in REQUIRED_RESULT)


def comparison_targets(
    golden: Any, actual: Any, record_id: str | None
) -> tuple[Any, Any, list[str]]:
    errors: list[str] = []

    if is_retriever_golden(golden):
        errors.extend(require_retriever_golden_shape("golden", golden, record_id))
        golden_target = golden_record_target(golden, record_id) if not errors else golden
    elif is_fixture(golden):
        errors.extend(require_fixture_shape("golden", golden))
        golden_target = {
            "request": golden.get("request"),
            "result": golden.get("result"),
        }
    elif is_bare_result(golden):
        errors.extend(require_result_shape("golden", golden))
        golden_target = {"result": golden}
    else:
        errors.append("golden: expected fixture wrapper or bare RetrieveResult")
        golden_target = golden

    if is_retriever_golden(actual):
        errors.extend(require_retriever_golden_shape("actual", actual, record_id))
        actual_target = golden_record_target(actual, record_id) if not errors else actual
    elif is_fixture(actual):
        errors.extend(require_fixture_shape("actual", actual))
        actual_target = {
            "request": actual.get("request"),
            "result": actual.get("result"),
        }
    elif is_bare_result(actual):
        errors.extend(require_result_shape("actual", actual))
        actual_target = {"result": actual}
    else:
        errors.append("actual: expected fixture wrapper or bare RetrieveResult")
        actual_target = actual

    # Go CLI output is intentionally bare RetrieveResult. If only one side has
    # request metadata, compare result parity and validate the wrapped request
    # shape without requiring the bare side to invent request fields.
    if isinstance(golden_target, dict) and isinstance(actual_target, dict):
        if "request" not in golden_target or "request" not in actual_target:
            golden_target = {"result": golden_target.get("result")}
            actual_target = {"result": actual_target.get("result")}

    return golden_target, actual_target, errors


def select_record(fixture: dict[str, Any], record_id: str | None) -> dict[str, Any]:
    records = fixture.get("records")
    if not isinstance(records, list) or not records:
        raise ValueError("no records available")
    if record_id:
        for record in records:
            if isinstance(record, dict) and record.get("id") == record_id:
                return record
        raise ValueError(f"record id {record_id!r} not found")
    if len(records) > 1:
        raise ValueError("multiple records present; pass --record-id")
    record = records[0]
    if not isinstance(record, dict):
        raise ValueError("selected record is not an object")
    return record


def golden_record_target(fixture: dict[str, Any], record_id: str | None) -> dict[str, Any]:
    record = select_record(fixture, record_id)
    return {
        "result": normalize_golden_retrieval(record.get("retrieval")),
    }


def normalize_golden_retrieval(value: Any) -> Any:
    if not isinstance(value, dict):
        return value

    result = dict(value)
    contents_by_id = result.pop("chunk_contents_by_id", None)
    if isinstance(contents_by_id, dict) and isinstance(result.get("chunk_ids"), list):
        result["chunk_contents"] = [
            contents_by_id.get(str(chunk_id), "") for chunk_id in result["chunk_ids"]
        ]
    return result


def normalize(data: Any) -> Any:
    if not isinstance(data, dict):
        return data

    normalized = dict(data)
    normalized["request"] = normalize_request(normalized.get("request"))
    normalized["result"] = normalize_result(normalized.get("result"))
    return normalized


def normalize_request(value: Any) -> Any:
    if not isinstance(value, dict):
        return value

    request = dict(value)
    if "involved_types" in request and isinstance(request["involved_types"], list):
        request["involved_types"] = [str(item) for item in request["involved_types"]]
    return request


def normalize_result(value: Any) -> Any:
    if not isinstance(value, dict):
        return value

    result = dict(value)
    if "chunk_retrieval_results" not in result:
        result["chunk_retrieval_results"] = []
    if isinstance(result.get("chunk_ids"), list):
        result["chunk_ids"] = [str(item) for item in result["chunk_ids"]]
    if isinstance(result.get("triples"), list):
        result["triples"] = [normalize_triple(item) for item in result["triples"]]
    return result


def normalize_triple(value: Any) -> Any:
    if isinstance(value, str):
        parsed = parse_triple_string(value)
        if parsed is not None:
            return parsed
        return value.strip()

    if not isinstance(value, dict):
        return value

    triple = dict(value)
    for key in REQUIRED_TRIPLE:
        if key in triple:
            triple[key] = str(triple[key]).strip()
    if "source" in triple and triple["source"] is not None:
        triple["source"] = str(triple["source"])
    return triple


def parse_triple_string(value: str) -> dict[str, str] | None:
    text = value.strip()
    if text.startswith("(") and ") [score:" in text:
        body = text[1:text.index(") [score:")]
    elif text.startswith("[") and text.endswith("]"):
        body = text[1:-1]
    else:
        return None

    parts = [part.strip() for part in body.split(",", 2)]
    if len(parts) != 3 or not all(parts):
        return None
    return {
        "subject": parts[0],
        "relation": parts[1],
        "object": parts[2],
    }


def compare(
    golden: Any,
    actual: Any,
    *,
    float_tolerance: float,
    ignored: set[str],
    strict_extra_fields: bool,
    path: str = "",
) -> list[str]:
    if is_ignored(path, ignored):
        return []

    if isinstance(golden, dict):
        if not isinstance(actual, dict):
            return [f"{path or '<root>'}: expected object, got {type(actual).__name__}"]

        errors: list[str] = []
        for key in sorted(golden):
            child = path_join(path, key)
            if is_ignored(child, ignored):
                continue
            if key not in actual:
                errors.append(f"{child}: missing from actual")
                continue
            errors.extend(
                compare(
                    golden[key],
                    actual[key],
                    float_tolerance=float_tolerance,
                    ignored=ignored,
                    strict_extra_fields=strict_extra_fields,
                    path=child,
                )
            )

        if strict_extra_fields:
            for key in sorted(set(actual) - set(golden)):
                child = path_join(path, key)
                if not is_ignored(child, ignored):
                    errors.append(f"{child}: extra field in actual")
        return errors

    if isinstance(golden, list):
        if not isinstance(actual, list):
            return [f"{path or '<root>'}: expected array, got {type(actual).__name__}"]

        errors = []
        if len(golden) != len(actual):
            errors.append(f"{path or '<root>'}: length {len(actual)} != {len(golden)}")

        for index, (golden_item, actual_item) in enumerate(zip(golden, actual)):
            errors.extend(
                compare(
                    golden_item,
                    actual_item,
                    float_tolerance=float_tolerance,
                    ignored=ignored,
                    strict_extra_fields=strict_extra_fields,
                    path=path_join(path, str(index)),
                )
            )
        return errors

    if isinstance(golden, (int, float)) and isinstance(actual, (int, float)):
        if math.isnan(float(golden)) or math.isnan(float(actual)):
            if math.isnan(float(golden)) == math.isnan(float(actual)):
                return []
            return [f"{path}: value {actual!r} != {golden!r}"]
        if abs(float(golden) - float(actual)) > float_tolerance:
            return [f"{path}: value {actual!r} != {golden!r} within {float_tolerance}"]
        return []

    if actual != golden:
        return [f"{path or '<root>'}: value {actual!r} != {golden!r}"]
    return []


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Compare Python retrieval golden output with Go actual output."
    )
    parser.add_argument("--golden", required=True, type=Path, help="Python golden JSON")
    parser.add_argument("--actual", required=True, type=Path, help="Go actual JSON")
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
        "--record-id",
        default=None,
        help="record id to compare when using retriever-golden/v1 fixtures",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    golden = load_json(args.golden)
    actual = load_json(args.actual)

    golden_target, actual_target, errors = comparison_targets(golden, actual, args.record_id)
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 2

    ignored = set(DEFAULT_IGNORES)
    ignored.update(args.ignore_path)

    diff = compare(
        normalize(golden_target),
        normalize(actual_target),
        float_tolerance=args.float_tolerance,
        ignored=ignored,
        strict_extra_fields=args.strict_extra_fields,
    )

    if diff:
        print(f"retrieval golden mismatch: {len(diff)} difference(s)", file=sys.stderr)
        for item in diff:
            print(f"- {item}", file=sys.stderr)
        return 1

    print(f"retrieval golden matched: {args.actual}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
