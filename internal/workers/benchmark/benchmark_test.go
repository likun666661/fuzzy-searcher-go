package benchmark_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/likun666661/youtu-rag-service/internal/jobs"
	"github.com/likun666661/youtu-rag-service/internal/workers/benchmark"
)

func TestRunExecutesPythonWorker(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	output := filepath.Join(dir, "out", "benchmark.json")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import os
import sys
out = sys.argv[sys.argv.index("--output") + 1]
dataset = sys.argv[sys.argv.index("--dataset") + 1]
qa = sys.argv[sys.argv.index("--qa") + 1]
assert "--limit" in sys.argv
assert "--answer-model" in sys.argv
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    json.dump({
        "schema_version": "benchmark-result/v1",
        "dataset": dataset,
        "qa_path": qa,
        "question_count": 2,
        "correct_count": 1,
        "accuracy": 0.5,
        "items": [{"id": "qa_1", "judge": "1"}, {"id": "qa_2", "judge": "0"}],
    }, f)
print(json.dumps({"ok": True, "output": out}))
`)

	result, err := benchmark.Run(context.Background(), benchmark.Config{
		PythonBin:  "python3",
		ScriptPath: script,
		WorkingDir: dir,
	}, jobs.BenchmarkSpec{
		Dataset:     "anony_eng",
		QAPath:      filepath.Join(dir, "qa.json"),
		OutputPath:  output,
		Limit:       2,
		Offset:      1,
		Mode:        "noagent",
		TopK:        20,
		AnswerModel: "deepseek-v4-pro",
		JudgeModel:  "deepseek-v4-pro",
		LLMBaseURL:  "https://api.deepseek.com",
		GraphPath:   filepath.Join(dir, "graph.json"),
		ChunksPath:  filepath.Join(dir, "chunks.txt"),
		SchemaPath:  filepath.Join(dir, "schema.json"),
		CacheDir:    filepath.Join(dir, "cache"),
		ConfigPath:  "config/base_config.yaml",
	})
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if result.SchemaVersion != "benchmark-job-result/v1" || result.OutputPath != output ||
		result.QuestionCount != 2 || result.CorrectCount != 1 || result.Accuracy != 0.5 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunReportsWorkerFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
import sys
print("benchmark runtime failed", file=sys.stderr)
raise SystemExit(7)
`)

	_, err := benchmark.Run(context.Background(), benchmark.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.BenchmarkSpec{
		Dataset:    "demo",
		QAPath:     filepath.Join(dir, "qa.json"),
		OutputPath: filepath.Join(dir, "benchmark.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "benchmark runtime failed") {
		t.Fatalf("failure err = %v", err)
	}
}

func TestRunRequiresBenchmarkOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
print("ok but no file")
`)

	_, err := benchmark.Run(context.Background(), benchmark.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.BenchmarkSpec{
		Dataset:    "demo",
		QAPath:     filepath.Join(dir, "qa.json"),
		OutputPath: filepath.Join(dir, "missing.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "benchmark output missing") {
		t.Fatalf("missing output err = %v", err)
	}
}

func TestRunRejectsUnexpectedBenchmarkSchema(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	output := filepath.Join(dir, "benchmark.json")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import sys
out = sys.argv[sys.argv.index("--output") + 1]
with open(out, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "not-benchmark/v1", "items": []}, f)
`)

	_, err := benchmark.Run(context.Background(), benchmark.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.BenchmarkSpec{
		Dataset:    "demo",
		QAPath:     filepath.Join(dir, "qa.json"),
		OutputPath: output,
	})
	if err == nil || !strings.Contains(err.Error(), `unexpected benchmark schema_version "not-benchmark/v1"`) {
		t.Fatalf("schema err = %v", err)
	}
}

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}
