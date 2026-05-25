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
  PAPER_BENCHMARK_PROMPT_MODE  open|reject
  PAPER_BENCHMARK_COMMUNITY    skipped|completed
  PAPER_BENCHMARK_COMPACTION_WAL
  PAPER_BENCHMARK_LLM_ATTEMPTS 3
  PAPER_BENCHMARK_RETRY_FAILED true|false
  PAPER_BENCHMARK_PREFLIGHT_ONLY true|false
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
PROMPT_MODE="${PAPER_BENCHMARK_PROMPT_MODE:-open}"
COMMUNITY_COMPACTION="${PAPER_BENCHMARK_COMMUNITY:-completed}"
COMPACTION_WAL="${PAPER_BENCHMARK_COMPACTION_WAL:-}"
LLM_ATTEMPTS="${PAPER_BENCHMARK_LLM_ATTEMPTS:-3}"
LLM_RETRY_BASE="${PAPER_BENCHMARK_LLM_RETRY_BASE:-2}"
LLM_RETRY_MAX="${PAPER_BENCHMARK_LLM_RETRY_MAX:-30}"
RETRY_FAILED="${PAPER_BENCHMARK_RETRY_FAILED:-true}"
PREFLIGHT_ONLY="${PAPER_BENCHMARK_PREFLIGHT_ONLY:-false}"

if [ ! -d "$ROOT" ]; then
  echo "paper benchmark failed: artifact root not found: $ROOT" >&2
  exit 2
fi

if [ "$PREFLIGHT_ONLY" != "true" ] && [ -z "${LLM_API_KEY:-}" ]; then
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
if [ "$COMMUNITY_COMPACTION" = "completed" ]; then
  GRAPH="$ROOT/output/graphs/${DATASET}_full_flash_community.json"
  CHUNKS="$ROOT/output/chunks/${DATASET}_full_flash_community.txt"
  CACHE_DIR="$ROOT/retriever/faiss_cache_new/${DATASET}_full_flash_community"
  if [ -z "$COMPACTION_WAL" ]; then
    COMPACTION_WAL="$ROOT/output/graph_wal/${DATASET}_full_flash_community.compaction.jsonl"
  fi
else
  GRAPH="$ROOT/output/graphs/${DATASET}_full_flash_new.json"
  CHUNKS="$ROOT/output/chunks/${DATASET}_full_flash.txt"
  CACHE_DIR="$ROOT/retriever/faiss_cache_new/${DATASET}_full_flash"
fi
SCHEMA="$ROOT/schemas/$DATASET.json"
OUT_DIR="$ROOT/output/benchmarks"
LIMIT_LABEL="limit${LIMIT}"
if [ "$LIMIT" = "0" ]; then
  LIMIT_LABEL="full"
fi
RUN_LABEL="${DATASET}_${MODEL}_method_${MODE}_${PROMPT_MODE}_${COMMUNITY_COMPACTION}_${LIMIT_LABEL}"
OUTPUT="$OUT_DIR/${RUN_LABEL}.json"
CHECKPOINT="$OUT_DIR/${RUN_LABEL}.checkpoint.jsonl"
PROGRESS="$OUT_DIR/${RUN_LABEL}.progress.json"

for path in "$QA" "$GRAPH" "$CHUNKS" "$SCHEMA"; do
  if [ ! -f "$path" ]; then
    echo "paper benchmark failed: required file missing: $path" >&2
    exit 2
  fi
done

if [ "$COMMUNITY_COMPACTION" = "completed" ] && [ ! -f "$COMPACTION_WAL" ]; then
  echo "paper benchmark failed: required compaction WAL missing: $COMPACTION_WAL" >&2
  exit 2
fi

mkdir -p "$OUT_DIR"

PYTHON="$ROOT/.venv/bin/python"
if [ ! -x "$PYTHON" ]; then
  PYTHON="python3"
fi

RETRY_FAILED_ARG=""
if [ "$RETRY_FAILED" = "true" ]; then
  RETRY_FAILED_ARG="--retry-failed"
fi
PREFLIGHT_ARG=""
if [ "$PREFLIGHT_ONLY" = "true" ]; then
  PREFLIGHT_ARG="--preflight-only"
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
  --llm-max-attempts "$LLM_ATTEMPTS" \
  --llm-retry-base-seconds "$LLM_RETRY_BASE" \
  --llm-retry-max-seconds "$LLM_RETRY_MAX" \
  --prompt-mode "$PROMPT_MODE" \
  --community-compaction "$COMMUNITY_COMPACTION" \
  --compaction-wal "$COMPACTION_WAL" \
  --resume \
  ${RETRY_FAILED_ARG:+$RETRY_FAILED_ARG} \
  ${PREFLIGHT_ARG:+$PREFLIGHT_ARG}

if [ "$PREFLIGHT_ONLY" = "true" ]; then
  echo "paper benchmark preflight passed: $OUTPUT"
  exit 0
fi

python3 scripts/check_paper_benchmark_result.py \
  --result "$OUTPUT" \
  --checkpoint "$CHECKPOINT" \
  --progress "$PROGRESS" \
  --dataset "$DATASET" \
  --mode "$MODE" \
  --limit "$LIMIT" \
  --prompt-mode "$PROMPT_MODE" \
  --community-compaction "$COMMUNITY_COMPACTION"

echo "paper benchmark smoke passed: $OUTPUT"
