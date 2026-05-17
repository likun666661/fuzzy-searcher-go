package parsedocs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuzzy-searcher-go/internal/jobs"
	"github.com/fuzzy-searcher-go/internal/workers/parsedocs"
)

func TestRunWritesCorpusOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "parse_worker.py")
	document := filepath.Join(dir, "doc.txt")
	output := filepath.Join(dir, "out", "corpus.json")
	mustWrite(t, document, "hello")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import os
import sys
output = sys.argv[sys.argv.index("--output") + 1]
document = sys.argv[sys.argv.index("--document") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump([{"id": "doc1", "text": open(document, encoding="utf-8").read()}], f)
print("parsed")
`)

	result, err := parsedocs.Run(context.Background(), parsedocs.Config{
		PythonBin:  "python3",
		ScriptPath: script,
		WorkingDir: dir,
	}, jobs.ParseDocumentsSpec{
		Dataset:       "demo",
		DocumentPaths: []string{document},
		OutputPath:    output,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.SchemaVersion != "parse-documents-result/v1" || result.OutputPath != output || result.Stdout != "parsed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunRejectsMissingAndBadOutput(t *testing.T) {
	dir := t.TempDir()
	document := filepath.Join(dir, "doc.txt")
	mustWrite(t, document, "hello")
	missingScript := filepath.Join(dir, "missing.py")
	writeExecutable(t, missingScript, `#!/usr/bin/env python3
print("no output")
`)
	_, err := parsedocs.Run(context.Background(), parsedocs.Config{PythonBin: "python3", ScriptPath: missingScript}, jobs.ParseDocumentsSpec{
		Dataset:       "demo",
		DocumentPaths: []string{document},
		OutputPath:    filepath.Join(dir, "missing", "corpus.json"),
	})
	if !errors.Is(err, parsedocs.ErrMissingOutput) {
		t.Fatalf("missing output err = %v", err)
	}

	badScript := filepath.Join(dir, "bad.py")
	writeExecutable(t, badScript, `#!/usr/bin/env python3
import os
import sys
output = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
open(output, "w", encoding="utf-8").write("not-json")
`)
	_, err = parsedocs.Run(context.Background(), parsedocs.Config{PythonBin: "python3", ScriptPath: badScript}, jobs.ParseDocumentsSpec{
		Dataset:       "demo",
		DocumentPaths: []string{document},
		OutputPath:    filepath.Join(dir, "bad", "corpus.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "parse corpus output") {
		t.Fatalf("bad output err = %v", err)
	}
}

func mustWrite(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	mustWrite(t, path, body)
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}
