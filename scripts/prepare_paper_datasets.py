#!/usr/bin/env python3
"""Download paper benchmark datasets into the original youtu-graphrag layout.

The Youtu-GraphRAG paper evaluates HotpotQA, 2WikiMultiHopQA, MuSiQue,
GraphRAG-Bench, and AnonyRAG. AnonyRAG has a dedicated helper because its
source files are Parquet. This script prepares the remaining JSON datasets and
also creates the combined GraphRAG-Bench layout expected by config/base_config.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.request
from pathlib import Path
from typing import Any


HF_RESOLVE = "https://huggingface.co/datasets/{repo}/resolve/main/{path}"

DOWNLOADS = [
    (
        "suki4060/GraphRAG-Benchmark",
        "Corpus/hotpotqa.json",
        "paper_raw/suki4060/Corpus/hotpotqa.json",
    ),
    (
        "suki4060/GraphRAG-Benchmark",
        "Questions/hotpotqa_questions.json",
        "paper_raw/suki4060/Questions/hotpotqa_questions.json",
    ),
    (
        "suki4060/GraphRAG-Benchmark",
        "Corpus/2wikimultihop.json",
        "paper_raw/suki4060/Corpus/2wikimultihop.json",
    ),
    (
        "suki4060/GraphRAG-Benchmark",
        "Questions/2wikimultihop_questions.json",
        "paper_raw/suki4060/Questions/2wikimultihop_questions.json",
    ),
    (
        "suki4060/GraphRAG-Benchmark",
        "Corpus/musique.json",
        "paper_raw/suki4060/Corpus/musique.json",
    ),
    (
        "suki4060/GraphRAG-Benchmark",
        "Questions/musique_questions.json",
        "paper_raw/suki4060/Questions/musique_questions.json",
    ),
    (
        "GraphRAG-Bench/GraphRAG-Bench",
        "Datasets/Corpus/medical.json",
        "paper_raw/graphrag-bench/Corpus/medical.json",
    ),
    (
        "GraphRAG-Bench/GraphRAG-Bench",
        "Datasets/Questions/medical_questions.json",
        "paper_raw/graphrag-bench/Questions/medical_questions.json",
    ),
    (
        "GraphRAG-Bench/GraphRAG-Bench",
        "Datasets/Corpus/novel.json",
        "paper_raw/graphrag-bench/Corpus/novel.json",
    ),
    (
        "GraphRAG-Bench/GraphRAG-Bench",
        "Datasets/Questions/novel_questions.json",
        "paper_raw/graphrag-bench/Questions/novel_questions.json",
    ),
]

COPY_TARGETS = [
    (
        "paper_raw/suki4060/Corpus/hotpotqa.json",
        "hotpotqa/hotpotqa_corpus.json",
    ),
    (
        "paper_raw/suki4060/Questions/hotpotqa_questions.json",
        "hotpotqa/hotpotqa.json",
    ),
    (
        "paper_raw/suki4060/Corpus/2wikimultihop.json",
        "2wiki/2wikimultihopqa_corpus.json",
    ),
    (
        "paper_raw/suki4060/Questions/2wikimultihop_questions.json",
        "2wiki/2wikimultihopqa.json",
    ),
    (
        "paper_raw/suki4060/Corpus/musique.json",
        "musique/musique_corpus.json",
    ),
    (
        "paper_raw/suki4060/Questions/musique_questions.json",
        "musique/musique.json",
    ),
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--artifact-root",
        type=Path,
        default=Path("../youtu-graphrag"),
        help="Path to the original youtu-graphrag checkout.",
    )
    parser.add_argument("--force", action="store_true", help="Re-download and rewrite outputs.")
    return parser.parse_args()


def download(url: str, output: Path, force: bool) -> None:
    if output.exists() and output.stat().st_size > 0 and not force:
        print(f"exists: {output}")
        return
    output.parent.mkdir(parents=True, exist_ok=True)
    tmp = output.with_suffix(output.suffix + ".tmp")
    last_error: Exception | None = None
    for attempt in range(1, 6):
        try:
            print(f"download: {url} -> {output}")
            req = urllib.request.Request(url, headers={"User-Agent": "youtu-rag-service"})
            with urllib.request.urlopen(req, timeout=180) as response, tmp.open("wb") as fh:
                while True:
                    chunk = response.read(1024 * 1024)
                    if not chunk:
                        break
                    fh.write(chunk)
            if tmp.stat().st_size <= 0:
                raise RuntimeError(f"downloaded empty file: {output}")
            tmp.replace(output)
            return
        except Exception as exc:  # noqa: BLE001 - user-facing retry wrapper
            last_error = exc
            if tmp.exists():
                tmp.unlink()
            if attempt < 5:
                time.sleep(2)
    raise RuntimeError(f"failed to download {url}: {last_error}")


def load_json(path: Path) -> Any:
    with path.open(encoding="utf-8") as fh:
        return json.load(fh)


def write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")


def copy_json(src: Path, dst: Path, force: bool) -> None:
    if dst.exists() and dst.stat().st_size > 0 and not force:
        print(f"exists: {dst}")
        return
    write_json(dst, load_json(src))
    print(f"prepared: {dst}")


def prepare_graphrag_bench(root: Path, force: bool) -> None:
    out_dir = root / "data" / "graphrag-bench-reformat"
    corpus_out = out_dir / "bench_corpus.json"
    qa_out = out_dir / "graphrag-bench.json"
    if corpus_out.exists() and qa_out.exists() and not force:
        print(f"exists: {corpus_out}")
        print(f"exists: {qa_out}")
        return

    corpus: list[dict[str, Any]] = []
    qa: list[dict[str, Any]] = []
    for domain in ("medical", "novel"):
        for row in load_json(root / "data" / "paper_raw" / "graphrag-bench" / "Corpus" / f"{domain}.json"):
            row = dict(row)
            row.setdefault("source_dataset", "graphrag-bench")
            row.setdefault("domain", domain)
            corpus.append(row)
        for row in load_json(
            root / "data" / "paper_raw" / "graphrag-bench" / "Questions" / f"{domain}_questions.json"
        ):
            row = dict(row)
            row.setdefault("source_dataset", "graphrag-bench")
            row.setdefault("domain", domain)
            qa.append(row)

    write_json(corpus_out, corpus)
    write_json(qa_out, qa)
    print(f"prepared: {corpus_out} rows={len(corpus)}")
    print(f"prepared: {qa_out} rows={len(qa)}")


def summarize(root: Path) -> None:
    datasets = [
        ("hotpot", "hotpotqa/hotpotqa_corpus.json", "hotpotqa/hotpotqa.json"),
        ("2wiki", "2wiki/2wikimultihopqa_corpus.json", "2wiki/2wikimultihopqa.json"),
        ("musique", "musique/musique_corpus.json", "musique/musique.json"),
        (
            "graphrag-bench",
            "graphrag-bench-reformat/bench_corpus.json",
            "graphrag-bench-reformat/graphrag-bench.json",
        ),
    ]
    print("\nsummary:")
    for name, corpus_rel, qa_rel in datasets:
        corpus_path = root / "data" / corpus_rel
        qa_path = root / "data" / qa_rel
        corpus = load_json(corpus_path)
        qa = load_json(qa_path)
        print(f"{name}: corpus={len(corpus)} qa={len(qa)} corpus_path=data/{corpus_rel} qa_path=data/{qa_rel}")


def main() -> int:
    args = parse_args()
    root = args.artifact_root.resolve()

    for repo, path, rel_out in DOWNLOADS:
        download(HF_RESOLVE.format(repo=repo, path=path), root / "data" / rel_out, args.force)

    for src_rel, dst_rel in COPY_TARGETS:
        copy_json(root / "data" / src_rel, root / "data" / dst_rel, args.force)

    prepare_graphrag_bench(root, args.force)
    summarize(root)
    return 0


if __name__ == "__main__":
    sys.exit(main())
