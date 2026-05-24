#!/bin/sh
set -eu

usage() {
  cat <<'USAGE'
Usage: scripts/run_paper_benchmark_smoke.sh

Runs a paper-aligned AnonyRAG CHS benchmark through the original
Youtu-GraphRAG GraphQ/KTRetriever/Eval chain.

Required environment:
  DEEPSEEK_API_KEY or LLM_API_KEY

Common overrides:
  YOUTU_RAG_ARTIFACT_ROOT       ../youtu-graphrag
  PAPER_BENCHMARK_DATASET      anony_chs
  PAPER_BENCHMARK_MODE         agent
  PAPER_BENCHMARK_LIMIT        20
  PAPER_BENCHMARK_MODEL        deepseek-v4-flash
  PAPER_BENCHMARK_TIMEOUT      180
  PAPER_BENCHMARK_COMMUNITY    skipped|completed
  PAPER_BENCHMARK_COMPACTION_WAL
USAGE
}

if [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

ROOT="${YOUTU_RAG_ARTIFACT_ROOT:-../youtu-graphrag}"
DATASET="${PAPER_BENCHMARK_DATASET:-anony_chs}"
MODE="${PAPER_BENCHMARK_MODE:-agent}"
LIMIT="${PAPER_BENCHMARK_LIMIT:-20}"
MODEL="${PAPER_BENCHMARK_MODEL:-deepseek-v4-flash}"
TIMEOUT="${PAPER_BENCHMARK_TIMEOUT:-180}"
COMMUNITY_COMPACTION="${PAPER_BENCHMARK_COMMUNITY:-skipped}"
COMPACTION_WAL="${PAPER_BENCHMARK_COMPACTION_WAL:-}"

if [ ! -d "$ROOT" ]; then
  echo "paper benchmark failed: artifact root not found: $ROOT" >&2
  exit 2
fi

if [ -z "${LLM_API_KEY:-}" ]; then
  if [ -n "${DEEPSEEK_API_KEY:-}" ]; then
    export LLM_API_KEY="$DEEPSEEK_API_KEY"
  else
    echo "paper benchmark failed: set LLM_API_KEY or DEEPSEEK_API_KEY" >&2
    exit 2
  fi
fi

export LLM_BASE_URL="${LLM_BASE_URL:-https://api.deepseek.com}"
export LLM_MODEL="$MODEL"
export TOKENIZERS_PARALLELISM="${TOKENIZERS_PARALLELISM:-false}"
export OMP_NUM_THREADS="${OMP_NUM_THREADS:-1}"
export MKL_NUM_THREADS="${MKL_NUM_THREADS:-1}"
export VECLIB_MAXIMUM_THREADS="${VECLIB_MAXIMUM_THREADS:-1}"
export NUMEXPR_NUM_THREADS="${NUMEXPR_NUM_THREADS:-1}"

QA="$ROOT/data/$DATASET/final_qa_pairs.json"
GRAPH="$ROOT/output/graphs/${DATASET}_full_flash_new.json"
CHUNKS="$ROOT/output/chunks/${DATASET}_full_flash.txt"
SCHEMA="$ROOT/schemas/$DATASET.json"
CACHE_DIR="$ROOT/retriever/faiss_cache_new/${DATASET}_full_flash"
OUT_DIR="$ROOT/output/benchmarks"
OUTPUT="$OUT_DIR/${DATASET}_${MODEL}_paper_${MODE}_limit${LIMIT}.json"
CHECKPOINT="$OUT_DIR/${DATASET}_${MODEL}_paper_${MODE}_limit${LIMIT}.checkpoint.jsonl"
PROGRESS="$OUT_DIR/${DATASET}_${MODEL}_paper_${MODE}_limit${LIMIT}.progress.json"

for path in "$QA" "$GRAPH" "$CHUNKS" "$SCHEMA"; do
  if [ ! -f "$path" ]; then
    echo "paper benchmark failed: required file missing: $path" >&2
    exit 2
  fi
done

mkdir -p "$OUT_DIR"

PYTHON="$ROOT/.venv/bin/python"
if [ ! -x "$PYTHON" ]; then
  PYTHON="python3"
fi

"$PYTHON" scripts/paper_benchmark_worker.py \
  --original-root "$ROOT" \
  --dataset "$DATASET" \
  --qa "$QA" \
  --graph "$GRAPH" \
  --chunks "$CHUNKS" \
  --schema "$SCHEMA" \
  --cache-dir "$CACHE_DIR" \
  --output "$OUTPUT" \
  --checkpoint "$CHECKPOINT" \
  --progress "$PROGRESS" \
  --mode "$MODE" \
  --limit "$LIMIT" \
  --top-k 20 \
  --recall-paths 2 \
  --max-agent-steps 5 \
  --llm-timeout-seconds "$TIMEOUT" \
  --community-compaction "$COMMUNITY_COMPACTION" \
  --compaction-wal "$COMPACTION_WAL" \
  --resume

python3 scripts/check_paper_benchmark_result.py \
  --result "$OUTPUT" \
  --checkpoint "$CHECKPOINT" \
  --progress "$PROGRESS" \
  --dataset "$DATASET" \
  --mode "$MODE" \
  --limit "$LIMIT" \
  --community-compaction "$COMMUNITY_COMPACTION"

echo "paper benchmark smoke passed: $OUTPUT"
