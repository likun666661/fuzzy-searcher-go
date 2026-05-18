#!/usr/bin/env python3
"""Download and prepare AnonyRAG for the original youtu-graphrag layout.

The source dataset lives on Hugging Face as Parquet files. The original
Youtu-RAG config expects JSON corpus/QA files under data/anony_chs and
data/anony_eng, plus schemas/anony_chs.json and schemas/anony_eng.json.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.request
from pathlib import Path
from typing import Any

try:
    import pandas as pd
except ImportError as exc:  # pragma: no cover - user-facing dependency error
    raise SystemExit(
        "prepare_anonyrag.py requires pandas and pyarrow. "
        "Run it with the youtu-graphrag Python environment."
    ) from exc


REPO_URL = "https://huggingface.co/datasets/Youtu-Graph/AnonyRAG/resolve/main"
FILES = [
    "README.md",
    "annoyrag_chs_qa.parquet",
    "annoyrag_chs_text_chunks.parquet",
    "annoyrag_eng_qa.parquet",
    "annoyrag_eng_text_chunks.parquet",
]

DATASETS = {
    "anony_chs": {
        "qa": "annoyrag_chs_qa.parquet",
        "texts": "annoyrag_chs_text_chunks.parquet",
    },
    "anony_eng": {
        "qa": "annoyrag_eng_qa.parquet",
        "texts": "annoyrag_eng_text_chunks.parquet",
    },
}

STARTER_SCHEMA = {
    "Nodes": [
        "person",
        "location",
        "organization",
        "event",
        "object",
        "concept",
        "time_period",
        "creative_work",
    ],
    "Relations": [
        "related_to",
        "located_in",
        "part_of",
        "belongs_to",
        "participates_in",
        "causes",
        "precedes",
        "follows",
        "helps",
        "opposes",
        "travels_to",
        "stays_at",
        "meets",
        "knows",
        "uses",
        "owns",
        "describes",
        "symbolizes",
        "has_role",
        "has_attribute",
    ],
    "Attributes": [
        "name",
        "description",
        "role",
        "identity",
        "location",
        "time",
        "status",
        "reason",
        "result",
        "relationship",
    ],
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--artifact-root",
        type=Path,
        default=Path("../youtu-graphrag"),
        help="Path to the original youtu-graphrag checkout.",
    )
    parser.add_argument("--force", action="store_true", help="Re-download files.")
    parser.add_argument(
        "--sample-size",
        type=int,
        default=20,
        help="Number of QA rows to write to final_qa_pairs.sampleN.json.",
    )
    parser.add_argument(
        "--corpus-sample-size",
        type=int,
        default=200,
        help="Number of corpus rows to write to final_chunk_corpus.sampleN.json.",
    )
    return parser.parse_args()


def download(url: str, output: Path, force: bool) -> None:
    if output.exists() and output.stat().st_size > 0 and not force:
        print(f"exists: {output}")
        return
    output.parent.mkdir(parents=True, exist_ok=True)
    last_error: Exception | None = None
    for attempt in range(1, 6):
        try:
            print(f"download: {url} -> {output}")
            request = urllib.request.Request(url, headers={"User-Agent": "youtu-rag-service"})
            with urllib.request.urlopen(request, timeout=120) as response:
                output.write_bytes(response.read())
            if output.stat().st_size <= 0:
                raise RuntimeError(f"downloaded empty file: {output}")
            return
        except Exception as exc:  # noqa: BLE001 - user-facing retry wrapper
            last_error = exc
            if attempt < 5:
                time.sleep(2)
    raise RuntimeError(f"failed to download {url}: {last_error}")


def text_value(value: Any) -> str:
    if value is None:
        return ""
    return str(value).strip()


def write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")


def convert_dataset(root: Path, raw_dir: Path, name: str, cfg: dict[str, str], args: argparse.Namespace) -> None:
    qa_df = pd.read_parquet(raw_dir / cfg["qa"])
    text_df = pd.read_parquet(raw_dir / cfg["texts"])

    qa_records = [
        {
            "question": text_value(row.get("question")),
            "answer": text_value(row.get("answer")),
            "relations": text_value(row.get("relations")),
            "entities": text_value(row.get("entities")),
            "query_type": text_value(row.get("query_type")),
        }
        for row in qa_df.to_dict(orient="records")
    ]
    corpus_records = [
        {
            "id": text_value(row.get("idx")),
            "title": text_value(row.get("title")),
            "text": text_value(row.get("chunk")),
        }
        for row in text_df.to_dict(orient="records")
    ]

    out_dir = root / "data" / name
    write_json(out_dir / "final_qa_pairs.json", qa_records)
    write_json(out_dir / f"final_qa_pairs.sample{args.sample_size}.json", qa_records[: args.sample_size])
    write_json(out_dir / "final_chunk_corpus.json", corpus_records)
    write_json(
        out_dir / f"final_chunk_corpus.sample{args.corpus_sample_size}.json",
        corpus_records[: args.corpus_sample_size],
    )

    schema_path = root / "schemas" / f"{name}.json"
    if not schema_path.exists() or args.force:
        write_json(schema_path, STARTER_SCHEMA)

    print(f"prepared {name}: qa={len(qa_records)} corpus={len(corpus_records)} schema={schema_path}")


def main() -> int:
    args = parse_args()
    root = args.artifact_root.resolve()
    raw_dir = root / "data" / "anonyrag" / "raw"
    for name in FILES:
        url = f"{REPO_URL}/{name}?download=true"
        download(url, raw_dir / name, args.force)

    for name, cfg in DATASETS.items():
        convert_dataset(root, raw_dir, name, cfg, args)

    print(f"AnonyRAG prepared under {root}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
