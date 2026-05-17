package buildgraph_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuzzy-searcher-go/internal/jobs"
	"github.com/fuzzy-searcher-go/internal/workers/buildgraph"
)

func TestRunExecutesPythonWorker(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	graphPath := filepath.Join(dir, "out", "demo_new.json")
	chunksPath := filepath.Join(dir, "out", "demo.txt")
	cacheDir := filepath.Join(dir, "cache", "demo")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import os
import sys
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
cache = sys.argv[sys.argv.index("--cache-dir") + 1]
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
print(json.dumps({"ok": True, "graph": graph, "chunks": chunks}))
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

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}
