#!/bin/sh
set -eu

SIDECAR_URL="${SIDECAR_URL:-http://127.0.0.1:8765}"
GRAPH="${GRAPH:-../youtu-graphrag/output/graphs/demo_new.json}"
CHUNKS="${CHUNKS:-../youtu-graphrag/output/chunks/demo.txt}"
GOLDEN="${GOLDEN:-../youtu-graphrag/output/retrieval_golden/demo.json}"
RECORD_ID="${RECORD_ID:-qa_1}"
DEFAULT_QUESTION="When was the person who Messi's goals in Copa del Rey compared to get signed by Barcelona?"
QUESTION="${QUESTION:-$DEFAULT_QUESTION}"
TOP_K="${TOP_K:-20}"
DATASET="${DATASET:-demo}"
OUT_DIR="${OUT_DIR:-/tmp/youtu-rag-service-demo-gates}"
MODES="${MODES:-runtime-trace primitive-merge rerank-merge native-path1-rerank}"

mkdir -p "$OUT_DIR"

require_file() {
  label="$1"
  path="$2"
  if [ ! -f "$path" ]; then
    cat >&2 <<EOF
missing $label: $path

Set explicit paths when running from a clean clone, for example:

  GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json \\
  CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt \\
  GOLDEN=/abs/path/youtu-graphrag/output/retrieval_golden/demo.json \\
  SIDECAR_URL=$SIDECAR_URL \\
  scripts/run_demo_gates.sh
EOF
    exit 2
  fi
}

require_file "graph JSON" "$GRAPH"
require_file "chunks file" "$CHUNKS"
require_file "golden fixture" "$GOLDEN"

run_mode() {
  mode="$1"
  actual="$OUT_DIR/${mode}.json"

  echo "==> running mode: $mode"
  go run ./cmd/youtu-retriever retrieve \
    --graph "$GRAPH" \
    --chunks "$CHUNKS" \
    --dataset "$DATASET" \
    --question "$QUESTION" \
    --top-k "$TOP_K" \
    --sidecar-url "$SIDECAR_URL" \
    --mode "$mode" \
    > "$actual"

  python3 scripts/retrieval_regression_report.py \
    --golden "$GOLDEN" \
    --actual "$actual" \
    --record-id "$RECORD_ID" \
    --fail-on loader \
    --fail-on chunk \
    --fail-on triple \
    --fail-on full

  python3 - "$actual" "$mode" <<'PY'
import json
import sys

actual_path, mode = sys.argv[1], sys.argv[2]
expected = {
    "runtime-trace": "python_triple_trace",
    "path2-detrace": "path2_detrace_merge",
    "primitive-merge": "path1_path2_primitive_merge",
    "rerank-merge": "path1_rerank_path2_primitive_merge",
    "native-path1-rerank": "go_path1_rerank_path2_primitive_merge",
}.get(mode)
if not expected:
    sys.exit(0)
with open(actual_path, "r", encoding="utf-8") as f:
    actual = json.load(f)
strategies = actual.get("debug", {}).get("strategies", [])
names = [item.get("name") for item in strategies]
if expected not in names:
    raise SystemExit(f"expected debug strategy {expected!r}, got {names!r}")
print(f"debug strategy ok: {expected}")
PY
}

for mode in $MODES; do
  run_mode "$mode"
done

echo "demo gates passed; actual files are in $OUT_DIR"
