#!/usr/bin/env python3
"""Offline regression check for the paper benchmark shard runner.

The fixture seeds a main checkpoint plus a couple of pre-existing shard
checkpoints, then runs the shard parent. Every child exits through checkpoint
resume, so this check never imports the original retrieval stack or calls an
LLM.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any


def write_json(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def write_checkpoint(path: Path, rows: list[dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("".join(json.dumps(row, ensure_ascii=False) + "\n" for row in rows), encoding="utf-8")


def item(qid: str, ordinal: int, correct: bool) -> dict[str, Any]:
    return {
        "schema_version": "paper-benchmark-item/v1",
        "id": qid,
        "index": ordinal - 1,
        "ordinal": ordinal,
        "question": f"question {ordinal}",
        "gold_answer": f"answer {ordinal}",
        "predicted_answer": f"answer {ordinal}" if correct else "wrong",
        "judge": "1" if correct else "0",
        "correct": correct,
        "status": "succeeded",
        "error": "",
        "mode": "agent",
        "duration_seconds": 0.01,
        "llm_call_count": 2,
        "llm_retry_count": 0,
        "llm_attempts": [],
        "retrieval": {"triples_count": 1, "chunk_count": 1, "ircot_steps": 1},
        "mapping_score": {
            "schema_version": "anonymized-mapping-score/v1",
            "applicable": True,
            "expected_count": 1,
            "predicted_count": 1,
            "matched_count": 1 if correct else 0,
            "exact_matched_count": 1 if correct else 0,
            "precision": 1.0 if correct else 0.0,
            "recall": 1.0 if correct else 0.0,
            "f1": 1.0 if correct else 0.0,
            "exact_recall": 1.0 if correct else 0.0,
        },
        "detail_summary": {"schema_version": "paper-benchmark-detail-summary/v1"},
    }


def checkpoint_row(qid: str, ordinal: int, correct: bool) -> dict[str, Any]:
    return {
        "schema_version": "paper-benchmark-checkpoint-item/v1",
        "id": qid,
        "dataset": "demo",
        "status": "succeeded",
        "item": item(qid, ordinal, correct),
        "time": "2026-05-26T00:00:00Z",
    }


def shard_path(path: Path, shard_index: int, shard_count: int) -> Path:
    return path.with_name(f"{path.stem}.shard-{shard_index:02d}-of-{shard_count:02d}{path.suffix}")


def load_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def main() -> int:
    repo = Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        qa = [
            {"id": f"q{index}", "question": f"question {index}", "answer": f"answer {index}"}
            for index in range(5)
        ]
        qa_path = root / "qa.json"
        graph_path = root / "graph.json"
        chunks_path = root / "chunks.txt"
        schema_path = root / "schema.json"
        output_path = root / "result.json"
        checkpoint_path = root / "result.checkpoint.jsonl"
        progress_path = root / "result.progress.json"
        write_json(qa_path, qa)
        write_json(graph_path, [])
        write_json(schema_path, {"Nodes": ["entity"], "Relations": ["related"], "Attributes": ["name"]})
        chunks_path.write_text("id: c1\tChunk: fixture chunk\n", encoding="utf-8")
        (root / "json_repair.py").write_text(
            "import json\n\n"
            "def load(f):\n"
            "    return json.load(f)\n",
            encoding="utf-8",
        )

        rows = [checkpoint_row(f"q{index}", index + 1, correct=index % 2 == 0) for index in range(5)]
        # The main checkpoint simulates an interrupted prior single-worker run.
        # Missing rows are preserved in already-existing shard checkpoints.
        write_checkpoint(checkpoint_path, [rows[0], rows[2], rows[4]])
        write_checkpoint(shard_path(checkpoint_path, 0, 3), [rows[1]])
        write_checkpoint(shard_path(checkpoint_path, 1, 3), [rows[3]])

        cmd = [
            sys.executable,
            str(repo / "scripts" / "paper_benchmark_shard_runner.py"),
            "--original-root",
            str(root),
            "--dataset",
            "demo",
            "--qa",
            str(qa_path),
            "--graph",
            str(graph_path),
            "--chunks",
            str(chunks_path),
            "--schema",
            str(schema_path),
            "--cache-dir",
            str(root / "cache"),
            "--output",
            str(output_path),
            "--checkpoint",
            str(checkpoint_path),
            "--progress",
            str(progress_path),
            "--mode",
            "agent",
            "--limit",
            "5",
            "--prompt-mode",
            "open",
            "--shard-count",
            "3",
            "--progress-poll-seconds",
            "1",
            "--resume",
        ]
        env = dict(os.environ)
        env["PYTHONPATH"] = str(root) + os.pathsep + env.get("PYTHONPATH", "")
        subprocess.run(cmd, cwd=repo, check=True, env=env)

        check_cmd = [
            sys.executable,
            str(repo / "scripts" / "check_paper_benchmark_result.py"),
            "--result",
            str(output_path),
            "--checkpoint",
            str(checkpoint_path),
            "--progress",
            str(progress_path),
            "--dataset",
            "demo",
            "--mode",
            "agent",
            "--limit",
            "5",
            "--prompt-mode",
            "open",
        ]
        subprocess.run(check_cmd, cwd=repo, check=True)

        result = load_json(output_path)
        if result["completed_count"] != 5 or result["parameters"].get("shard_count") != 3:
            raise SystemExit(f"unexpected merged result: {result}")
        progress = load_json(progress_path)
        if progress.get("completed") != 5 or len(progress.get("shards") or []) != 3:
            raise SystemExit(f"unexpected merged progress: {progress}")
        ids = []
        for index in range(3):
            shard_checkpoint = shard_path(checkpoint_path, index, 3)
            shard_ids = [
                json.loads(line)["id"]
                for line in shard_checkpoint.read_text(encoding="utf-8").splitlines()
                if line.strip()
            ]
            ids.extend(shard_ids)
        if sorted(ids) != [f"q{index}" for index in range(5)]:
            raise SystemExit(f"shard checkpoints did not partition ids: {ids}")

    print("paper benchmark shard runner check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
