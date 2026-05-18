#!/usr/bin/env python3
"""Validate the documented release surface without third-party dependencies."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

EXPECTED_ROUTES = {
    "GET /healthz",
    "GET /readyz",
    "GET /v1/version",
    "GET /v1/datasets",
    "POST /v1/datasets/import",
    "GET /v1/datasets/{dataset}",
    "DELETE /v1/datasets/{dataset}",
    "GET /v1/datasets/{dataset}/artifacts",
    "POST /v1/datasets/{dataset}/rebuild",
    "GET /v1/schemas",
    "POST /v1/schemas/validate",
    "GET /v1/datasets/{dataset}/schema",
    "PUT /v1/datasets/{dataset}/schema",
    "GET /v1/dataset-operations",
    "GET /v1/dataset-operations/{operation_id}",
    "GET /v1/datasets/{dataset}/operations",
    "GET /v1/sidecars",
    "GET /v1/sidecars/vector/health",
    "POST /v1/retrieve",
    "POST /v1/jobs",
    "GET /v1/jobs/{job_id}",
    "GET /v1/jobs/{job_id}/events",
    "POST /v1/jobs/{job_id}/cancel",
    "GET /v1/workflows",
    "POST /v1/workflows",
    "GET /v1/workflows/{workflow_id}",
    "GET /v1/workflows/{workflow_id}/steps",
    "GET /v1/workflows/{workflow_id}/steps/{step_name}",
    "GET /v1/workflows/{workflow_id}/events",
    "POST /v1/workflows/{workflow_id}/cancel",
}

EXPECTED_SCHEMA_TOKENS = {
    "service-job/v1",
    "workflow/v1",
    "dataset-import/v1",
    "dataset-rebuild/v1",
    "dataset-delete/v1",
    "dataset-operation/v1",
    "dataset-schema/v1",
    "dataset-schema-update/v1",
    "schema-validation/v1",
}

SCRIPT_EXPECTATIONS = {
    "scripts/run_service_smoke.sh": [
        "Usage: scripts/run_service_smoke.sh",
        "SMOKE_ARTIFACT_ROOT",
        "/v1/schemas/validate",
        "/v1/datasets/import",
        "/v1/retrieve",
        "/v1/jobs",
        "service smoke passed",
    ],
    "scripts/run_service_local.sh": [
        "Usage: scripts/run_service_local.sh",
        "ENV_FILE=.env",
        "--check-only",
        "--check-config",
    ],
    "scripts/run_demo_service_smoke.sh": [
        "Usage: scripts/run_demo_service_smoke.sh",
        "YOUTU_RAG_ARTIFACT_ROOT",
        "/v1/sidecars/vector/health",
        "/v1/retrieve",
        "/v1/jobs",
        "real demo service smoke passed",
    ],
    ".env.example": [
        "YOUTU_RAG_PROFILE=demo",
        "YOUTU_RAG_ARTIFACT_ROOT=../youtu-graphrag",
        "YOUTU_RAG_VALIDATE_ON_START=true",
        "YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765",
    ],
}


def openapi_routes(body: str) -> set[str]:
    routes: set[str] = set()
    current_path: str | None = None
    for line in body.splitlines():
        path_match = re.match(r"^  (/[^:]+):\s*$", line)
        if path_match:
            current_path = path_match.group(1)
            continue
        method_match = re.match(r"^    (get|post|put|delete):\s*$", line)
        if current_path and method_match:
            routes.add(f"{method_match.group(1).upper()} {current_path}")
    return routes


def require(condition: bool, message: str, errors: list[str]) -> None:
    if not condition:
        errors.append(message)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo-root", default=".")
    args = parser.parse_args()
    root = Path(args.repo_root)
    errors: list[str] = []

    openapi_path = root / "docs" / "openapi.yaml"
    try:
        openapi = openapi_path.read_text(encoding="utf-8")
    except FileNotFoundError:
        errors.append(f"missing {openapi_path}")
        openapi = ""

    require("openapi: 3.1.0" in openapi, "openapi.yaml must declare OpenAPI 3.1.0", errors)
    routes = openapi_routes(openapi)
    missing_routes = sorted(EXPECTED_ROUTES - routes)
    extra_routes = sorted(routes - EXPECTED_ROUTES)
    require(not missing_routes, f"openapi.yaml missing routes: {missing_routes}", errors)
    require(not extra_routes, f"openapi.yaml has unexpected routes: {extra_routes}", errors)
    for token in sorted(EXPECTED_SCHEMA_TOKENS):
        require(token in openapi, f"openapi.yaml missing schema token {token}", errors)

    makefile = (root / "Makefile").read_text(encoding="utf-8") if (root / "Makefile").exists() else ""
    require("service-smoke:" in makefile, "Makefile missing service-smoke target", errors)
    require("scripts/run_service_smoke.sh" in makefile, "service-smoke must call scripts/run_service_smoke.sh", errors)
    require("demo-service-smoke:" in makefile, "Makefile missing demo-service-smoke target", errors)
    require(
        "scripts/run_demo_service_smoke.sh" in makefile,
        "demo-service-smoke must call scripts/run_demo_service_smoke.sh",
        errors,
    )

    for relative, tokens in SCRIPT_EXPECTATIONS.items():
        path = root / relative
        try:
            body = path.read_text(encoding="utf-8")
        except FileNotFoundError:
            errors.append(f"missing {relative}")
            continue
        for token in tokens:
            require(token in body, f"{relative} missing {token!r}", errors)

    if errors:
        for error in errors:
            print(f"release surface check failed: {error}", file=sys.stderr)
        return 1
    print(f"release surface ok: {len(routes)} routes")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
