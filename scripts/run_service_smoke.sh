#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage: scripts/run_service_smoke.sh [--keep-artifacts]

Starts youtu-rag-service against a temporary tiny artifact root and verifies the
release surface without requiring the Python sidecar or model-heavy workers.

Environment overrides:
  SMOKE_ADDR=127.0.0.1:18080
  SMOKE_ARTIFACT_ROOT=/tmp/youtu-rag-service-smoke
  KEEP_SMOKE_ARTIFACTS=1
EOF
}

keep_artifacts="${KEEP_SMOKE_ARTIFACTS:-0}"
for arg in "$@"; do
  case "$arg" in
    --help|-h)
      usage
      exit 0
      ;;
    --keep-artifacts)
      keep_artifacts=1
      ;;
    *)
      echo "unknown argument: $arg" >&2
      usage >&2
      exit 2
      ;;
  esac
done

SMOKE_ADDR="${SMOKE_ADDR:-127.0.0.1:18080}"
SMOKE_URL="http://$SMOKE_ADDR"
if [ "${SMOKE_ARTIFACT_ROOT:-}" ]; then
  ROOT="$SMOKE_ARTIFACT_ROOT"
  mkdir -p "$ROOT"
else
  ROOT="$(mktemp -d "${TMPDIR:-/tmp}/youtu-rag-service-smoke.XXXXXX")"
fi

PID=""
cleanup() {
  if [ "$PID" ]; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  if [ "$keep_artifacts" != "1" ] && [ -z "${SMOKE_ARTIFACT_ROOT:-}" ]; then
    rm -rf "$ROOT"
  fi
}
trap cleanup EXIT INT TERM

mkdir -p \
  "$ROOT/data/demo" \
  "$ROOT/data/incoming" \
  "$ROOT/schemas" \
  "$ROOT/output/graphs" \
  "$ROOT/output/chunks" \
  "$ROOT/retriever/faiss_cache_new/demo"

cat > "$ROOT/data/demo/demo_corpus.json" <<'EOF'
[{"id":"doc1","text":"Alice knows Bob."}]
EOF

cat > "$ROOT/data/incoming/corpus.json" <<'EOF'
[{"id":"doc2","text":"Paris hosts Alice."}]
EOF

cat > "$ROOT/data/incoming/schema.json" <<'EOF'
{"Nodes":["person","location"],"Relations":["knows","located_in"],"Attributes":["name"]}
EOF

cat > "$ROOT/schemas/default.json" <<'EOF'
{"Nodes":["person","location"],"Relations":["knows","located_in"],"Attributes":["name"]}
EOF

cat > "$ROOT/schemas/demo.json" <<'EOF'
{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}
EOF

cat > "$ROOT/output/graphs/demo_new.json" <<'EOF'
[
  {
    "start_node": {"id": "n1", "label": "entity", "properties": {"name": "Alice", "chunk id": "c1", "schema_type": "person"}},
    "relation": "knows",
    "end_node": {"id": "n2", "label": "entity", "properties": {"name": "Bob", "chunk id": "c2", "schema_type": "person"}}
  }
]
EOF

cat > "$ROOT/output/chunks/demo.txt" <<'EOF'
id: c1	Chunk: Alice knows Bob.
id: c2	Chunk: Bob is known by Alice.
EOF

export YOUTU_RAG_PROFILE=demo
export YOUTU_RAG_HTTP_ADDR="$SMOKE_ADDR"
export YOUTU_RAG_ARTIFACT_ROOT="$ROOT"
export YOUTU_RAG_DATASET=demo
export YOUTU_RAG_GRAPH="$ROOT/output/graphs/demo_new.json"
export YOUTU_RAG_CHUNKS="$ROOT/output/chunks/demo.txt"
export YOUTU_RAG_MODE=native
export YOUTU_RAG_VALIDATE_ON_START=true

go run ./cmd/youtu-rag-service --check-config > "$ROOT/check-config.json"
go run ./cmd/youtu-rag-service > "$ROOT/service.log" 2>&1 &
PID="$!"

python3 - "$SMOKE_URL" "$ROOT" <<'PY'
import json
import sys
import time
import urllib.error
import urllib.request

base = sys.argv[1].rstrip("/")
root = sys.argv[2]


def request(method, path, body=None, expect=200):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["content-type"] = "application/json"
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            payload = resp.read().decode("utf-8")
            if resp.status != expect:
                raise AssertionError(f"{method} {path}: status {resp.status}, want {expect}, body={payload}")
            return json.loads(payload) if payload else {}
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode("utf-8")
        if exc.code != expect:
            raise AssertionError(f"{method} {path}: status {exc.code}, want {expect}, body={payload}") from exc
        return json.loads(payload) if payload else {}


deadline = time.time() + 25
while True:
    try:
        health = request("GET", "/healthz")
        if health.get("status") == "ok":
            break
    except Exception:
        if time.time() > deadline:
            raise
        time.sleep(0.25)

ready = request("GET", "/readyz")
assert ready["ready"] is True, ready

version = request("GET", "/v1/version")
assert version["app"], version

datasets = request("GET", "/v1/datasets")
assert any(item["name"] == "demo" for item in datasets["datasets"]), datasets

artifacts = request("GET", "/v1/datasets/demo/artifacts")
assert any(item["name"] == "graph" and item["exists"] for item in artifacts["artifacts"]), artifacts

validation = request("POST", "/v1/schemas/validate", {
    "schema": {
        "Nodes": ["person"],
        "Relations": ["knows"],
        "Attributes": ["name"],
    }
})
assert validation["schema_version"] == "schema-validation/v1" and validation["valid"] is True, validation

schema_update = request("PUT", "/v1/datasets/smoke/schema", {
    "schema": {
        "Nodes": ["person", "location"],
        "Relations": ["located_in"],
        "Attributes": ["name"],
    },
    "overwrite": True,
})
assert schema_update["schema_version"] == "dataset-schema-update/v1", schema_update

schema = request("GET", "/v1/datasets/smoke/schema?include_body=false")
assert schema["schema_version"] == "dataset-schema/v1" and "schema" not in schema, schema

imported = request("POST", "/v1/datasets/import", {
    "dataset": "smoke_import",
    "corpus_path": f"{root}/data/incoming/corpus.json",
    "schema_path": f"{root}/data/incoming/schema.json",
}, expect=201)
assert imported["schema_version"] == "dataset-import/v1", imported

ops = request("GET", "/v1/datasets/smoke_import/operations")
assert ops["count"] >= 1, ops

retrieved = request("POST", "/v1/retrieve", {
    "dataset": "demo",
    "graph_path": f"{root}/output/graphs/demo_new.json",
    "chunks_path": f"{root}/output/chunks/demo.txt",
    "question": "Alice",
    "top_k": 5,
    "mode": "native",
})
assert "triples" in retrieved and "chunk_ids" in retrieved and "debug" in retrieved, retrieved

job = request("POST", "/v1/jobs", {
    "type": "retrieve",
    "retrieve": {
        "dataset": "demo",
        "graph_path": f"{root}/output/graphs/demo_new.json",
        "chunks_path": f"{root}/output/chunks/demo.txt",
        "question": "Alice",
        "top_k": 5,
        "mode": "native",
    },
}, expect=202)
job_id = job["id"]
for _ in range(80):
    current = request("GET", f"/v1/jobs/{job_id}")
    if current["status"] in {"succeeded", "failed", "canceled"}:
        break
    time.sleep(0.1)
else:
    raise AssertionError(f"retrieve job did not finish: {job_id}")
assert current["status"] == "succeeded", current

events = request("GET", f"/v1/jobs/{job_id}/events")
assert events["events"], events

print("service smoke passed")
PY

echo "service smoke passed at $SMOKE_URL"
if [ "$keep_artifacts" = "1" ] || [ "${SMOKE_ARTIFACT_ROOT:-}" ]; then
  echo "smoke artifacts kept at $ROOT"
else
  echo "smoke artifacts cleaned; rerun with KEEP_SMOKE_ARTIFACTS=1 to inspect them"
fi
