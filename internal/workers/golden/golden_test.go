package golden_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/likun666661/youtu-rag-service/internal/jobs"
	"github.com/likun666661/youtu-rag-service/internal/workers/golden"
)

func TestRunExecutesPythonWorker(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	output := filepath.Join(dir, "out", "golden.json")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import sys
args = sys.argv
out = args[args.index("--output") + 1]
dataset = args[args.index("--dataset") + 1]
with open(out, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "retriever-golden/v1", "dataset": dataset}, f)
print(json.dumps({"ok": True, "output": out}))
`)

	result, err := golden.Run(context.Background(), golden.Config{
		PythonBin:  "python3",
		ScriptPath: script,
		WorkingDir: dir,
	}, jobs.GenerateGoldenSpec{
		Dataset:       "demo",
		OutputPath:    output,
		Limit:         2,
		Questions:     []string{"Who?"},
		TopK:          5,
		InvolvedTypes: `{"nodes":[],"relations":[],"attributes":[]}`,
		ConfigPath:    "config/base_config.yaml",
	})
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if result.SchemaVersion != "generate-golden-result/v1" || result.OutputPath != output {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("output not written: %v", err)
	}
}

func TestRunReportsWorkerFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
import sys
print("bad runtime", file=sys.stderr)
raise SystemExit(7)
`)

	_, err := golden.Run(context.Background(), golden.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.GenerateGoldenSpec{
		Dataset:    "demo",
		OutputPath: filepath.Join(dir, "golden.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "bad runtime") {
		t.Fatalf("failure err = %v", err)
	}
}

func TestRunRequiresGoldenFixtureOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	writeExecutable(t, script, `#!/usr/bin/env python3
print("ok but no file")
`)

	_, err := golden.Run(context.Background(), golden.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.GenerateGoldenSpec{
		Dataset:    "demo",
		OutputPath: filepath.Join(dir, "missing.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "golden output missing") {
		t.Fatalf("missing output err = %v", err)
	}
}

func TestRunRejectsUnexpectedGoldenSchema(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker.py")
	output := filepath.Join(dir, "golden.json")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import sys
out = sys.argv[sys.argv.index("--output") + 1]
with open(out, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "not-golden/v1"}, f)
`)

	_, err := golden.Run(context.Background(), golden.Config{
		PythonBin:  "python3",
		ScriptPath: script,
	}, jobs.GenerateGoldenSpec{
		Dataset:    "demo",
		OutputPath: output,
	})
	if err == nil || !strings.Contains(err.Error(), `unexpected golden schema_version "not-golden/v1"`) {
		t.Fatalf("schema err = %v", err)
	}
}

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}
