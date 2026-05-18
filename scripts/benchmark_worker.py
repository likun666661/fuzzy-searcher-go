#!/usr/bin/env python3
"""Small benchmark worker for the Go Youtu-RAG service.

The worker intentionally keeps orchestration simple: Go owns job/workflow
records and artifact metadata; this script owns QA loading, optional lightweight
corpus context selection, LLM answer generation, LLM judging, and writing the
benchmark-result/v1 artifact.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import signal
import sys
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


SCHEMA_VERSION = "benchmark-result/v1"
DEFAULT_BASE_URL = "https://api.deepseek.com"
DEFAULT_MODEL = "deepseek-v4-pro"
STOP_REQUESTED = threading.Event()


def handle_stop(_signum: int, _frame: Any) -> None:
    STOP_REQUESTED.set()


signal.signal(signal.SIGTERM, handle_stop)
signal.signal(signal.SIGINT, handle_stop)


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def load_json(path: str) -> Any:
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def load_qa(path: str) -> list[dict[str, Any]]:
    data = load_json(path)
    if isinstance(data, dict):
        for key in ("qa_pairs", "questions", "items", "data"):
            if isinstance(data.get(key), list):
                data = data[key]
                break
    if not isinstance(data, list):
        raise SystemExit(f"benchmark_invalid_request: QA file must be a list: {path}")
    items: list[dict[str, Any]] = []
    for idx, row in enumerate(data):
        if not isinstance(row, dict):
            continue
        question = str(row.get("question") or row.get("query") or "").strip()
        answer = str(
            row.get("answer")
            or row.get("gold_answer")
            or row.get("gold")
            or row.get("reference_answer")
            or ""
        ).strip()
        if not question:
            continue
        item = dict(row)
        item["_id"] = str(row.get("id") or row.get("qid") or row.get("question_id") or f"qa_{idx + 1}")
        item["_question"] = question
        item["_answer"] = answer
        items.append(item)
    return items


def infer_corpus_path(qa_path: str) -> str:
    qa = Path(qa_path)
    candidates = [
        qa.with_name("final_chunk_corpus.json"),
        qa.with_name("chunk_corpus.json"),
        qa.with_name("corpus.json"),
    ]
    for candidate in candidates:
        if candidate.exists():
            return str(candidate)
    return ""


def load_corpus(path: str) -> list[dict[str, str]]:
    if not path or not os.path.exists(path):
        return []
    data = load_json(path)
    if isinstance(data, dict):
        iterable = data.values()
    elif isinstance(data, list):
        iterable = data
    else:
        return []
    chunks: list[dict[str, str]] = []
    for idx, row in enumerate(iterable):
        if isinstance(row, dict):
            text = str(row.get("text") or row.get("content") or row.get("chunk") or "").strip()
            title = str(row.get("title") or "").strip()
            chunk_id = str(row.get("id") or row.get("chunk_id") or idx)
        else:
            text = str(row).strip()
            title = ""
            chunk_id = str(idx)
        if text:
            chunks.append({"id": chunk_id, "title": title, "text": text})
    return chunks


TOKEN_RE = re.compile(r"[A-Za-z0-9_\-\[\]#]+|[\u4e00-\u9fff]{2,}")


def tokenize(text: str) -> set[str]:
    return {tok.lower() for tok in TOKEN_RE.findall(text) if len(tok.strip()) > 1}


def select_context(question: str, chunks: list[dict[str, str]], limit: int = 4) -> list[dict[str, str]]:
    if not chunks:
        return []
    query_tokens = tokenize(question)
    scored: list[tuple[int, dict[str, str]]] = []
    for chunk in chunks:
        haystack = f"{chunk.get('title', '')}\n{chunk.get('text', '')}"
        score = len(query_tokens.intersection(tokenize(haystack)))
        if score > 0:
            scored.append((score, chunk))
    scored.sort(key=lambda pair: (-pair[0], pair[1].get("id", "")))
    return [chunk for _, chunk in scored[:limit]]


def model_name(preferred: str) -> str:
    return preferred or os.getenv("LLM_MODEL") or DEFAULT_MODEL


def llm_base_url(preferred: str) -> str:
    return (preferred or os.getenv("LLM_BASE_URL") or DEFAULT_BASE_URL).rstrip("/")


def api_key() -> str:
    return os.getenv("LLM_API_KEY") or os.getenv("DEEPSEEK_API_KEY") or ""


def chat_completion(base_url: str, key: str, model: str, messages: list[dict[str, str]], timeout: float) -> str:
    if STOP_REQUESTED.is_set():
        raise RuntimeError("benchmark_canceled")
    req = urllib.request.Request(
        f"{base_url}/chat/completions",
        data=json.dumps(
            {
                "model": model,
                "messages": messages,
                "temperature": 0,
                "stream": False,
            }
        ).encode("utf-8"),
        headers={
            "authorization": f"Bearer {key}",
            "content-type": "application/json",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"LLM HTTP {exc.code}: {detail[:800]}") from exc
    choices = payload.get("choices") or []
    if not choices:
        raise RuntimeError(f"LLM response has no choices: {payload}")
    message = choices[0].get("message") or {}
    content = str(message.get("content") or "").strip()
    if not content:
        raise RuntimeError(f"LLM response has empty content: {payload}")
    return content


def answer_question(
    base_url: str,
    key: str,
    model: str,
    question: str,
    contexts: list[dict[str, str]],
    timeout: float,
) -> str:
    context_text = "\n\n".join(
        f"[chunk {chunk['id']}] {chunk.get('title', '')}\n{chunk['text'][:1800]}" for chunk in contexts
    )
    user = (
        "Answer the question using the provided context when it is useful. "
        "If the question asks for anonymized entity mappings, return the mappings directly.\n\n"
    )
    if context_text:
        user += f"Context:\n{context_text}\n\n"
    user += f"Question:\n{question}\n\nAnswer concisely."
    return chat_completion(
        base_url,
        key,
        model,
        [
            {"role": "system", "content": "You are a careful QA benchmark answerer."},
            {"role": "user", "content": user},
        ],
        timeout,
    )


def judge_answer(
    base_url: str,
    key: str,
    model: str,
    question: str,
    gold: str,
    predicted: str,
    timeout: float,
) -> str:
    prompt = f"""Decide whether the predicted answer is semantically correct.

Return exactly one character:
1 = correct enough
0 = incorrect

Question:
{question}

Gold answer:
{gold}

Predicted answer:
{predicted}
"""
    judged = chat_completion(
        base_url,
        key,
        model,
        [
            {"role": "system", "content": "You are a strict benchmark judge. Return only 1 or 0."},
            {"role": "user", "content": prompt},
        ],
        timeout,
    )
    match = re.search(r"[01]", judged)
    if not match:
        raise RuntimeError(f"benchmark_judge_invalid: {judged[:200]}")
    return match.group(0)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run a small Youtu-RAG benchmark worker.")
    parser.add_argument("--dataset", required=True)
    parser.add_argument("--qa", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--offset", type=int, default=0)
    parser.add_argument("--mode", default="noagent")
    parser.add_argument("--top-k", type=int, default=20)
    parser.add_argument("--answer-model", default="")
    parser.add_argument("--judge-model", default="")
    parser.add_argument("--llm-base-url", default="")
    parser.add_argument("--graph", default="")
    parser.add_argument("--chunks", default="")
    parser.add_argument("--corpus", default="")
    parser.add_argument("--progress", default="")
    parser.add_argument("--checkpoint", default="")
    parser.add_argument("--concurrency", type=int, default=1)
    parser.add_argument("--rate-limit-rpm", type=int, default=0)
    parser.add_argument("--checkpoint-every", type=int, default=1)
    parser.add_argument("--max-failures", type=int, default=0)
    parser.add_argument("--question-timeout", type=float, default=0)
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--cache-dir", default="")
    parser.add_argument("--schema", default="")
    parser.add_argument("--config", default="")
    return parser.parse_args()


def write_json_atomic(path: str, payload: dict[str, Any]) -> None:
    if not path:
        return
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_suffix(target.suffix + ".tmp")
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(payload, f, ensure_ascii=False, indent=2)
        f.write("\n")
    os.replace(tmp, target)


def append_jsonl(path: str, payload: dict[str, Any], lock: threading.Lock) -> None:
    if not path:
        return
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with lock:
        with open(target, "a", encoding="utf-8") as f:
            json.dump(payload, f, ensure_ascii=False)
            f.write("\n")


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
                item = json.loads(line)
            except json.JSONDecodeError as exc:
                raise SystemExit(f"benchmark_checkpoint_invalid: line {line_no}: {exc}") from exc
            if item.get("schema_version") != "benchmark-checkpoint-item/v1":
                raise SystemExit(f"benchmark_checkpoint_invalid: line {line_no}: unexpected schema")
            item_id = str(item.get("id") or "")
            if item_id:
                completed[item_id] = item
    return completed


class RateLimiter:
    def __init__(self, rpm: int) -> None:
        self.interval = 60.0 / rpm if rpm > 0 else 0.0
        self.next_at = 0.0
        self.lock = threading.Lock()

    def wait(self) -> None:
        if self.interval <= 0:
            return
        with self.lock:
            now = time.time()
            wait_for = self.next_at - now
            if wait_for > 0:
                time.sleep(wait_for)
                now = time.time()
            self.next_at = now + self.interval


def progress_payload(
    args: argparse.Namespace,
    total: int,
    completed: int,
    correct_count: int,
    failed_count: int,
    items: list[dict[str, Any]],
    started_at: str,
    status: str,
    running: int = 0,
    last_error: str = "",
    checkpoint_path: str = "",
) -> dict[str, Any]:
    succeeded_count = completed - failed_count
    return {
        "schema_version": "benchmark-progress/v1",
        "dataset": args.dataset,
        "qa_path": args.qa,
        "output_path": args.output,
        "status": status,
        "total": total,
        "completed": completed,
        "running": running,
        "succeeded": succeeded_count,
        "failed": failed_count,
        "correct": correct_count,
        "correct_count": correct_count,
        "failed_count": failed_count,
        "accuracy_so_far": (correct_count / succeeded_count) if succeeded_count else 0.0,
        "concurrency": max(args.concurrency, 1),
        "checkpoint_path": checkpoint_path,
        "started_at": started_at,
        "updated_at": now_iso(),
        "last_error": last_error,
        "items": items,
    }


def run_one_item(
    row: dict[str, Any],
    corpus: list[dict[str, str]],
    args: argparse.Namespace,
    base_url: str,
    key: str,
    answer_model: str,
    judge_model: str,
    timeout: float,
    rate_limiter: RateLimiter,
) -> dict[str, Any]:
    item_start = time.time()
    question = row["_question"]
    gold = row["_answer"]
    contexts = select_context(question, corpus, limit=min(max(args.top_k, 1), 6))
    predicted = ""
    judge = "0"
    error = ""
    try:
        rate_limiter.wait()
        predicted = answer_question(base_url, key, answer_model, question, contexts, timeout)
        rate_limiter.wait()
        judge = judge_answer(base_url, key, judge_model, question, gold, predicted, timeout) if gold else "0"
        correct = judge == "1"
    except Exception as exc:
        error = str(exc)
        correct = False
    return {
        "id": row["_id"],
        "question": question,
        "gold_answer": gold,
        "predicted_answer": predicted,
        "judge": judge,
        "correct": correct,
        "latency_ms": int((time.time() - item_start) * 1000),
        "error": error,
        "context_chunk_ids": [chunk["id"] for chunk in contexts],
        "finished_at": now_iso(),
    }


def main() -> int:
    args = parse_args()
    key = api_key()
    if not key:
        print("benchmark_llm_unconfigured: set LLM_API_KEY or DEEPSEEK_API_KEY", file=sys.stderr)
        return 2

    base_url = llm_base_url(args.llm_base_url)
    answer_model = model_name(args.answer_model)
    judge_model = model_name(args.judge_model)
    timeout = float(os.getenv("BENCHMARK_LLM_TIMEOUT_SECONDS", "60"))
    if args.question_timeout > 0:
        timeout = args.question_timeout

    qa_items = load_qa(args.qa)
    start = max(args.offset, 0)
    stop = len(qa_items) if args.limit <= 0 else min(len(qa_items), start + args.limit)
    selected = qa_items[start:stop]
    resumed_by_id = load_checkpoint(args.checkpoint) if args.resume else {}
    corpus_path = args.corpus or infer_corpus_path(args.qa)
    corpus = load_corpus(corpus_path)
    concurrency = max(args.concurrency, 1)
    checkpoint_every = max(args.checkpoint_every, 1)
    rate_limiter = RateLimiter(args.rate_limit_rpm)

    started_at = now_iso()
    started = time.time()
    items_by_index: dict[int, dict[str, Any]] = {}
    correct_count = 0
    failed_count = 0
    completed = 0
    lock = threading.Lock()
    checkpoint_lock = threading.Lock()
    for index, row in enumerate(selected):
        resumed = resumed_by_id.get(row["_id"])
        if not resumed:
            continue
        item = dict(resumed)
        item.setdefault("question", row["_question"])
        item.setdefault("gold_answer", row["_answer"])
        item.setdefault("error", "")
        items_by_index[index] = item
        completed += 1
        if item.get("correct") is True:
            correct_count += 1
        if item.get("error"):
            failed_count += 1
    write_json_atomic(
        args.progress,
        progress_payload(
            args,
            len(selected),
            completed,
            correct_count,
            failed_count,
            [items_by_index[i] for i in sorted(items_by_index)],
            started_at,
            "running",
            running=min(concurrency, max(len(selected) - completed, 0)),
            checkpoint_path=args.checkpoint,
        ),
    )

    def record_done(index: int, item: dict[str, Any]) -> None:
        nonlocal completed, correct_count, failed_count
        with lock:
            items_by_index[index] = item
            completed += 1
            if item.get("correct") is True:
                correct_count += 1
            if item.get("error"):
                failed_count += 1
            ordered = [items_by_index[i] for i in sorted(items_by_index)]
            checkpoint_item = dict(item)
            checkpoint_item["schema_version"] = "benchmark-checkpoint-item/v1"
            checkpoint_item["index"] = index
            append_jsonl(args.checkpoint, checkpoint_item, checkpoint_lock)
            if completed % checkpoint_every == 0 or completed == len(selected):
                write_json_atomic(
                    args.progress,
                    progress_payload(
                        args,
                        len(selected),
                        completed,
                        correct_count,
                        failed_count,
                        ordered,
                        started_at,
                        "running" if completed < len(selected) else "succeeded",
                        running=max(min(concurrency, len(selected) - completed), 0),
                        last_error=str(item.get("error") or ""),
                        checkpoint_path=args.checkpoint,
                    ),
                )
            if args.max_failures > 0 and failed_count > args.max_failures:
                STOP_REQUESTED.set()

    with ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = {
            executor.submit(run_one_item, row, corpus, args, base_url, key, answer_model, judge_model, timeout, rate_limiter): index
            for index, row in enumerate(selected)
            if index not in items_by_index
        }
        for future in as_completed(futures):
            if STOP_REQUESTED.is_set():
                break
            index = futures[future]
            try:
                item = future.result()
            except Exception as exc:
                row = selected[index]
                item = {
                    "id": row["_id"],
                    "question": row["_question"],
                    "gold_answer": row["_answer"],
                    "predicted_answer": "",
                    "judge": "0",
                    "correct": False,
                    "latency_ms": 0,
                    "error": str(exc),
                    "context_chunk_ids": [],
                    "finished_at": now_iso(),
                }
            record_done(index, item)

    if STOP_REQUESTED.is_set() and args.max_failures > 0 and failed_count > args.max_failures:
        write_json_atomic(
            args.progress,
            progress_payload(
                args,
                len(selected),
                completed,
                correct_count,
                failed_count,
                [items_by_index[i] for i in sorted(items_by_index)],
                started_at,
                "failed",
                last_error="benchmark_failure_budget_exceeded",
                checkpoint_path=args.checkpoint,
            ),
        )
        print("benchmark_failure_budget_exceeded", file=sys.stderr)
        return 3

    duration_ms = int((time.time() - started) * 1000)
    question_count = len(selected)
    items = [items_by_index[i] for i in sorted(items_by_index)]
    result = {
        "schema_version": SCHEMA_VERSION,
        "dataset": args.dataset,
        "qa_path": args.qa,
        "corpus_path": corpus_path,
        "question_count": question_count,
        "answered_count": question_count - failed_count,
        "correct_count": correct_count,
        "failed_count": failed_count,
        "accuracy": (correct_count / question_count) if question_count else 0.0,
        "started_at": started_at,
        "finished_at": now_iso(),
        "duration_ms": duration_ms,
        "model": {
            "answer_model": answer_model,
            "judge_model": judge_model,
            "llm_base_url": base_url,
        },
        "retrieval": {
            "mode": args.mode,
            "top_k": args.top_k,
            "concurrency": concurrency,
            "context_source": "corpus_keyword_overlap" if corpus else "none",
            "graph_path": args.graph,
            "chunks_path": args.chunks,
            "cache_dir": args.cache_dir,
            "schema_path": args.schema,
            "config_path": args.config,
        },
        "items": items,
    }
    Path(args.output).parent.mkdir(parents=True, exist_ok=True)
    with open(args.output, "w", encoding="utf-8") as f:
        json.dump(result, f, ensure_ascii=False, indent=2)
        f.write("\n")
    write_json_atomic(
        args.progress,
        progress_payload(
            args,
            question_count,
            question_count,
            correct_count,
            failed_count,
            items,
            started_at,
            "succeeded",
            checkpoint_path=args.checkpoint,
        ),
    )
    print(json.dumps({"schema_version": "benchmark-worker-summary/v1", "output_path": args.output, "question_count": question_count, "accuracy": result["accuracy"]}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
