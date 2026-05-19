package buildgraph_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/likun666661/youtu-rag-service/internal/jobs"
	"github.com/likun666661/youtu-rag-service/internal/workers/buildgraph"
)

func TestRunExecutesPythonWorker(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	graphPath := filepath.Join(dir, "out", "demo_new.json")
	chunksPath := filepath.Join(dir, "out", "demo.txt")
	walPath := filepath.Join(dir, "out", "demo.wal.jsonl")
	cacheDir := filepath.Join(dir, "cache", "demo")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import os
import sys
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
cache = sys.argv[sys.argv.index("--cache-dir") + 1]
wal = sys.argv[sys.argv.index("--wal") + 1]
assert "--resume" in sys.argv
assert sys.argv[sys.argv.index("--max-workers") + 1] == "4"
assert sys.argv[sys.argv.index("--runner-count") + 1] == "3"
assert sys.argv[sys.argv.index("--llm-rate-limit-rpm") + 1] == "90"
assert sys.argv[sys.argv.index("--llm-rate-limit-file") + 1].endswith("llm.limit")
assert "--skip-communities" in sys.argv
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
os.makedirs(os.path.dirname(wal), exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
with open(wal, "w", encoding="utf-8") as f:
    f.write("{}\n")
print(json.dumps({
    "schema_version": "build-graph-result/v1",
    "dataset": "demo",
    "graph_output_path": graph,
    "chunks_output_path": chunks,
    "wal_path": wal,
    "total_chunks": 2,
    "succeeded_chunks": 2,
    "skipped_chunks": 2,
    "runner_count": 3,
    "llm_rate_limit_rpm": 90,
    "skip_communities": True,
}))
`)

	result, err := buildgraph.Run(context.Background(), buildgraph.Config{
		PythonBin:  "python3",
		ScriptPath: script,
		WorkingDir: dir,
	}, jobs.BuildGraphSpec{
		Dataset:          "demo",
		CorpusPath:       filepath.Join(dir, "corpus.json"),
		SchemaPath:       filepath.Join(dir, "schema.json"),
		GraphOutputPath:  graphPath,
		ChunksOutputPath: chunksPath,
		WALPath:          walPath,
		Resume:           true,
		MaxWorkers:       4,
		RunnerCount:      3,
		LLMRateLimitRPM:  90,
		LLMRateLimitFile: filepath.Join(dir, "llm.limit"),
		SkipCommunities:  true,
		CacheDir:         cacheDir,
		ConfigPath:       "config/base_config.yaml",
		Mode:             "noagent",
	})
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if result.SchemaVersion != "build-graph-result/v1" || result.GraphOutputPath != graphPath || result.ChunksOutputPath != chunksPath {
		t.Fatalf("result = %#v", result)
	}
	if result.WALPath != walPath || result.TotalChunks != 2 || result.SucceededChunks != 2 || result.SkippedChunks != 2 ||
		result.RunnerCount != 3 || result.LLMRateLimitRPM != 90 || !result.SkipCommunities {
		t.Fatalf("structured result not merged: %#v", result)
	}
	if _, err := os.Stat(graphPath); err != nil {
		t.Fatalf("graph output missing: %v", err)
	}
	if _, err := os.Stat(chunksPath); err != nil {
		t.Fatalf("chunks output missing: %v", err)
	}
}

func TestRunReportsWorkerFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
import sys
print("graph runtime failed", file=sys.stderr)
raise SystemExit(9)
`)

	_, err := buildgraph.Run(context.Background(), buildgraph.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.BuildGraphSpec{
		Dataset:          "demo",
		CorpusPath:       filepath.Join(dir, "corpus.json"),
		GraphOutputPath:  filepath.Join(dir, "graph.json"),
		ChunksOutputPath: filepath.Join(dir, "chunks.txt"),
	})
	if err == nil || !strings.Contains(err.Error(), "graph runtime failed") {
		t.Fatalf("failure err = %v", err)
	}
}

func TestRunRequiresGraphAndChunksOutputs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
print("ok but no files")
`)

	_, err := buildgraph.Run(context.Background(), buildgraph.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.BuildGraphSpec{
		Dataset:          "demo",
		CorpusPath:       filepath.Join(dir, "corpus.json"),
		GraphOutputPath:  filepath.Join(dir, "graph.json"),
		ChunksOutputPath: filepath.Join(dir, "chunks.txt"),
	})
	if err == nil || !strings.Contains(err.Error(), "build graph output missing") {
		t.Fatalf("missing output err = %v", err)
	}
}

func TestRunRejectsInvalidGraphJSON(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	graphPath := filepath.Join(dir, "graph.json")
	chunksPath := filepath.Join(dir, "chunks.txt")
	writeExecutable(t, script, `#!/usr/bin/env python3
import os
import sys
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
os.makedirs(os.path.dirname(graph) or ".", exist_ok=True)
os.makedirs(os.path.dirname(chunks) or ".", exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    f.write("{not-json")
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
`)

	_, err := buildgraph.Run(context.Background(), buildgraph.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.BuildGraphSpec{
		Dataset:          "demo",
		CorpusPath:       filepath.Join(dir, "corpus.json"),
		GraphOutputPath:  graphPath,
		ChunksOutputPath: chunksPath,
	})
	if err == nil || !strings.Contains(err.Error(), "parse graph output") {
		t.Fatalf("invalid graph err = %v", err)
	}
}

func TestRunMultiRunnerWorkerWritesShardWALAndResumeDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	script := realBuildGraphWorkerPath(t)
	prepareFakeYoutuGraphRAG(t, dir)
	corpusPath := filepath.Join(dir, "corpus.json")
	schemaPath := filepath.Join(dir, "schema.json")
	graphPath := filepath.Join(dir, "out", "demo_new.json")
	chunksPath := filepath.Join(dir, "out", "demo.txt")
	walPath := filepath.Join(dir, "out", "demo.wal.jsonl")
	limiterPath := filepath.Join(dir, "out", "demo.llm_rate_limit")
	mustWrite(t, corpusPath, `[
{"title":"one","text":"Alpha"},
{"title":"two","text":"Beta"},
{"title":"three","text":"Gamma"},
{"title":"four","text":"Delta"}
]`)
	mustWrite(t, schemaPath, `{"Nodes":["entity"],"Relations":["related"],"Attributes":["name"]}`)

	spec := jobs.BuildGraphSpec{
		Dataset:          "demo",
		CorpusPath:       corpusPath,
		SchemaPath:       schemaPath,
		GraphOutputPath:  graphPath,
		ChunksOutputPath: chunksPath,
		WALPath:          walPath,
		Resume:           true,
		MaxWorkers:       1,
		RunnerCount:      3,
		LLMRateLimitRPM:  6000,
		LLMRateLimitFile: limiterPath,
		SkipCommunities:  true,
		ConfigPath:       "config/base_config.yaml",
		Mode:             "noagent",
	}
	result, err := buildgraph.Run(context.Background(), buildgraph.Config{
		PythonBin:  pythonPathWrapper(t, dir),
		ScriptPath: script,
		WorkingDir: dir,
	}, spec)
	if err != nil {
		t.Fatalf("run multi-runner worker: %v", err)
	}
	if result.TotalChunks != 4 || result.SucceededChunks != 4 || result.RunnerCount != 3 ||
		result.LLMRateLimitRPM != 6000 || !result.SkipCommunities {
		t.Fatalf("multi-runner result = %#v", result)
	}
	if _, err := os.Stat(limiterPath); err != nil {
		t.Fatalf("limiter file missing: %v", err)
	}
	records := readWALRecords(t, walPath)
	if got := countWALEvent(records, "chunk_succeeded"); got != 4 {
		t.Fatalf("chunk_succeeded count = %d, records = %#v", got, records)
	}
	for ordinal := 0; ordinal < 4; ordinal++ {
		if got := countChunkOrdinal(records, ordinal); got != 1 {
			t.Fatalf("chunk ordinal %d success count = %d, records = %#v", ordinal, got, records)
		}
	}
	for _, runnerIndex := range []float64{0, 1, 2} {
		if !hasRunRecordForRunner(records, runnerIndex) {
			t.Fatalf("missing run record for runner %v, records = %#v", runnerIndex, records)
		}
	}
	if !hasRunRecordWithRateLimit(records, 6000) {
		t.Fatalf("missing rate-limit metadata in WAL, records = %#v", records)
	}
	if !strings.Contains(string(mustRead(t, chunksPath)), "Alpha") || !strings.Contains(string(mustRead(t, chunksPath)), "Delta") {
		t.Fatalf("chunks output = %s", mustRead(t, chunksPath))
	}

	again, err := buildgraph.Run(context.Background(), buildgraph.Config{
		PythonBin:  pythonPathWrapper(t, dir),
		ScriptPath: script,
		WorkingDir: dir,
	}, spec)
	if err != nil {
		t.Fatalf("resume multi-runner worker: %v", err)
	}
	if again.SkippedChunks != 4 || again.SucceededChunks != 4 {
		t.Fatalf("resume result = %#v", again)
	}
	resumedRecords := readWALRecords(t, walPath)
	if got := countWALEvent(resumedRecords, "chunk_succeeded"); got != 4 {
		t.Fatalf("resume duplicated chunk_succeeded count = %d, records = %#v", got, resumedRecords)
	}
}

func TestRunMultiRunnerWorkerFailureDoesNotCompact(t *testing.T) {
	dir := t.TempDir()
	script := realBuildGraphWorkerPath(t)
	prepareFakeYoutuGraphRAG(t, dir)
	corpusPath := filepath.Join(dir, "corpus.json")
	schemaPath := filepath.Join(dir, "schema.json")
	graphPath := filepath.Join(dir, "out", "demo_new.json")
	chunksPath := filepath.Join(dir, "out", "demo.txt")
	walPath := filepath.Join(dir, "out", "demo.wal.jsonl")
	mustWrite(t, corpusPath, `[
{"title":"one","text":"Alpha"},
{"title":"two","text":"FAIL"},
{"title":"three","text":"Gamma"}
]`)
	mustWrite(t, schemaPath, `{"Nodes":["entity"],"Relations":["related"],"Attributes":["name"]}`)

	_, err := buildgraph.Run(context.Background(), buildgraph.Config{
		PythonBin:  pythonPathWrapper(t, dir),
		ScriptPath: script,
		WorkingDir: dir,
	}, jobs.BuildGraphSpec{
		Dataset:          "demo",
		CorpusPath:       corpusPath,
		SchemaPath:       schemaPath,
		GraphOutputPath:  graphPath,
		ChunksOutputPath: chunksPath,
		WALPath:          walPath,
		Resume:           true,
		MaxWorkers:       1,
		RunnerCount:      2,
		SkipCommunities:  true,
		ConfigPath:       "config/base_config.yaml",
		Mode:             "noagent",
	})
	if err == nil || !strings.Contains(err.Error(), "graph_build_runner_failed") {
		t.Fatalf("multi-runner failure err = %v", err)
	}
	records := readWALRecords(t, walPath)
	if got := countWALEvent(records, "chunk_failed"); got != 1 {
		t.Fatalf("chunk_failed count = %d, records = %#v", got, records)
	}
	if countWALEvent(records, "run_succeeded") != 0 {
		t.Fatalf("failed run should not compact successfully, records = %#v", records)
	}
	if _, err := os.Stat(graphPath); !os.IsNotExist(err) {
		t.Fatalf("graph output should not be written after runner failure, stat err = %v", err)
	}
	if _, err := os.Stat(chunksPath); !os.IsNotExist(err) {
		t.Fatalf("chunks output should not be written after runner failure, stat err = %v", err)
	}
}

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}

func realBuildGraphWorkerPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "scripts", "build_graph_worker.py"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("build graph worker script missing: %v", err)
	}
	return path
}

func prepareFakeYoutuGraphRAG(t *testing.T, dir string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "json_repair.py"), `
import json

def load(f):
    return json.load(f)
`)
	mustWrite(t, filepath.Join(dir, "config.py"), `
class _Construction:
    chunk_size = 1000
    overlap = 200
    min_tail_tokens = 100

class ConfigManager:
    def __init__(self, path):
        self.path = path
        self.construction = _Construction()
    def get_dataset_config(self, dataset):
        return type("DatasetConfig", (), {"schema_path": ""})()
    def override_config(self, overrides):
        self.overrides = overrides
`)
	mustWrite(t, filepath.Join(dir, "models", "__init__.py"), "")
	mustWrite(t, filepath.Join(dir, "models", "constructor", "__init__.py"), "")
	mustWrite(t, filepath.Join(dir, "models", "constructor", "kt_gen.py"), `
import threading

class FakeGraph:
    def __init__(self):
        self.nodes = {}
        self.edges = []
    def add_node(self, node_id, **data):
        self.nodes[node_id] = data
    def add_edge(self, u, v, **data):
        self.edges.append({"u": u, "v": v, **data})

class KTBuilder:
    datasets_no_chunk = {"demo"}
    def __init__(self, dataset_name, schema, mode="noagent", config=None):
        self.dataset_name = dataset_name
        self.schema = schema
        self.mode = mode
        self.config = config
        self.lock = threading.Lock()
        self.graph = FakeGraph()
        self.all_chunks = {}
    def _split_text_with_overlap(self, raw_text, chunk_size, overlap, min_tail_tokens):
        return [raw_text]
    def _get_construction_prompt(self, chunk_text):
        return chunk_text
    def extract_with_llm(self, prompt):
        if "FAIL" in prompt:
            raise RuntimeError("forced chunk failure")
        return prompt
    def _validate_and_parse_llm_response(self, prompt, llm_response):
        return {"attributes": {}, "triples": [{"head": prompt, "relation": "mentions", "tail": prompt}], "entity_types": {}}
    def _process_attributes(self, extracted_attr, chunk_id, entity_types):
        return [], []
    def _process_triples(self, extracted_triples, chunk_id, entity_types):
        return [(chunk_id, {"kind": "chunk"})], []
    def _process_attributes_agent(self, extracted_attr, chunk_id, entity_types):
        self.graph.add_node(chunk_id, kind="chunk")
    def _process_triples_agent(self, extracted_triples, chunk_id, entity_types):
        self.graph.add_node(chunk_id, kind="chunk")
    def _update_schema_with_new_types(self, new_schema_types):
        pass
    def token_cal(self, text):
        return len(text)
    def triple_deduplicate(self):
        pass
    def process_level4(self):
        raise RuntimeError("process_level4 should be skipped in this test")
    def format_output(self):
        return [{"id": node_id, **data} for node_id, data in sorted(self.graph.nodes.items())]
`)
}

func pythonPathWrapper(t *testing.T, dir string) string {
	t.Helper()
	wrapper := filepath.Join(dir, "python_with_path.sh")
	writeExecutable(t, wrapper, "#!/bin/sh\nPYTHONPATH="+dir+":${PYTHONPATH:-} exec python3 \"$@\"\n")
	return wrapper
}

func readWALRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	body := mustRead(t, path)
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode wal line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func countWALEvent(records []map[string]any, event string) int {
	count := 0
	for _, record := range records {
		if record["event"] == event {
			count++
		}
	}
	return count
}

func countChunkOrdinal(records []map[string]any, ordinal int) int {
	count := 0
	for _, record := range records {
		if record["event"] == "chunk_succeeded" && record["chunk_ordinal"] == float64(ordinal) {
			count++
		}
	}
	return count
}

func hasRunRecordForRunner(records []map[string]any, runnerIndex float64) bool {
	for _, record := range records {
		event, _ := record["event"].(string)
		if !strings.HasPrefix(event, "run_") {
			continue
		}
		payload, _ := record["payload"].(map[string]any)
		if payload["runner_index"] == runnerIndex {
			return true
		}
	}
	return false
}

func hasRunRecordWithRateLimit(records []map[string]any, rpm float64) bool {
	for _, record := range records {
		event, _ := record["event"].(string)
		if !strings.HasPrefix(event, "run_") {
			continue
		}
		payload, _ := record["payload"].(map[string]any)
		if payload["llm_rate_limit_rpm"] == rpm {
			return true
		}
	}
	return false
}

func mustWrite(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}
