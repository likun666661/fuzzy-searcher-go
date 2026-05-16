#!/usr/bin/env python3
"""
Phase 1 Oracle Export Script — Atomic, versioned, behavior-accurate.

All artifacts (ONNX model, tokenizer, golden data) are exported together
from the same model instance. No skip-if-exists: every run is a clean slate.

Usage:
    source .venv/bin/activate
    python scripts/export_phase1_oracle.py
"""

import hashlib
import json
import shutil
import sys
import time
from pathlib import Path

import numpy as np

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------
SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_DIR = SCRIPT_DIR.parent
YOUTU_DIR = PROJECT_DIR.parent / "youtu-graphrag"
GRAPH_PATH = YOUTU_DIR / "output" / "graphs" / "demo_new.json"

TESTDATA_DIR = PROJECT_DIR / "testdata"
MODEL_DIR = PROJECT_DIR / "models" / "all-MiniLM-L6-v2"
MODEL_NAME = "sentence-transformers/all-MiniLM-L6-v2"
ONNX_OPSET = 18


def validate():
    if not GRAPH_PATH.exists():
        print(f"ERROR: Graph file not found at {GRAPH_PATH}")
        sys.exit(1)
    print(f"✅ Found graph: {GRAPH_PATH} ({GRAPH_PATH.stat().st_size} bytes)")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fingerprint_artifacts(paths: list[Path]) -> tuple[str, dict[str, str]]:
    """Return a bundle hash plus per-file hashes for exported artifacts."""
    digest = hashlib.sha256()
    per_file = {}

    for path in sorted(paths, key=lambda p: p.name):
        file_hash = sha256_file(path)
        per_file[path.name] = file_hash[:16]
        digest.update(path.name.encode("utf-8"))
        digest.update(b"\0")
        digest.update(file_hash.encode("ascii"))
        digest.update(b"\0")

    return digest.hexdigest()[:16], per_file


# ===================================================================
# 1. ONNX Model + Tokenizer (always re-export, no skip)
# ===================================================================
def export_onnx_model():
    from sentence_transformers import SentenceTransformer
    from transformers import AutoTokenizer
    import torch

    print("\n📦 Exporting ONNX model (clean)...")

    # Wipe and recreate model dir for atomic export
    if MODEL_DIR.exists():
        shutil.rmtree(MODEL_DIR)
    MODEL_DIR.mkdir(parents=True)

    st_model = SentenceTransformer(MODEL_NAME)
    tokenizer = AutoTokenizer.from_pretrained(MODEL_NAME)

    # Save tokenizer
    tokenizer.save_pretrained(str(MODEL_DIR))
    print(f"  ✅ Tokenizer saved")

    # Export ONNX — force CPU to avoid MPS conflicts
    onnx_path = MODEL_DIR / "model.onnx"
    hf_model = st_model[0].auto_model.cpu()
    dummy = tokenizer("Hello", return_tensors="pt", padding="max_length",
                       max_length=128, truncation=True)
    dummy = {k: v.cpu() for k, v in dummy.items()}
    hf_model.eval()
    with torch.no_grad():
        torch.onnx.export(
            hf_model,
            (dummy["input_ids"], dummy["attention_mask"], dummy["token_type_ids"]),
            str(onnx_path),
            input_names=["input_ids", "attention_mask", "token_type_ids"],
            output_names=["last_hidden_state"],
            dynamic_axes={
                "input_ids": {0: "batch", 1: "seq"},
                "attention_mask": {0: "batch", 1: "seq"},
                "token_type_ids": {0: "batch", 1: "seq"},
                "last_hidden_state": {0: "batch", 1: "seq"},
            },
            opset_version=ONNX_OPSET,
        )

    artifact_paths = [
        MODEL_DIR / "model.onnx",
        MODEL_DIR / "model.onnx.data",
        MODEL_DIR / "tokenizer.json",
        MODEL_DIR / "tokenizer_config.json",
    ]
    artifact_paths = [path for path in artifact_paths if path.exists()]
    bundle_hash, artifact_hashes = fingerprint_artifacts(artifact_paths)

    print(f"  ✅ ONNX bundle fingerprint={bundle_hash}")
    for path in artifact_paths:
        print(
            f"     - {path.name}: {path.stat().st_size} bytes, "
            f"sha256={artifact_hashes[path.name]}"
        )

    return st_model, tokenizer, bundle_hash, artifact_hashes


# ===================================================================
# 2. Load graph (replicating graph_processor.load_graph_from_json)
# ===================================================================
def _norm_name(name):
    if isinstance(name, list):
        return ", ".join(str(x) for x in name)
    return str(name) if not isinstance(name, str) else name


def load_graph():
    import networkx as nx

    print("\n📊 Loading graph...")
    with open(GRAPH_PATH, "r", encoding="utf-8") as f:
        rels = json.load(f)

    graph = nx.MultiDiGraph()
    mapping = {}
    counter = 0

    for rel in rels:
        for side in ("start_node", "end_node"):
            nd = rel[side]
            name = _norm_name(nd["properties"].get("name", ""))
            key = (nd["label"], name)
            if key not in mapping:
                nid = f"{nd['label']}_{counter}"
                mapping[key] = nid
                counter += 1
                level_map = {"attribute": 1, "entity": 2, "keyword": 3, "community": 4}
                graph.add_node(nid, label=nd["label"], properties=nd["properties"],
                               level=level_map.get(nd["label"], 2))

        sid = mapping[(rel["start_node"]["label"],
                        _norm_name(rel["start_node"]["properties"].get("name", "")))]
        eid = mapping[(rel["end_node"]["label"],
                        _norm_name(rel["end_node"]["properties"].get("name", "")))]
        graph.add_edge(sid, eid, relation=rel["relation"])

    # ---------------------------------------------------------------
    # Inject synthetic edge-case nodes for coverage (P2 fix)
    # ---------------------------------------------------------------
    synthetic = [
        {"id": "synth_list_name", "label": "entity", "properties": {
            "name": ["Guangzhou", "Evergrande", "FC"],
            "description": "Chinese football club listnodecoverage",
            "schema_type": "organization"}},
        {"id": "synth_chinese", "label": "entity", "properties": {
            "name": "腾讯", "description": "中国科技公司 深圳总部",
            "schema_type": "organization"}},
        {"id": "synth_empty_desc", "label": "entity", "properties": {
            "name": "Unknown Player", "schema_type": "person"}},
        {"id": "synth_numeric_name", "label": "attribute", "properties": {
            "name": 12345, "description": None}},
        {"id": "synth_unicode", "label": "entity", "properties": {
            "name": "Ángel Di María", "description": "Argentine footballer with diacritics",
            "schema_type": "person"}},
    ]
    for s in synthetic:
        graph.add_node(s["id"], label=s["label"], properties=s["properties"],
                       level={"attribute": 1, "entity": 2}.get(s["label"], 2))

    print(f"  ✅ Graph: {graph.number_of_nodes()} nodes, {graph.number_of_edges()} edges")
    print(f"     (including {len(synthetic)} synthetic edge-case nodes)")
    return graph


# ===================================================================
# 3. Node text (exact replica of DualFAISSRetriever._get_node_text)
# ===================================================================
def get_node_text(graph, node: str) -> str:
    data = graph.nodes[node]
    if "properties" in data and isinstance(data["properties"], dict):
        name = data["properties"].get("name") or "none"
        description = data["properties"].get("description") or "none"
        name = str(name).strip()
        description = str(description).strip()
    else:
        name = data.get("name") or "none"
        description = data.get("description") or "none"
        name = str(name).strip()
        description = str(description).strip()

    if isinstance(name, list):
        name = ", ".join(str(x) for x in name)
    elif not isinstance(name, str):
        name = str(name)

    if isinstance(description, list):
        description = ", ".join(str(x) for x in description)
    elif not isinstance(description, str):
        description = str(description)

    return f"{name},{description}".strip()


def export_node_text_cases(graph):
    print("\n📝 Exporting node text cases...")
    cases = {nid: get_node_text(graph, nid) for nid in graph.nodes()}
    out = TESTDATA_DIR / "node_text_cases.json"
    with open(out, "w", encoding="utf-8") as f:
        json.dump(cases, f, ensure_ascii=False, indent=2)
    print(f"  ✅ {len(cases)} cases -> {out}")
    return cases


# ===================================================================
# 4. Keyword index (exact replica of _build_node_text_index + search)
# ===================================================================
def build_keyword_index(node_texts: dict) -> dict:
    """Replicate enhanced_kt_retriever._build_node_text_index:
    - lowercase
    - split on whitespace
    - skip words with len <= 2
    """
    index = {}
    for nid, text in node_texts.items():
        for word in set(text.lower().split()):
            if len(word) > 2:  # P1 fix: match real behavior
                if word not in index:
                    index[word] = set()
                index[word].add(nid)
    return index


def keyword_search(index: dict, query: str) -> list:
    """Replicate enhanced_kt_retriever._keyword_based_node_search:
    - max 50 nodes per keyword
    - total cap at 200
    """
    keywords = [w for w in query.lower().split() if len(w) > 2]
    result = set()
    max_per_kw = 50

    for kw in keywords:
        if kw in index:
            nodes = index[kw]
            if len(nodes) > max_per_kw:
                nodes = set(list(nodes)[:max_per_kw])
            result.update(nodes)
        if len(result) > 200:
            break

    return sorted(result)  # sorted for stable ordering


def export_keyword_cases(node_texts: dict):
    print("\n🔑 Exporting keyword cases...")
    inv = build_keyword_index(node_texts)

    queries = [
        "Barcelona football",
        "Messi academy",
        "Real Madrid champions",
        "Copa del Rey",
        "Maradona transfer Napoli",
        "Champions League",
        "football club manager coach",
        "season trophies",
        "深圳总部",          # Chinese keyword path under current tokenizer behavior
        "listnodecoverage",  # list-name node presence via unique description token
        "Unknown Player",       # empty description
        "Argentine footballer",  # unicode diacritics
    ]

    # Also export the raw inverted index terms for debugging
    cases = []
    for q in queries:
        nodes = keyword_search(inv, q)
        terms_used = [w for w in q.lower().split() if len(w) > 2]
        cases.append({
            "query": q,
            "terms_used": terms_used,
            "expected_node_ids": nodes,
        })

    out = TESTDATA_DIR / "keyword_cases.json"
    with open(out, "w", encoding="utf-8") as f:
        json.dump(cases, f, ensure_ascii=False, indent=2)
    print(f"  ✅ {len(cases)} cases -> {out}")


# ===================================================================
# 5. Embedding cases
# ===================================================================
def export_embedding_cases(st_model, node_texts: dict):
    print("\n🧮 Exporting embedding cases...")

    texts = []
    seen = set()

    # Diverse node texts (not just first 20)
    for nid, t in node_texts.items():
        if t not in seen and t != "none,none" and len(t) > 3:
            texts.append(t)
            seen.add(t)

    # Explicit edge-case texts
    edge_cases = [
        "Who is Messi?",
        "FC Barcelona history",
        "Diego Maradona transfer",
        "Champions League semi final",
        "腾讯 GraphRAG 知识图谱",   # Chinese
        "Ángel Di María",            # diacritics
        "",                          # empty string
        "a",                         # single char
        "Guangzhou, Evergrande, FC,Chinese football club",  # list-name format
        "12345,none",                # numeric name
    ]
    for t in edge_cases:
        if t not in seen:
            texts.append(t)
            seen.add(t)

    embeddings = st_model.encode(texts, normalize_embeddings=True)

    cases = [{"text": t, "embedding": e.tolist()} for t, e in zip(texts, embeddings)]

    out = TESTDATA_DIR / "embedding_cases.json"
    with open(out, "w", encoding="utf-8") as f:
        json.dump(cases, f, ensure_ascii=False)
    print(f"  ✅ {len(cases)} cases -> {out}")


# ===================================================================
# 6. Semantic search cases
# ===================================================================
def export_semantic_search_cases(st_model, node_texts: dict):
    print("\n🔍 Exporting semantic search cases...")

    node_ids = sorted(node_texts.keys())
    texts = [node_texts[nid] for nid in node_ids]
    node_embs = st_model.encode(texts, normalize_embeddings=True)

    queries = [
        "Who is Lionel Messi?",
        "FC Barcelona football club",
        "Copa del Rey tournament",
        "Diego Maradona transfer fee",
        "Champions League knockout",
        "La Liga winners",
        "Barcelona manager coach",
        "Ronaldinho fitness",
        "中国科技公司",           # Chinese semantic
        "Argentine footballer diacritics",  # unicode
    ]

    top_k = 10
    cases = []
    for q in queries:
        q_emb = st_model.encode([q], normalize_embeddings=True)[0]
        scores = np.dot(node_embs, q_emb)
        scored = list(zip(node_ids, scores.tolist()))
        # Stable sort: score desc, node_id asc
        scored.sort(key=lambda x: (-x[1], x[0]))
        cases.append({
            "query": q,
            "top_k": top_k,
            "results": [{"node_id": nid, "score": round(s, 6)}
                         for nid, s in scored[:top_k]],
        })

    out = TESTDATA_DIR / "semantic_search_cases.json"
    with open(out, "w", encoding="utf-8") as f:
        json.dump(cases, f, ensure_ascii=False, indent=2)
    print(f"  ✅ {len(cases)} cases -> {out}")


# ===================================================================
# 7. Graph snapshot + version manifest
# ===================================================================
def export_graph_snapshot_and_write_manifest(
    graph,
    bundle_hash: str,
    artifact_hashes: dict[str, str],
):
    print("\n📋 Writing augmented graph snapshot + manifest...")
    dst = TESTDATA_DIR / "demo_graph_snapshot.json"

    snapshot = {
        "format": "phase1_graph_snapshot_v1",
        "source_graph": str(GRAPH_PATH),
        "includes_synthetic_nodes": True,
        "nodes": [],
        "edges": [],
    }

    for node_id, data in sorted(graph.nodes(data=True), key=lambda item: item[0]):
        snapshot["nodes"].append({
            "id": node_id,
            "label": data.get("label"),
            "level": data.get("level"),
            "properties": data.get("properties", {}),
        })

    edge_rows = []
    for source, target, edge_key, data in graph.edges(keys=True, data=True):
        edge_rows.append((source, target, edge_key, data.get("relation", "")))
    edge_rows.sort()

    for source, target, edge_key, relation in edge_rows:
        snapshot["edges"].append({
            "source": source,
            "target": target,
            "key": edge_key,
            "relation": relation,
        })

    with open(dst, "w", encoding="utf-8") as f:
        json.dump(snapshot, f, ensure_ascii=False, indent=2)

    import sentence_transformers, torch
    manifest = {
        "exported_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "model_name": MODEL_NAME,
        "artifact_bundle_sha256_prefix": bundle_hash,
        "artifact_hashes": artifact_hashes,
        "onnx_opset": ONNX_OPSET,
        "sentence_transformers_version": sentence_transformers.__version__,
        "torch_version": torch.__version__,
        "graph_source": str(GRAPH_PATH),
        "snapshot_format": "phase1_graph_snapshot_v1",
        "note": (
            "All artifacts exported atomically from the same model instance. "
            "Snapshot is the augmented Phase 1 graph and includes synthetic edge-case nodes."
        ),
    }
    with open(TESTDATA_DIR / "manifest.json", "w", encoding="utf-8") as f:
        json.dump(manifest, f, ensure_ascii=False, indent=2)
    print(f"  ✅ Snapshot -> {dst}")
    print(f"  ✅ manifest.json written")


# ===================================================================
# Main
# ===================================================================
def main():
    print("=" * 60)
    print("Phase 1 Oracle Export (atomic)")
    print("=" * 60)

    validate()

    # Clean slate
    if TESTDATA_DIR.exists():
        shutil.rmtree(TESTDATA_DIR)
    TESTDATA_DIR.mkdir(parents=True)

    st_model, tokenizer, bundle_hash, artifact_hashes = export_onnx_model()
    graph = load_graph()
    export_graph_snapshot_and_write_manifest(graph, bundle_hash, artifact_hashes)
    node_texts = export_node_text_cases(graph)
    export_keyword_cases(node_texts)
    export_embedding_cases(st_model, node_texts)
    export_semantic_search_cases(st_model, node_texts)

    print("\n" + "=" * 60)
    print("✅ Done! All artifacts from same model instance.")
    print(f"   Model:    {MODEL_DIR}")
    print(f"   Testdata: {TESTDATA_DIR}")
    print(f"   Bundle hash: {bundle_hash}")
    print("=" * 60)


if __name__ == "__main__":
    main()
