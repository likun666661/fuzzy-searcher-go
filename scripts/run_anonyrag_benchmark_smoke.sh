#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage: scripts/run_anonyrag_benchmark_smoke.sh

Runs a real benchmark job through the Go HTTP service using the prepared
AnonyRAG dataset and a real LLM key. The default is intentionally tiny
(`BENCHMARK_LIMIT=1`) to keep cost and latency bounded.

Common overrides:
  YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag
  YOUTU_RAG_HTTP_ADDR=127.0.0.1:18083
  BENCHMARK_DATASET=anony_eng
  BENCHMARK_LIMIT=1
  BENCHMARK_MODEL=deepseek-v4-pro
  LLM_API_KEY="${DEEPSEEK_API_KEY}"
EOF
}

for arg in "$@"; do
  case "$arg" in
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      usage >&2
      exit 2
      ;;
  esac
done

ROOT="${YOUTU_RAG_ARTIFACT_ROOT:-../youtu-graphrag}"
SERVICE_ADDR="${YOUTU_RAG_HTTP_ADDR:-127.0.0.1:18083}"
SERVICE_URL="http://$SERVICE_ADDR"
DATASET="${BENCHMARK_DATASET:-anony_eng}"
LIMIT="${BENCHMARK_LIMIT:-1}"
MODEL="${BENCHMARK_MODEL:-${LLM_MODEL:-deepseek-v4-pro}}"
BASE_URL="${BENCHMARK_LLM_BASE_URL:-${LLM_BASE_URL:-https://api.deepseek.com}}"
OUT_DIR="${OUT_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/youtu-rag-anonyrag-benchmark.XXXXXX")}"

if [ -z "${LLM_API_KEY:-}" ] && [ -n "${DEEPSEEK_API_KEY:-}" ]; then
  export LLM_API_KEY="$DEEPSEEK_API_KEY"
fi
if [ -z "${LLM_API_KEY:-}" ]; then
  echo "missing LLM key: set LLM_API_KEY or DEEPSEEK_API_KEY" >&2
  exit 2
fi

PYTHON_BIN="${YOUTU_RAG_PYTHON:-$ROOT/.venv/bin/python}"
QA="$ROOT/data/$DATASET/final_qa_pairs.json"
CORPUS="$ROOT/data/$DATASET/final_chunk_corpus.json"
SCHEMA="$ROOT/schemas/$DATASET.json"
SCRIPT="$(pwd)/scripts/benchmark_worker.py"
OUTPUT="${BENCHMARK_OUTPUT:-$ROOT/output/benchmarks/${DATASET}_${MODEL}_smoke.json}"

require_file() {
  label="$1"
  path="$2"
  if [ ! -f "$path" ]; then
    cat >&2 <<EOF
missing $label: $path

Run the AnonyRAG preparation helper first:

  scripts/prepare_anonyrag.py --artifact-root "$ROOT"
EOF
    exit 2
  fi
}

require_file "Python interpreter" "$PYTHON_BIN"
require_file "AnonyRAG QA" "$QA"
require_file "AnonyRAG corpus" "$CORPUS"
require_file "AnonyRAG schema" "$SCHEMA"
require_file "benchmark worker" "$SCRIPT"

SERVICE_PID=""
cleanup() {
  status="${1:-$?}"
  if [ "$SERVICE_PID" ]; then
    kill "$SERVICE_PID" 2>/dev/null || true
    wait "$SERVICE_PID" 2>/dev/null || true
  fi
  if [ "$status" != "0" ]; then
    echo "AnonyRAG benchmark smoke failed; logs kept at $OUT_DIR" >&2
    [ -f "$OUT_DIR/service.log" ] && echo "Go service log: $OUT_DIR/service.log" >&2
  else
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

mkdir -p "$OUT_DIR"

export YOUTU_RAG_PROFILE=local
export YOUTU_RAG_HTTP_ADDR="$SERVICE_ADDR"
export YOUTU_RAG_ARTIFACT_ROOT="$ROOT"
export YOUTU_RAG_DATASET="$DATASET"
export YOUTU_RAG_DATASETS="$DATASET"
export YOUTU_RAG_PYTHON="$PYTHON_BIN"
export YOUTU_RAG_BENCHMARK_SCRIPT="$SCRIPT"
export YOUTU_RAG_WORKER_CWD="$(pwd)"
export YOUTU_RAG_VALIDATE_ON_START=false

echo "artifact root: $ROOT" >&2
echo "dataset: $DATASET" >&2
echo "qa: $QA" >&2
echo "corpus: $CORPUS" >&2
echo "service: $SERVICE_URL" >&2
echo "output: $OUTPUT" >&2

go run ./cmd/youtu-rag-service > "$OUT_DIR/service.log" 2>&1 &
SERVICE_PID="$!"

python3 - "$SERVICE_URL" "$DATASET" "$QA" "$CORPUS" "$SCHEMA" "$OUTPUT" "$LIMIT" "$MODEL" "$BASE_URL" <<'PY'
import json
import sys
import time
import urllib.error
import urllib.request

base, dataset, qa, corpus, schema, output, limit, model, base_url = sys.argv[1:10]


def request(method, path, body=None, timeout=180):
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

created = request("POST", "/v1/jobs", {
    "type": "benchmark",
    "benchmark": {
        "dataset": dataset,
        "qa_path": qa,
        "corpus_path": corpus,
        "schema_path": schema,
        "output_path": output,
        "limit": int(limit),
        "mode": "noagent",
        "top_k": 20,
        "answer_model": model,
        "judge_model": model,
        "llm_base_url": base_url,
    },
}, timeout=30)
job_id = created["id"]
for _ in range(360):
    job = request("GET", f"/v1/jobs/{job_id}", timeout=20)
    if job["status"] in {"succeeded", "failed", "canceled"}:
        break
    time.sleep(1)
else:
    raise AssertionError(f"benchmark job did not finish: {job_id}")

if job["status"] != "succeeded":
    raise AssertionError(json.dumps(job, ensure_ascii=False, indent=2))
result = job.get("result") or {}
if result.get("schema_version") != "benchmark-job-result/v1":
    raise AssertionError(result)
if int(result.get("question_count") or 0) <= 0:
    raise AssertionError(result)
with open(output, "r", encoding="utf-8") as f:
    payload = json.load(f)
if payload.get("schema_version") != "benchmark-result/v1":
    raise AssertionError(payload)
print(json.dumps({
    "job_id": job_id,
    "dataset": dataset,
    "question_count": payload.get("question_count"),
    "correct_count": payload.get("correct_count"),
    "accuracy": payload.get("accuracy"),
    "output_path": output,
}, ensure_ascii=False))
PY

echo "AnonyRAG benchmark smoke passed"
