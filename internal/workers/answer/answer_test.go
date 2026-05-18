package answer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/likun666661/youtu-rag-service/internal/jobs"
	"github.com/likun666661/youtu-rag-service/internal/workers/answer"
)

func TestRunExecutesPythonWorker(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	output := filepath.Join(dir, "out", "answer.json")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import os
import sys
out = sys.argv[sys.argv.index("--output") + 1]
dataset = sys.argv[sys.argv.index("--dataset") + 1]
question = sys.argv[sys.argv.index("--question") + 1]
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "answer-output/v1", "dataset": dataset, "question": question, "answer": "Paris"}, f)
print(json.dumps({"ok": True, "output": out}))
`)

	result, err := answer.Run(context.Background(), answer.Config{
		PythonBin:  "python3",
		ScriptPath: script,
		WorkingDir: dir,
	}, jobs.AnswerSpec{
		Dataset:       "demo",
		Question:      "Where?",
		OutputPath:    output,
		Mode:          "noagent",
		TopK:          3,
		GraphPath:     filepath.Join(dir, "graph.json"),
		ChunksPath:    filepath.Join(dir, "chunks.txt"),
		ConfigPath:    "config/base_config.yaml",
		InvolvedTypes: `{"nodes":[],"relations":[],"attributes":[]}`,
	})
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if result.SchemaVersion != "answer-result/v1" || result.OutputPath != output || result.Question != "Where?" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("answer output missing: %v", err)
	}
}

func TestRunReportsWorkerFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
import sys
print("answer runtime failed", file=sys.stderr)
raise SystemExit(7)
`)

	_, err := answer.Run(context.Background(), answer.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.AnswerSpec{
		Dataset:    "demo",
		Question:   "Who?",
		OutputPath: filepath.Join(dir, "answer.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "answer runtime failed") {
		t.Fatalf("failure err = %v", err)
	}
}

func TestRunRequiresAnswerOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
print("ok but no file")
`)

	_, err := answer.Run(context.Background(), answer.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.AnswerSpec{
		Dataset:    "demo",
		Question:   "Who?",
		OutputPath: filepath.Join(dir, "missing.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "answer output missing") {
		t.Fatalf("missing output err = %v", err)
	}
}

func TestRunRejectsUnexpectedAnswerSchema(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	output := filepath.Join(dir, "answer.json")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import sys
out = sys.argv[sys.argv.index("--output") + 1]
with open(out, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "not-answer/v1"}, f)
`)

	_, err := answer.Run(context.Background(), answer.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.AnswerSpec{
		Dataset:    "demo",
		Question:   "Who?",
		OutputPath: output,
	})
	if err == nil || !strings.Contains(err.Error(), `unexpected answer schema_version "not-answer/v1"`) {
		t.Fatalf("schema err = %v", err)
	}
}

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}
