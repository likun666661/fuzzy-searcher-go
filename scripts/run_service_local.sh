#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage: scripts/run_service_local.sh [--check-only]

Runs the Go Youtu-RAG service with local/demo defaults.

Common overrides:
  YOUTU_RAG_PROFILE=local|demo|production
  YOUTU_RAG_ARTIFACT_ROOT=/abs/path/youtu-graphrag
  YOUTU_RAG_GRAPH=/abs/path/youtu-graphrag/output/graphs/demo_new.json
  YOUTU_RAG_CHUNKS=/abs/path/youtu-graphrag/output/chunks/demo.txt
  YOUTU_RAG_SIDECAR_URL=http://127.0.0.1:8765
  YOUTU_RAG_MODE=native-path1-rerank

Use --check-only to print the startup validation report without running.
EOF
}

check_only=0
for arg in "$@"; do
  case "$arg" in
    --help|-h)
      usage
      exit 0
      ;;
    --check-only)
      check_only=1
      ;;
    *)
      echo "unknown argument: $arg" >&2
      usage >&2
      exit 2
      ;;
  esac
done

: "${YOUTU_RAG_PROFILE:=demo}"
: "${YOUTU_RAG_ARTIFACT_ROOT:=../youtu-graphrag}"
: "${YOUTU_RAG_DATASET:=demo}"
: "${YOUTU_RAG_GRAPH:=$YOUTU_RAG_ARTIFACT_ROOT/output/graphs/demo_new.json}"
: "${YOUTU_RAG_CHUNKS:=$YOUTU_RAG_ARTIFACT_ROOT/output/chunks/demo.txt}"
: "${YOUTU_RAG_MODE:=native-path1-rerank}"

export YOUTU_RAG_PROFILE
export YOUTU_RAG_ARTIFACT_ROOT
export YOUTU_RAG_DATASET
export YOUTU_RAG_GRAPH
export YOUTU_RAG_CHUNKS
export YOUTU_RAG_MODE

if [ "$check_only" = "1" ]; then
  exec go run ./cmd/youtu-rag-service --check-config
fi

go run ./cmd/youtu-rag-service --check-config
echo "starting youtu-rag-service at ${YOUTU_RAG_HTTP_ADDR:-:8080}" >&2
exec go run ./cmd/youtu-rag-service
