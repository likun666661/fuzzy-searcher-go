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
import urllib.parse
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
ANON_ID_RE = re.compile(r"\[?([A-Z][A-Z0-9_]*#[0-9]+)\]?")
MAPPING_SEP_RE = re.compile(r"^\s*(?:[:：=]|[-–—]+|->|=>|→)+\s*")


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


def post_json(url: str, payload: dict[str, Any], timeout: float) -> dict[str, Any]:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"content-type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"retrieve HTTP {exc.code}: {detail[:800]}") from exc
    if not isinstance(data, dict):
        raise RuntimeError(f"retrieve response is not an object: {type(data).__name__}")
    return data


def retrieve_context(question: str, args: argparse.Namespace, timeout: float) -> tuple[list[dict[str, str]], dict[str, Any]]:
    if not args.retrieve_url:
        contexts = select_context_from_args(question, args)
        return contexts, {
            "context_source": "corpus_keyword_overlap" if contexts else "none",
            "chunk_ids": [chunk["id"] for chunk in contexts],
            "triples_count": 0,
            "chunk_count": len(contexts),
        }

    endpoint = urllib.parse.urljoin(args.retrieve_url.rstrip("/") + "/", "v1/retrieve")
    payload: dict[str, Any] = {
        "dataset": args.dataset,
        "question": question,
        "top_k": args.top_k,
        "mode": args.retrieve_mode or args.mode,
    }
    if args.graph:
        payload["graph_path"] = args.graph
    if args.chunks:
        payload["chunks_path"] = args.chunks
    if args.sidecar_url:
        payload["sidecar_url"] = args.sidecar_url
    result = post_json(endpoint, payload, timeout)
    triples = [str(item) for item in result.get("triples") or [] if str(item).strip()]
    chunk_ids = [str(item) for item in result.get("chunk_ids") or [] if str(item).strip()]
    chunk_contents = [str(item) for item in result.get("chunk_contents") or [] if str(item).strip()]
    chunk_results = [str(item) for item in result.get("chunk_retrieval_results") or [] if str(item).strip()]
    contexts: list[dict[str, str]] = []
    if triples:
        contexts.append({"id": "retrieved_triples", "title": "Retrieved triples", "text": "\n".join(triples[: args.top_k])})
    for idx, content in enumerate(chunk_contents[: max(args.top_k, 1)]):
        chunk_id = chunk_ids[idx] if idx < len(chunk_ids) else f"chunk_{idx + 1}"
        contexts.append({"id": chunk_id, "title": "Retrieved chunk", "text": content})
    if not chunk_contents and chunk_results:
        for idx, content in enumerate(chunk_results[: max(args.top_k, 1)]):
            chunk_id = chunk_ids[idx] if idx < len(chunk_ids) else f"retrieval_result_{idx + 1}"
            contexts.append({"id": chunk_id, "title": "Chunk retrieval result", "text": content})
    strategies = ((result.get("debug") or {}).get("strategies") or []) if isinstance(result.get("debug"), dict) else []
    strategy_names = [str(item.get("name")) for item in strategies if isinstance(item, dict) and item.get("name")]
    return contexts, {
        "context_source": "service_retrieve",
        "retrieve_url": args.retrieve_url,
        "retrieve_mode": payload.get("mode"),
        "chunk_ids": chunk_ids,
        "triples_count": len(triples),
        "chunk_count": len(chunk_contents) or len(chunk_results),
        "debug_strategies": strategy_names,
    }


def select_context_from_args(question: str, args: argparse.Namespace) -> list[dict[str, str]]:
    return select_context(question, args._corpus, limit=min(max(args.top_k, 1), 6))


def anonymized_ids(text: str) -> list[str]:
    return sorted(set(ANON_ID_RE.findall(text or "")))


def is_anonymized_mapping_task(question: str) -> bool:
    if not anonymized_ids(question):
        return False
    return "匿名化" in question or "被匿名" in question or "匿名" in question


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
    user += f"Question:\n{question}\n\n"
    if is_anonymized_mapping_task(question):
        ids = ", ".join(anonymized_ids(question))
        user += (
            "This is an anonymized entity restoration task. Return only mapping lines, one per anonymized id.\n"
            f"Required ids: {ids}\n"
            "Format each line exactly as: ID——实体\n"
            "Do not include bullets, brackets, explanations, aliases, confidence, or extra prose.\n\n"
        )
    user += "Answer concisely."
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


def repair_mapping_answer(
    base_url: str,
    key: str,
    model: str,
    question: str,
    contexts: list[dict[str, str]],
    predicted: str,
    timeout: float,
) -> str:
    ids = anonymized_ids(question)
    context_text = "\n\n".join(
        f"[chunk {chunk['id']}] {chunk.get('title', '')}\n{chunk['text'][:1200]}" for chunk in contexts
    )
    user = (
        "Rewrite the previous answer into strict anonymized mapping format.\n"
        "Use the question and context only; do not invent ids that are not listed.\n"
        "Return every required id exactly once, one line per id.\n"
        "Output format: ID——实体\n"
        "No bullets, brackets, explanations, aliases, descriptions, or extra prose.\n\n"
        f"Required ids: {', '.join(ids)}\n\n"
    )
    if context_text:
        user += f"Context:\n{context_text}\n\n"
    user += f"Question:\n{question}\n\nPrevious answer:\n{predicted}\n"
    return chat_completion(
        base_url,
        key,
        model,
        [
            {"role": "system", "content": "You format anonymized entity restoration answers as strict mapping lines."},
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


def normalize_mapping_value(value: str) -> str:
    value = re.sub(r"^[\s:：=,，;；\-–—>→]+", "", value or "")
    value = value.strip()
    value = value.strip(" \t\r\n\"'`“”‘’[]【】{}<>《》:：,，;；。.!！?？-–—")
    value = re.sub(r"[（）()]", "", value)
    return re.sub(r"\s+", "", value)


def extract_anonymized_mappings(text: str) -> dict[str, str]:
    mappings: dict[str, str] = {}
    for line in (text or "").splitlines():
        matches = list(ANON_ID_RE.finditer(line))
        if not matches:
            continue
        for pos, match in enumerate(matches):
            key = match.group(1)
            start = match.end()
            end = matches[pos + 1].start() if pos + 1 < len(matches) else len(line)
            segment = line[start:end]
            segment = MAPPING_SEP_RE.sub("", segment)
            segment = re.split(r"(?:[;；]|,|，|、)\s*(?:\[?[A-Z][A-Z0-9_]*#[0-9]+\]?\s*(?:[:：=]|[-–—]+|->|=>|→))", segment, 1)[0]
            value = normalize_mapping_value(segment)
            if value:
                mappings.setdefault(key, value)
    return mappings


def mapping_match(expected: str, predicted: str) -> tuple[bool, str]:
    if not expected or not predicted:
        return False, "missing"
    if expected == predicted:
        return True, "exact"
    if expected in predicted or predicted in expected:
        return True, "contains"
    return False, "mismatch"


def score_anonymized_mappings(gold: str, predicted: str) -> dict[str, Any]:
    expected = extract_anonymized_mappings(gold)
    actual = extract_anonymized_mappings(predicted)
    matched_exact = 0
    matched_relaxed = 0
    by_key: dict[str, dict[str, Any]] = {}
    for key, expected_value in sorted(expected.items()):
        predicted_value = actual.get(key, "")
        matched, match_type = mapping_match(expected_value, predicted_value)
        if expected_value and predicted_value and expected_value == predicted_value:
            matched_exact += 1
        if matched:
            matched_relaxed += 1
        by_key[key] = {
            "expected": expected_value,
            "predicted": predicted_value,
            "matched": matched,
            "match_type": match_type,
        }
    missing_keys = [key for key in sorted(expected) if key not in actual]
    extra_keys = [key for key in sorted(actual) if key not in expected]
    expected_count = len(expected)
    predicted_count = len(actual)
    precision = matched_relaxed / predicted_count if predicted_count else (1.0 if expected_count == 0 else 0.0)
    recall = matched_relaxed / expected_count if expected_count else 1.0
    f1 = (2 * precision * recall / (precision + recall)) if (precision + recall) else 0.0
    exact_recall = matched_exact / expected_count if expected_count else 1.0
    return {
        "schema_version": "anonymized-mapping-score/v1",
        "applicable": expected_count > 0,
        "expected_count": expected_count,
        "predicted_count": predicted_count,
        "matched_count": matched_relaxed,
        "exact_matched_count": matched_exact,
        "precision": precision,
        "recall": recall,
        "f1": f1,
        "exact_recall": exact_recall,
        "missing_keys": missing_keys,
        "extra_keys": extra_keys,
        "by_key": by_key,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run a small Youtu-RAG benchmark worker.")
    parser.add_argument("--dataset", required=True)
    parser.add_argument("--qa", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--offset", type=int, default=0)
    parser.add_argument("--mode", default="noagent")
    parser.add_argument("--retrieve-url", default="")
    parser.add_argument("--retrieve-mode", default="")
    parser.add_argument("--sidecar-url", default="")
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
    contexts: list[dict[str, str]] = []
    predicted = ""
    original_predicted = ""
    judge = "0"
    error = ""
    repair_error = ""
    repaired = False
    retrieval_meta: dict[str, Any] = {}
    try:
        contexts, retrieval_meta = retrieve_context(question, args, timeout)
        rate_limiter.wait()
        predicted = answer_question(base_url, key, answer_model, question, contexts, timeout)
        original_predicted = predicted
        if is_anonymized_mapping_task(question):
            try:
                rate_limiter.wait()
                repaired_answer = repair_mapping_answer(base_url, key, answer_model, question, contexts, predicted, timeout)
                if extract_anonymized_mappings(repaired_answer):
                    predicted = repaired_answer
                    repaired = True
            except Exception as exc:
                repair_error = str(exc)
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
        "original_predicted_answer": original_predicted if repaired else "",
        "answer_repaired": repaired,
        "answer_repair_error": repair_error,
        "judge": judge,
        "correct": correct,
        "mapping_score": score_anonymized_mappings(gold, predicted),
        "latency_ms": int((time.time() - item_start) * 1000),
        "error": error,
        "context_chunk_ids": retrieval_meta.get("chunk_ids") or [chunk["id"] for chunk in contexts],
        "retrieval": retrieval_meta,
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
    args._corpus = corpus
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
    mapping_items = [item.get("mapping_score") or {} for item in items if (item.get("mapping_score") or {}).get("applicable")]
    mapping_expected_count = sum(int(score.get("expected_count") or 0) for score in mapping_items)
    mapping_predicted_count = sum(int(score.get("predicted_count") or 0) for score in mapping_items)
    mapping_matched_count = sum(int(score.get("matched_count") or 0) for score in mapping_items)
    mapping_exact_count = sum(int(score.get("exact_matched_count") or 0) for score in mapping_items)
    mapping_precision = mapping_matched_count / mapping_predicted_count if mapping_predicted_count else (1.0 if mapping_expected_count == 0 else 0.0)
    mapping_recall = mapping_matched_count / mapping_expected_count if mapping_expected_count else 1.0
    mapping_f1 = (2 * mapping_precision * mapping_recall / (mapping_precision + mapping_recall)) if (mapping_precision + mapping_recall) else 0.0
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
            "retrieve_url": args.retrieve_url,
            "retrieve_mode": args.retrieve_mode or args.mode,
            "sidecar_url": args.sidecar_url,
            "top_k": args.top_k,
            "concurrency": concurrency,
            "context_source": "service_retrieve" if args.retrieve_url else ("corpus_keyword_overlap" if corpus else "none"),
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
