#!/usr/bin/env sh
set -eu

DEFAULT_QUESTION="When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?"

usage() {
  cat <<'EOF'
Usage: scripts/run_demo_service_smoke.sh [--keep-logs]

Runs a real demo retrieval smoke through the Go HTTP service and the Python
vector sidecar. Unlike scripts/run_service_smoke.sh, this uses the real
youtu-graphrag demo artifacts, sentence-transformers/FAISS sidecar, and golden
comparison harness.

Environment overrides:
  YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag
  YOUTU_RAG_HTTP_ADDR=127.0.0.1:18082
  YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765
  DEMO_SMOKE_QUESTION="..."
  DEMO_SMOKE_TOP_K=20
  RECORD_ID=qa_1
  KEEP_DEMO_SERVICE_LOGS=1
EOF
}

keep_logs="${KEEP_DEMO_SERVICE_LOGS:-0}"
for arg in "$@"; do
  case "$arg" in
    --help|-h)
      usage
      exit 0
      ;;
    --keep-logs)
      keep_logs=1
      ;;
    *)
      echo "unknown argument: $arg" >&2
      usage >&2
      exit 2
      ;;
  esac
done

ROOT="${YOUTU_RAG_ARTIFACT_ROOT:-../youtu-graphrag}"
SERVICE_ADDR="${YOUTU_RAG_HTTP_ADDR:-127.0.0.1:18082}"
SERVICE_URL="http://$SERVICE_ADDR"
SIDECAR_URL="${YOUTU_RAG_SIDECAR_URL:-http://127.0.0.1:8765}"
DATASET="${DATASET:-demo}"
QUESTION="${DEMO_SMOKE_QUESTION:-${QUESTION:-$DEFAULT_QUESTION}}"
TOP_K="${DEMO_SMOKE_TOP_K:-${TOP_K:-20}}"
RECORD_ID="${RECORD_ID:-qa_1}"
OUT_DIR="${OUT_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/youtu-rag-demo-service-smoke.XXXXXX")}"

SERVICE_PID=""
SIDECAR_PID=""
cleanup() {
  status="${1:-$?}"
  if [ "$SERVICE_PID" ]; then
    kill "$SERVICE_PID" 2>/dev/null || true
    wait "$SERVICE_PID" 2>/dev/null || true
  fi
  if [ "$SIDECAR_PID" ]; then
    kill "$SIDECAR_PID" 2>/dev/null || true
    wait "$SIDECAR_PID" 2>/dev/null || true
  fi
  if [ "$status" != "0" ]; then
    echo "real demo service smoke failed; logs kept at $OUT_DIR" >&2
    [ -f "$OUT_DIR/service.log" ] && echo "Go service log: $OUT_DIR/service.log" >&2
    [ -f "$OUT_DIR/sidecar.log" ] && echo "Python sidecar log: $OUT_DIR/sidecar.log" >&2
  elif [ "$keep_logs" != "1" ] && [ -z "${OUT_DIR_KEEP:-}" ]; then
    rm -rf "$OUT_DIR"
  fi
}
on_exit() {
  status="$?"
  trap - EXIT INT TERM
  cleanup "$status"
  exit "$status"
}
trap on_exit EXIT INT TERM

require_file() {
  label="$1"
  path="$2"
  if [ ! -f "$path" ]; then
    cat >&2 <<EOF
missing $label: $path

Set YOUTU_RAG_ARTIFACT_ROOT to a youtu-graphrag checkout with generated demo
artifacts, for example:

  YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag make demo-service-smoke
EOF
    exit 2
  fi
}

require_dir() {
  label="$1"
  path="$2"
  if [ ! -d "$path" ]; then
    echo "missing $label: $path" >&2
    exit 2
  fi
}

PYTHON_BIN="${YOUTU_RAG_PYTHON:-$ROOT/.venv/bin/python}"
GRAPH="$ROOT/output/graphs/${DATASET}_new.json"
CHUNKS="$ROOT/output/chunks/${DATASET}.txt"
SCHEMA="$ROOT/schemas/${DATASET}.json"
GOLDEN="$ROOT/output/retrieval_golden/${DATASET}.json"
CACHE_DIR="$ROOT/retriever/faiss_cache_new/$DATASET"
SIDECAR_SCRIPT="$ROOT/scripts/vector_sidecar.py"

require_file "Python interpreter" "$PYTHON_BIN"
require_file "Python vector sidecar" "$SIDECAR_SCRIPT"
require_file "graph JSON" "$GRAPH"
require_file "chunks file" "$CHUNKS"
require_file "schema JSON" "$SCHEMA"
require_dir "FAISS cache" "$CACHE_DIR"

mkdir -p "$OUT_DIR"

SIDECAR_HOST_PORT="${SIDECAR_URL#http://}"
SIDECAR_HOST_PORT="${SIDECAR_HOST_PORT#https://}"
SIDECAR_HOST_PORT="${SIDECAR_HOST_PORT%%/*}"
SIDECAR_HOST="${SIDECAR_HOST_PORT%:*}"
SIDECAR_PORT="${SIDECAR_HOST_PORT##*:}"

if [ "$SIDECAR_HOST" = "$SIDECAR_PORT" ] || [ -z "$SIDECAR_HOST" ] || [ -z "$SIDECAR_PORT" ]; then
  echo "YOUTU_RAG_SIDECAR_URL must include host and port, got: $SIDECAR_URL" >&2
  exit 2
fi

echo "artifact root: $ROOT" >&2
echo "graph: $GRAPH" >&2
echo "chunks: $CHUNKS" >&2
echo "schema: $SCHEMA" >&2
echo "cache: $CACHE_DIR" >&2
echo "sidecar: $SIDECAR_URL" >&2
echo "service: $SERVICE_URL" >&2

check_sidecar() {
  python3 - "$SIDECAR_URL" "$DATASET" <<'PY'
import json
import sys
import time
import urllib.request

base, dataset = sys.argv[1].rstrip("/"), sys.argv[2]
with urllib.request.urlopen(f"{base}/v1/datasets/{dataset}/cache", timeout=5) as resp:
    data = json.loads(resp.read().decode("utf-8"))
if data.get("dataset") != dataset:
    raise SystemExit(f"unexpected sidecar cache response: {data}")
print("sidecar cache health ok")
PY
}

if check_sidecar >/dev/null 2>&1; then
  echo "using existing Python vector sidecar at $SIDECAR_URL" >&2
else
  echo "starting Python vector sidecar at $SIDECAR_URL" >&2
  (
    cd "$ROOT"
    HF_HUB_OFFLINE="${HF_HUB_OFFLINE:-1}" \
    TRANSFORMERS_OFFLINE="${TRANSFORMERS_OFFLINE:-1}" \
    TOKENIZERS_PARALLELISM=false \
    OMP_NUM_THREADS=1 \
    MKL_NUM_THREADS=1 \
    VECLIB_MAXIMUM_THREADS=1 \
    NUMEXPR_NUM_THREADS=1 \
    "$PYTHON_BIN" "$SIDECAR_SCRIPT" --host "$SIDECAR_HOST" --port "$SIDECAR_PORT"
  ) > "$OUT_DIR/sidecar.log" 2>&1 &
  SIDECAR_PID="$!"

  python3 - "$SIDECAR_URL" "$DATASET" <<'PY'
import json
import sys
import time
import urllib.request

base, dataset = sys.argv[1].rstrip("/"), sys.argv[2]
deadline = time.time() + 90
last = None
while time.time() < deadline:
    try:
        with urllib.request.urlopen(f"{base}/v1/datasets/{dataset}/cache", timeout=5) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        if data.get("dataset") == dataset:
            print("sidecar cache health ok")
            break
    except Exception as exc:
        last = exc
    time.sleep(0.5)
else:
    raise SystemExit(f"sidecar did not become ready: {last}")
PY
fi

export YOUTU_RAG_PROFILE=demo
export YOUTU_RAG_HTTP_ADDR="$SERVICE_ADDR"
export YOUTU_RAG_ARTIFACT_ROOT="$ROOT"
export YOUTU_RAG_DATASET="$DATASET"
export YOUTU_RAG_GRAPH="$GRAPH"
export YOUTU_RAG_CHUNKS="$CHUNKS"
export YOUTU_RAG_MODE=native-path1-rerank
export YOUTU_RAG_SIDECAR_URL="$SIDECAR_URL"
export YOUTU_RAG_VALIDATE_ON_START=true

go run ./cmd/youtu-rag-service --check-config > "$OUT_DIR/check-config.json"

echo "starting Go service at $SERVICE_URL" >&2
go run ./cmd/youtu-rag-service > "$OUT_DIR/service.log" 2>&1 &
SERVICE_PID="$!"

ACTUAL="$OUT_DIR/demo-service-actual.json"
python3 - "$SERVICE_URL" "$DATASET" "$QUESTION" "$TOP_K" "$ACTUAL" <<'PY'
import json
import sys
import time
import urllib.error
import urllib.request

base, dataset, question, top_k, actual_path = sys.argv[1:6]


def request(method, path, body=None, timeout=120):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["content-type"] = "application/json"
    req = urllib.request.Request(base.rstrip("/") + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} failed with HTTP {exc.code}: {detail}") from exc


deadline = time.time() + 45
while True:
    try:
        health = request("GET", "/healthz", timeout=5)
        if health.get("status") == "ok":
            break
    except Exception:
        if time.time() > deadline:
            raise
        time.sleep(0.25)

ready = request("GET", "/readyz", timeout=5)
assert ready.get("ready") is True, ready

sidecars = request("GET", f"/v1/sidecars/vector/health?dataset={dataset}", timeout=20)
assert sidecars.get("reachable") is True, sidecars

result = request("POST", "/v1/retrieve", {
    "dataset": dataset,
    "question": question,
    "top_k": int(top_k),
    "mode": "native-path1-rerank",
}, timeout=180)
strategies = [item.get("name") for item in result.get("debug", {}).get("strategies", [])]
assert "go_path1_rerank_path2_primitive_merge" in strategies, strategies
assert len(result.get("triples", [])) == 17, result
assert len(result.get("chunk_retrieval_results", [])) == 3, result
with open(actual_path, "w", encoding="utf-8") as f:
    json.dump(result, f, ensure_ascii=False, indent=2)
    f.write("\n")

job = request("POST", "/v1/jobs", {
    "type": "retrieve",
    "retrieve": {
        "dataset": dataset,
        "question": question,
        "top_k": int(top_k),
        "mode": "native-path1-rerank",
    }
}, timeout=30)
job_id = job["id"]
for _ in range(120):
    current = request("GET", f"/v1/jobs/{job_id}", timeout=10)
    if current["status"] in {"succeeded", "failed", "canceled"}:
        break
    time.sleep(0.25)
else:
    raise AssertionError(f"job did not finish: {job_id}")
assert current["status"] == "succeeded", current
print(f"demo retrieve and job passed; job_id={job_id}")
PY

if [ -f "$GOLDEN" ]; then
  python3 scripts/retrieval_regression_report.py \
    --golden "$GOLDEN" \
    --actual "$ACTUAL" \
    --record-id "$RECORD_ID" \
    --fail-on loader \
    --fail-on chunk \
    --fail-on triple \
    --fail-on full
else
  echo "golden fixture not found; skipped regression report: $GOLDEN" >&2
fi

echo "real demo service smoke passed"
if [ "$keep_logs" = "1" ] || [ "${OUT_DIR_KEEP:-}" ]; then
  echo "logs kept at $OUT_DIR"
else
  echo "logs cleaned; rerun with KEEP_DEMO_SERVICE_LOGS=1 to inspect them"
fi
