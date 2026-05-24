#!/usr/bin/env python3
"""Paper-aligned Youtu-GraphRAG benchmark runner.

This runner intentionally calls the original youtu-graphrag GraphQ,
KTRetriever, and Eval classes. The surrounding code only adds resumable
checkpoint/progress output and stable JSON artifacts for service use.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import signal
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import json_repair

try:
    from benchmark_worker import score_anonymized_mappings
except ImportError:  # pragma: no cover - only used when script is copied alone.
    def score_anonymized_mappings(_gold: str, _predicted: str) -> dict[str, Any]:
        return {"schema_version": "anonymized-mapping-score/v1", "applicable": False}


SCHEMA_VERSION = "paper-benchmark-result/v1"
CHECKPOINT_SCHEMA_VERSION = "paper-benchmark-checkpoint-item/v1"
PROGRESS_SCHEMA_VERSION = "paper-benchmark-progress/v1"
STOP_REQUESTED = False


def handle_stop(_signum: int, _frame: Any) -> None:
    global STOP_REQUESTED
    STOP_REQUESTED = True


signal.signal(signal.SIGINT, handle_stop)
signal.signal(signal.SIGTERM, handle_stop)


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run a paper-aligned Youtu-GraphRAG benchmark.")
    parser.add_argument("--original-root", required=True)
    parser.add_argument("--dataset", default="anony_chs")
    parser.add_argument("--qa", required=True)
    parser.add_argument("--graph", required=True)
    parser.add_argument("--chunks", required=True)
    parser.add_argument("--schema", required=True)
    parser.add_argument("--cache-dir", default="retriever/faiss_cache_new")
    parser.add_argument("--config", default="config/base_config.yaml")
    parser.add_argument("--output", required=True)
    parser.add_argument("--checkpoint", default="")
    parser.add_argument("--progress", default="")
    parser.add_argument("--mode", choices=["agent", "noagent"], default="agent")
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--offset", type=int, default=0)
    parser.add_argument("--top-k", type=int, default=20)
    parser.add_argument("--recall-paths", type=int, default=2)
    parser.add_argument("--max-agent-steps", type=int, default=5)
    parser.add_argument("--llm-timeout-seconds", type=float, default=180.0)
    parser.add_argument("--community-compaction", choices=["skipped", "completed"], default="skipped")
    parser.add_argument("--compaction-wal", default="")
    parser.add_argument("--include-private-traces", action="store_true")
    parser.add_argument("--resume", action="store_true")
    return parser.parse_args()


def load_json(path: str) -> Any:
    with open(path, "r", encoding="utf-8") as f:
        return json_repair.load(f)


def load_qa(path: str, offset: int, limit: int) -> list[dict[str, Any]]:
    data = load_json(path)
    if isinstance(data, dict):
        for key in ("qa_pairs", "questions", "items", "data"):
            if isinstance(data.get(key), list):
                data = data[key]
                break
    if not isinstance(data, list):
        raise SystemExit(f"paper_benchmark_invalid_qa: expected list at {path}")

    rows: list[dict[str, Any]] = []
    for idx, row in enumerate(data):
        if not isinstance(row, dict):
            continue
        question = str(row.get("question") or row.get("query") or "").strip()
        answer = str(row.get("answer") or row.get("gold_answer") or row.get("gold") or "").strip()
        if not question:
            continue
        item = dict(row)
        item["_index"] = idx
        item["_id"] = str(row.get("id") or row.get("qid") or row.get("question_id") or f"qa_{idx + 1}")
        item["_question"] = question
        item["_answer"] = answer
        rows.append(item)

    start = max(offset, 0)
    end = len(rows) if limit <= 0 else min(len(rows), start + limit)
    return rows[start:end]


def load_chunks(path: str) -> dict[str, str]:
    chunks: dict[str, str] = {}
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or "\t" not in line:
                continue
            left, right = line.split("\t", 1)
            if left.startswith("id: ") and right.startswith("Chunk: "):
                chunks[left[4:]] = right[7:].replace("\\n", "\n").replace("\\t", "\t")
    if not chunks:
        raise SystemExit(f"paper_benchmark_invalid_chunks: no chunks loaded from {path}")
    return chunks


def validate_input_paths(args: argparse.Namespace) -> None:
    for label, path in (
        ("qa", args.qa),
        ("graph", args.graph),
        ("chunks", args.chunks),
        ("schema", args.schema),
    ):
        if not path or not os.path.isfile(path):
            raise SystemExit(f"paper_benchmark_missing_artifact: {label} {path}")


def configure_original_repo(root: str) -> None:
    root_path = Path(root).resolve()
    if str(root_path) not in sys.path:
        sys.path.insert(0, str(root_path))
    os.chdir(root_path)


def patch_llm_timeout(timeout: float) -> None:
    if timeout <= 0:
        return
    from utils import call_llm_api

    original = call_llm_api.LLMCompletionCall

    class TimeoutLLMCompletionCall(original):
        def __init__(self):
            super().__init__()
            with_options = getattr(self.client, "with_options", None)
            if callable(with_options):
                self.client = with_options(timeout=timeout)

    call_llm_api.LLMCompletionCall = TimeoutLLMCompletionCall


def load_checkpoint(path: str) -> dict[str, dict[str, Any]]:
    if not path or not os.path.exists(path):
        return {}
    completed: dict[str, dict[str, Any]] = {}
    with open(path, "r", encoding="utf-8") as f:
        for line_no, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                raise SystemExit(f"paper_benchmark_checkpoint_invalid: line {line_no}: {exc}") from exc
            if record.get("schema_version") != CHECKPOINT_SCHEMA_VERSION:
                raise SystemExit(f"paper_benchmark_checkpoint_invalid: line {line_no}: bad schema")
            item_id = str(record.get("id") or "")
            if item_id:
                completed[item_id] = record
    return completed


def append_checkpoint(path: str, item: dict[str, Any]) -> None:
    if not path:
        return
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with open(target, "a", encoding="utf-8") as f:
        f.write(json.dumps(item, ensure_ascii=False) + "\n")
        f.flush()
        os.fsync(f.fileno())


def write_progress(path: str, payload: dict[str, Any]) -> None:
    if not path:
        return
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_suffix(target.suffix + ".tmp")
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(payload, f, ensure_ascii=False, indent=2)
        f.write("\n")
    os.replace(tmp, target)


def normalize_judge(value: str) -> str:
    match = re.search(r"[01]", value or "")
    if not match:
        raise RuntimeError(f"paper_benchmark_judge_invalid: {value[:200]}")
    return match.group(0)


def summarize_detail(detail: dict[str, Any]) -> dict[str, Any]:
    logs = detail.get("logs") if isinstance(detail.get("logs"), list) else []
    initial = detail.get("initial_result") if isinstance(detail.get("initial_result"), dict) else {}
    sub_questions = initial.get("sub_questions") if isinstance(initial.get("sub_questions"), list) else []
    return {
        "schema_version": "paper-benchmark-detail-summary/v1",
        "triples_count": int(detail.get("triples_count") or 0),
        "chunk_count": int(detail.get("chunk_count") or 0),
        "ircot_steps": int(detail.get("ircot_steps") or 0),
        "sub_question_count": len(sub_questions),
        "generated_query_count": sum(1 for log in logs if isinstance(log, dict) and log.get("query")),
        "retrieval_time_seconds": float(detail.get("total_time") or 0),
    }


def sanitize_item(item: dict[str, Any], include_private_traces: bool) -> dict[str, Any]:
    if include_private_traces:
        return item
    sanitized = dict(item)
    detail = sanitized.pop("detail", None)
    if isinstance(detail, dict):
        sanitized["detail_summary"] = summarize_detail(detail)
    return sanitized


def deduplicate_triples(triples: list[str]) -> list[str]:
    return list(set(triples))


def merge_chunk_contents(chunk_ids: list[str], chunk_contents: dict[str, str]) -> list[str]:
    return [chunk_contents.get(chunk_id, f"[Missing content for chunk {chunk_id}]") for chunk_id in chunk_ids]


def rerank_chunks_by_keywords(chunks: list[str], question: str, top_k: int) -> list[str]:
    if len(chunks) <= top_k:
        return chunks
    question_keywords = set(question.lower().split())
    scored = []
    for chunk in chunks:
        chunk_lower = chunk.lower()
        score = sum(1 for keyword in question_keywords if keyword in chunk_lower)
        scored.append((score, chunk))
    scored.sort(key=lambda item: item[0], reverse=True)
    return [chunk for _, chunk in scored[:top_k]]


def normalize_chunk_contents(chunk_ids: list[str], chunk_contents: Any) -> dict[str, str]:
    if isinstance(chunk_contents, dict):
        return {str(key): str(value) for key, value in chunk_contents.items()}
    normalized: dict[str, str] = {}
    if isinstance(chunk_contents, list):
        for idx, chunk_id in enumerate(chunk_ids):
            if idx < len(chunk_contents):
                normalized[str(chunk_id)] = str(chunk_contents[idx])
    return normalized


def initial_question_decomposition(
    graphq: Any,
    kt_retriever: Any,
    config: Any,
    question: str,
    schema_path: str,
) -> dict[str, Any]:
    all_triples: set[str] = set()
    all_chunk_ids: set[str] = set()
    all_chunk_contents: dict[str, str] = {}
    all_sub_question_results: list[dict[str, Any]] = []
    total_time = 0.0
    involved_types = {"nodes": [], "relations": [], "attributes": []}

    try:
        decomposition_result = graphq.decompose(question, schema_path)
        sub_questions = decomposition_result.get("sub_questions", [])
        involved_types = decomposition_result.get("involved_types", involved_types)
    except Exception as exc:
        decomposition_result = {"error": str(exc)}
        sub_questions = [{"sub-question": question}]

    if len(sub_questions) > 1 and config.retrieval.agent.enable_parallel_subquestions:
        aggregated_results, parallel_time = kt_retriever.process_subquestions_parallel(
            sub_questions, top_k=config.retrieval.top_k_filter, involved_types=involved_types
        )
        total_time += float(parallel_time or 0)
        all_triples.update(str(item) for item in (aggregated_results.get("triples") or []))
        all_chunk_ids.update(str(item) for item in (aggregated_results.get("chunk_ids") or []))
        all_chunk_contents.update(normalize_chunk_contents(list(all_chunk_ids), aggregated_results.get("chunk_contents")))
        all_sub_question_results = list(aggregated_results.get("sub_question_results") or [])
    else:
        for i, sub_question in enumerate(sub_questions):
            sub_question_text = str(sub_question.get("sub-question") if isinstance(sub_question, dict) else sub_question)
            try:
                retrieval_results, time_taken = kt_retriever.process_retrieval_results(
                    sub_question_text,
                    top_k=config.retrieval.top_k_filter,
                    involved_types=involved_types,
                )
                total_time += float(time_taken or 0)
                triples = [str(item) for item in (retrieval_results.get("triples") or [])]
                chunk_ids = [str(item) for item in (retrieval_results.get("chunk_ids") or [])]
                chunk_contents = normalize_chunk_contents(chunk_ids, retrieval_results.get("chunk_contents") or [])
                all_triples.update(triples)
                all_chunk_ids.update(chunk_ids)
                all_chunk_contents.update(chunk_contents)
                all_sub_question_results.append({
                    "sub_question": sub_question_text,
                    "triples_count": len(triples),
                    "chunk_ids_count": len(chunk_ids),
                    "time_taken": time_taken,
                })
            except Exception as exc:
                all_sub_question_results.append({
                    "sub_question": sub_question_text,
                    "triples_count": 0,
                    "chunk_ids_count": 0,
                    "time_taken": 0.0,
                    "error": str(exc),
                    "ordinal": i,
                })

    dedup_triples = deduplicate_triples(list(all_triples))
    dedup_chunk_ids = list(set(all_chunk_ids))
    dedup_chunk_contents = merge_chunk_contents(dedup_chunk_ids, all_chunk_contents)
    if not dedup_triples and not dedup_chunk_contents:
        dedup_triples = ["No relevant information found"]
        dedup_chunk_contents = ["No relevant chunks found"]

    if len(dedup_triples) > config.retrieval.top_k_filter:
        question_keywords = set(question.lower().split())
        scored_triples = []
        for triple in dedup_triples:
            triple_lower = triple.lower()
            score = sum(1 for keyword in question_keywords if keyword in triple_lower)
            scored_triples.append((score, triple))
        scored_triples.sort(key=lambda item: item[0], reverse=True)
        dedup_triples = [triple for _, triple in scored_triples[: config.retrieval.top_k_filter]]

    if len(dedup_chunk_contents) > config.retrieval.top_k_filter:
        dedup_chunk_contents = rerank_chunks_by_keywords(dedup_chunk_contents, question, config.retrieval.top_k_filter)

    context = "=== Triples ===\n" + "\n".join(dedup_triples)
    context += "\n=== Chunks ===\n" + "\n".join(dedup_chunk_contents)
    prompt = kt_retriever.generate_prompt(question, context)
    answer = kt_retriever.generate_answer(prompt)
    return {
        "decomposition_result": decomposition_result,
        "sub_questions": sub_questions,
        "involved_types": involved_types,
        "triples": dedup_triples,
        "chunk_ids": dedup_chunk_ids,
        "chunk_contents": dedup_chunk_contents,
        "sub_question_results": all_sub_question_results,
        "initial_answer": answer,
        "total_time": total_time,
    }


def run_agent_question(
    graphq: Any,
    kt_retriever: Any,
    config: Any,
    qa: dict[str, Any],
    schema_path: str,
    max_steps: int,
) -> dict[str, Any]:
    question = qa["_question"]
    initial_result = initial_question_decomposition(graphq, kt_retriever, config, question, schema_path)
    all_triples = set(initial_result["triples"])
    all_chunk_ids = set(initial_result["chunk_ids"])
    all_chunk_contents = {
        chunk_id: content
        for chunk_id, content in zip(initial_result["chunk_ids"], initial_result["chunk_contents"])
    }
    thoughts = [f"Initial analysis (noagent mode): {initial_result['initial_answer']}"]
    logs: list[dict[str, Any]] = []
    current_query = question
    total_time = float(initial_result.get("total_time") or 0)

    for step in range(1, max_steps + 1):
        dedup_triples = deduplicate_triples(list(all_triples))
        dedup_chunk_ids = list(set(all_chunk_ids))
        dedup_chunk_contents = merge_chunk_contents(dedup_chunk_ids, all_chunk_contents)
        context = "=== Triples ===\n" + "\n".join(dedup_triples)
        context += "\n=== Chunks ===\n" + "\n".join(dedup_chunk_contents)
        ircot_prompt = f"""
You are an expert knowledge assistant using iterative retrieval with chain-of-thought reasoning.

Current Question: {current_query}

Available Knowledge Context:
{context}

Previous Thoughts: {' | '.join(thoughts) if thoughts else 'None'}

Step {step}: Please think step by step about what additional information you need to answer the question completely and accurately.

Instructions:
1. Analyze the current knowledge context and the question
2. Consider the initial analysis from noagent mode (if available in previous thoughts)
3. Think about what information might be missing or unclear
4. If you have enough information to answer, in the end of your response, write "So the answer is:" followed by your final answer
5. If you need more information, in the end of your response, write a specific query begin with "The new query is:" to retrieve additional relevant information
6. Be specific and focused in your reasoning
7. Build upon the initial analysis to provide deeper insights

Your reasoning:
"""
        response = kt_retriever.generate_answer(ircot_prompt)
        thoughts.append(response)
        logs.append({
            "step": step,
            "query": current_query,
            "retrieved_triples_count": len(dedup_triples),
            "retrieved_chunks_count": len(dedup_chunk_contents),
            "response": response,
        })
        if "So the answer is:" in response:
            break
        if "The new query is:" in response:
            new_query = response.split("The new query is:", 1)[1].strip()
        else:
            new_query = response.strip()
        if not new_query or new_query == current_query:
            break
        current_query = new_query
        retrieval_results, time_taken = kt_retriever.process_retrieval_results(
            current_query, top_k=config.retrieval.top_k_filter
        )
        total_time += float(time_taken or 0)
        new_triples = [str(item) for item in (retrieval_results.get("triples") or [])]
        new_chunk_ids = [str(item) for item in (retrieval_results.get("chunk_ids") or [])]
        new_chunk_contents = normalize_chunk_contents(new_chunk_ids, retrieval_results.get("chunk_contents") or [])
        all_triples.update(new_triples)
        all_chunk_ids.update(new_chunk_ids)
        all_chunk_contents.update(new_chunk_contents)

    final_context = "=== Final Triples ===\n" + "\n".join(deduplicate_triples(list(all_triples)))
    final_context += "\n=== Final Chunks ===\n" + "\n".join(merge_chunk_contents(list(set(all_chunk_ids)), all_chunk_contents))
    final_prompt = kt_retriever.generate_prompt(question, final_context)
    answer = kt_retriever.generate_answer(final_prompt)
    return {
        "answer": answer,
        "initial_answer": initial_result["initial_answer"],
        "triples_count": len(deduplicate_triples(list(all_triples))),
        "chunk_count": len(merge_chunk_contents(list(set(all_chunk_ids)), all_chunk_contents)),
        "ircot_steps": len(logs),
        "thoughts": thoughts,
        "logs": logs,
        "total_time": total_time,
        "initial_result": {
            "sub_questions": initial_result["sub_questions"],
            "sub_question_results": initial_result["sub_question_results"],
            "triples_count": len(initial_result["triples"]),
            "chunk_count": len(initial_result["chunk_ids"]),
        },
    }


def run_noagent_question(graphq: Any, kt_retriever: Any, config: Any, qa: dict[str, Any], schema_path: str) -> dict[str, Any]:
    result = initial_question_decomposition(graphq, kt_retriever, config, qa["_question"], schema_path)
    return {
        "answer": result["initial_answer"],
        "triples_count": len(result["triples"]),
        "chunk_count": len(result["chunk_ids"]),
        "sub_questions": result["sub_questions"],
        "sub_question_results": result["sub_question_results"],
        "total_time": result["total_time"],
    }


def write_result(path: str, result: dict[str, Any]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with open(target, "w", encoding="utf-8") as f:
        json.dump(result, f, ensure_ascii=False, indent=2)
        f.write("\n")


def build_result(
    args: argparse.Namespace,
    results: list[dict[str, Any]],
    total: int,
    checkpoint_path: str,
    progress_path: str,
    started_at: str,
    started_time: float,
) -> dict[str, Any]:
    answered = sum(1 for item in results if item.get("status") == "succeeded")
    failed = sum(1 for item in results if item.get("status") != "succeeded")
    correct = sum(1 for item in results if item.get("correct"))

    mapping_items = [item.get("mapping_score") or {} for item in results if (item.get("mapping_score") or {}).get("applicable")]
    mapping_expected_count = sum(int(score.get("expected_count") or 0) for score in mapping_items)
    mapping_predicted_count = sum(int(score.get("predicted_count") or 0) for score in mapping_items)
    mapping_matched_count = sum(int(score.get("matched_count") or 0) for score in mapping_items)
    mapping_exact_count = sum(int(score.get("exact_matched_count") or 0) for score in mapping_items)
    mapping_precision = mapping_matched_count / mapping_predicted_count if mapping_predicted_count else (1.0 if mapping_expected_count == 0 else 0.0)
    mapping_recall = mapping_matched_count / mapping_expected_count if mapping_expected_count else 1.0
    mapping_f1 = (2 * mapping_precision * mapping_recall / (mapping_precision + mapping_recall)) if (mapping_precision + mapping_recall) else 0.0

    return {
        "schema_version": SCHEMA_VERSION,
        "benchmark_kind": "paper_aligned",
        "dataset": args.dataset,
        "mode": args.mode,
        "question_count": total,
        "completed_count": len(results),
        "answered_count": answered,
        "failed_count": failed,
        "correct_count": correct,
        "accuracy": correct / total if total else 0.0,
        "duration_ms": int((time.time() - started_time) * 1000),
        "anonymized_mapping": {
            "schema_version": "anonymized-mapping-summary/v1",
            "applicable_count": len(mapping_items),
            "expected_count": mapping_expected_count,
            "predicted_count": mapping_predicted_count,
            "matched_count": mapping_matched_count,
            "exact_matched_count": mapping_exact_count,
            "precision": mapping_precision,
            "recall": mapping_recall,
            "f1": mapping_f1,
            "exact_recall": (mapping_exact_count / mapping_expected_count) if mapping_expected_count else 1.0,
        },
        "artifacts": {
            "qa_path": args.qa,
            "graph_path": args.graph,
            "chunks_path": args.chunks,
            "schema_path": args.schema,
            "cache_dir": args.cache_dir,
            "checkpoint_path": checkpoint_path,
            "progress_path": progress_path,
        },
        "parameters": {
            "top_k": args.top_k,
            "recall_paths": args.recall_paths,
            "max_agent_steps": args.max_agent_steps,
            "llm_model": os.getenv("LLM_MODEL", ""),
            "llm_base_url": os.getenv("LLM_BASE_URL", ""),
            "llm_timeout_seconds": args.llm_timeout_seconds,
            "include_private_traces": args.include_private_traces,
        },
        "paper_config": {
            "constructor_trigger": False,
            "retrieve_trigger": True,
            "mode": args.mode,
            "recall_paths": args.recall_paths,
            "top_k_filter": args.top_k,
            "enable_query_enhancement": True,
            "enable_high_recall": True,
            "enable_reranking": True,
            "agent_max_steps": args.max_agent_steps,
        },
        "deviations": {
            "graph_source": "industrial_wal_full_flash",
            "skip_communities": args.community_compaction != "completed",
            "community_compaction": args.community_compaction,
            "compaction_wal_path": args.compaction_wal,
            "note": "Uses original GraphQ/KTRetriever/Eval retrieval-answer-judge chain; graph was built by resumable WAL worker.",
        },
        "model": {
            "answer_model": os.getenv("LLM_MODEL", ""),
            "judge_model": os.getenv("LLM_MODEL", ""),
            "llm_base_url": os.getenv("LLM_BASE_URL", ""),
        },
        "items": sorted(results, key=lambda item: int(item.get("ordinal") or 0)),
        "started_at": started_at,
        "finished_at": now_iso(),
    }


def main() -> int:
    args = parse_args()
    validate_input_paths(args)
    qa_items = load_qa(args.qa, args.offset, args.limit)
    chunk_contents = load_chunks(args.chunks)
    configure_original_repo(args.original_root)
    patch_llm_timeout(args.llm_timeout_seconds)

    checkpoint_path = args.checkpoint or str(Path(args.output).with_suffix(".checkpoint.jsonl"))
    progress_path = args.progress or str(Path(args.output).with_suffix(".progress.json"))
    completed = load_checkpoint(checkpoint_path) if args.resume else {}
    total = len(qa_items)
    started_time = time.time()
    started_at = now_iso()

    results: list[dict[str, Any]] = []
    for qa in qa_items:
        existing = completed.get(qa["_id"])
        if not isinstance(existing, dict):
            continue
        item = existing.get("item")
        if isinstance(item, dict):
            results.append(sanitize_item(item, args.include_private_traces))

    if args.resume and len(results) == total:
        result = build_result(args, results, total, checkpoint_path, progress_path, started_at, started_time)
        write_result(args.output, result)
        write_progress(progress_path, {
            "schema_version": PROGRESS_SCHEMA_VERSION,
            "dataset": args.dataset,
            "mode": args.mode,
            "total": total,
            "completed": len(results),
            "answered": result["answered_count"],
            "failed": result["failed_count"],
            "correct": result["correct_count"],
            "accuracy": result["accuracy"],
            "updated_at": now_iso(),
        })
        print(json.dumps({
            "schema_version": PROGRESS_SCHEMA_VERSION,
            "dataset": args.dataset,
            "mode": args.mode,
            "completed": len(results),
            "total": total,
            "correct": result["correct_count"],
            "failed": result["failed_count"],
            "accuracy": result["accuracy"],
            "resumed_from_checkpoint": True,
        }, ensure_ascii=False), flush=True)
        return 0 if result["failed_count"] == 0 else 2

    from config import ConfigManager
    from models.retriever import agentic_decomposer as decomposer
    from models.retriever import enhanced_kt_retriever as retriever
    from utils.eval import Eval

    config = ConfigManager(args.config)
    config.override_config({
        "datasets": {
            args.dataset: {
                "corpus_path": "",
                "qa_path": args.qa,
                "schema_path": args.schema,
                "graph_output": args.graph,
            }
        },
        "triggers": {
            "constructor_trigger": False,
            "retrieve_trigger": True,
            "mode": args.mode,
        },
        "retrieval": {
            "cache_dir": args.cache_dir,
            "recall_paths": args.recall_paths,
            "top_k": args.top_k,
            "top_k_filter": args.top_k,
            "agent": {
                "max_steps": args.max_agent_steps,
            },
        },
    })

    graphq = decomposer.GraphQ(args.dataset, config=config)
    kt_retriever = retriever.KTRetriever(
        args.dataset,
        args.graph,
        recall_paths=args.recall_paths,
        schema_path=args.schema,
        top_k=args.top_k,
        mode=args.mode,
        config=config,
    )
    kt_retriever.chunk2id = chunk_contents
    kt_retriever.chunk_embedding_cache.clear()
    kt_retriever.chunk_faiss_index = None
    kt_retriever.chunk_id_to_index.clear()
    kt_retriever.index_to_chunk_id.clear()
    kt_retriever.chunk_embeddings_precomputed = False
    kt_retriever._precompute_chunk_embeddings()
    kt_retriever.build_indices()
    evaluator = Eval()

    correct = 0
    failed = 0
    answered = 0

    for item in results:
        if item.get("status") == "succeeded":
            answered += 1
            if item.get("correct"):
                correct += 1
        else:
            failed += 1

    completed_ids = {str(item.get("id")) for item in results}
    for ordinal, qa in enumerate(qa_items, 1):
        if STOP_REQUESTED:
            break
        item_id = qa["_id"]
        if args.resume and item_id in completed_ids:
            continue
        item_started = time.time()
        status = "succeeded"
        error = ""
        answer = ""
        judge = "0"
        detail: dict[str, Any] = {}
        try:
            if args.mode == "agent":
                detail = run_agent_question(graphq, kt_retriever, config, qa, args.schema, args.max_agent_steps)
            else:
                detail = run_noagent_question(graphq, kt_retriever, config, qa, args.schema)
            answer = str(detail.get("answer") or "")
            judge = normalize_judge(evaluator.eval(qa["_question"], qa["_answer"], answer))
            item_correct = judge == "1"
            answered += 1
            if item_correct:
                correct += 1
        except Exception as exc:
            status = "failed"
            error = str(exc)
            item_correct = False
            failed += 1

        item = {
            "schema_version": "paper-benchmark-item/v1",
            "id": item_id,
            "index": qa["_index"],
            "ordinal": ordinal,
            "question": qa["_question"],
            "gold_answer": qa["_answer"],
            "predicted_answer": answer,
            "judge": judge,
            "correct": item_correct,
            "status": status,
            "error": error,
            "mode": args.mode,
            "duration_seconds": time.time() - item_started,
            "retrieval": {
                "triples_count": detail.get("triples_count", 0),
                "chunk_count": detail.get("chunk_count", 0),
                "ircot_steps": detail.get("ircot_steps", 0),
            },
            "mapping_score": score_anonymized_mappings(qa["_answer"], answer),
        }
        if args.include_private_traces:
            item["detail"] = detail
        else:
            item["detail_summary"] = summarize_detail(detail)
        results.append(item)
        append_checkpoint(checkpoint_path, {
            "schema_version": CHECKPOINT_SCHEMA_VERSION,
            "id": item_id,
            "dataset": args.dataset,
            "status": status,
            "item": item,
            "time": now_iso(),
        })
        write_progress(progress_path, {
            "schema_version": PROGRESS_SCHEMA_VERSION,
            "dataset": args.dataset,
            "mode": args.mode,
            "total": total,
            "completed": len(results),
            "answered": answered,
            "failed": failed,
            "correct": correct,
            "accuracy": correct / total if total else 0.0,
            "updated_at": now_iso(),
        })
        print(json.dumps({
            "schema_version": PROGRESS_SCHEMA_VERSION,
            "dataset": args.dataset,
            "mode": args.mode,
            "completed": len(results),
            "total": total,
            "correct": correct,
            "failed": failed,
            "accuracy": correct / total if total else 0.0,
        }, ensure_ascii=False), flush=True)

    result = build_result(args, results, total, checkpoint_path, progress_path, started_at, started_time)
    write_result(args.output, result)
    return 0 if result["failed_count"] == 0 else 2


if __name__ == "__main__":
    raise SystemExit(main())
