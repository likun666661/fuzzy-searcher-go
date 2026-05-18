package config_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fuzzy-searcher-go/internal/config"
)

func TestLoadParsesServiceEnvironment(t *testing.T) {
	t.Setenv("YOUTU_RAG_APP_NAME", "retriever-api")
	t.Setenv("YOUTU_RAG_ENV", "test")
	t.Setenv("YOUTU_RAG_PROFILE", "demo")
	t.Setenv("YOUTU_RAG_VERSION", "v1")
	t.Setenv("YOUTU_RAG_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("YOUTU_RAG_DATASET", "demo")
	t.Setenv("YOUTU_RAG_ARTIFACT_ROOT", "/tmp/youtu")
	t.Setenv("YOUTU_RAG_SIDECAR_URL", "http://127.0.0.1:8765")
	t.Setenv("YOUTU_RAG_MODE", "native-path1-rerank")
	t.Setenv("YOUTU_RAG_JOB_ROOT", "/tmp/youtu-jobs")
	t.Setenv("YOUTU_RAG_DATASETS", " demo, news ,, legal ")
	t.Setenv("YOUTU_RAG_PATH2_THRESHOLD", "0.25")
	t.Setenv("YOUTU_RAG_SHUTDOWN_SECONDS", "3")
	t.Setenv("YOUTU_RAG_VALIDATE_ON_START", "true")

	cfg := config.Load()
	if cfg.AppName != "retriever-api" || cfg.Env != "test" || cfg.Profile != "demo" || cfg.ServerVersion != "v1" {
		t.Fatalf("identity config = %#v", cfg)
	}
	if cfg.HTTPAddr != "127.0.0.1:0" || cfg.DefaultSidecar != "http://127.0.0.1:8765" {
		t.Fatalf("runtime config = %#v", cfg)
	}
	if cfg.DefaultMode != "native-path1-rerank" {
		t.Fatalf("default mode = %q", cfg.DefaultMode)
	}
	if !reflect.DeepEqual(cfg.DatasetNames, []string{"demo", "news", "legal"}) {
		t.Fatalf("dataset names = %#v", cfg.DatasetNames)
	}
	if cfg.Path2Threshold != 0.25 {
		t.Fatalf("path2 threshold = %v", cfg.Path2Threshold)
	}
	if cfg.ShutdownGrace != 3*time.Second {
		t.Fatalf("shutdown grace = %v", cfg.ShutdownGrace)
	}
	if !cfg.ValidateOnStart {
		t.Fatalf("validate on start = %v", cfg.ValidateOnStart)
	}
	if cfg.GraphRoot != filepath.Join("/tmp/youtu", "output", "graphs") {
		t.Fatalf("graph root = %q", cfg.GraphRoot)
	}
	if cfg.JobRoot != "/tmp/youtu-jobs" {
		t.Fatalf("job root = %q", cfg.JobRoot)
	}
	if cfg.DatasetMetaRoot != filepath.Join("/tmp/youtu", "output", "datasets") {
		t.Fatalf("dataset meta root = %q", cfg.DatasetMetaRoot)
	}
	if cfg.WorkflowRoot != filepath.Join("/tmp/youtu", "output", "workflows") {
		t.Fatalf("workflow root = %q", cfg.WorkflowRoot)
	}
	if cfg.PythonBin != filepath.Join("/tmp/youtu", ".venv", "bin", "python") {
		t.Fatalf("python bin = %q", cfg.PythonBin)
	}
	if cfg.GoldenScript != filepath.Join("/tmp/youtu", "scripts", "generate_retriever_golden.py") {
		t.Fatalf("golden script = %q", cfg.GoldenScript)
	}
	if cfg.ParseDocsScript != filepath.Join("/tmp/youtu", "scripts", "parse_documents_worker.py") {
		t.Fatalf("parse documents script = %q", cfg.ParseDocsScript)
	}
	if cfg.BuildGraphScript != filepath.Join("/tmp/youtu", "scripts", "build_graph_worker.py") {
		t.Fatalf("build graph script = %q", cfg.BuildGraphScript)
	}
	if cfg.AnswerScript != filepath.Join("/tmp/youtu", "scripts", "answer_worker.py") {
		t.Fatalf("answer script = %q", cfg.AnswerScript)
	}
	if cfg.WorkerCWD != "/tmp/youtu" {
		t.Fatalf("worker cwd = %q", cfg.WorkerCWD)
	}
}

func TestValidateServiceConfigurationProfiles(t *testing.T) {
	dir := t.TempDir()
	python := filepath.Join(dir, "python")
	golden := filepath.Join(dir, "generate.py")
	parse := filepath.Join(dir, "parse.py")
	build := filepath.Join(dir, "build.py")
	answer := filepath.Join(dir, "answer.py")
	graph := filepath.Join(dir, "output", "graphs", "demo_new.json")
	chunks := filepath.Join(dir, "output", "chunks", "demo.txt")
	for _, path := range []string{python, golden, parse, build, answer, graph, chunks} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	report := config.Validate(config.Config{
		Profile:          "production",
		HTTPAddr:         "127.0.0.1:0",
		DefaultDataset:   "demo",
		DefaultMode:      "native-path1-rerank",
		DefaultSidecar:   "http://127.0.0.1:8765",
		ArtifactRoot:     dir,
		WorkerCWD:        dir,
		PythonBin:        python,
		GoldenScript:     golden,
		ParseDocsScript:  parse,
		BuildGraphScript: build,
		AnswerScript:     answer,
	})
	if !report.Ready || report.SchemaVersion != config.ValidationSchemaVersion || report.Err() != nil {
		t.Fatalf("report = %#v err=%v", report, report.Err())
	}

	failed := config.Validate(config.Config{
		Profile:        "production",
		HTTPAddr:       "127.0.0.1:0",
		DefaultDataset: "demo",
		DefaultMode:    "native-path1-rerank",
		ArtifactRoot:   filepath.Join(dir, "missing"),
	})
	if failed.Ready || failed.Err() == nil {
		t.Fatalf("failed report = %#v err=%v", failed, failed.Err())
	}
}

func TestValidateServiceConfigurationStableChecksAndFailureProfiles(t *testing.T) {
	dir := t.TempDir()
	graph := filepath.Join(dir, "output", "graphs", "demo_new.json")
	chunks := filepath.Join(dir, "output", "chunks", "demo.txt")
	for _, path := range []string{graph, chunks} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	report := config.Validate(config.Config{
		Profile:        "demo",
		HTTPAddr:       "127.0.0.1:0",
		DefaultDataset: "demo",
		DefaultMode:    "native-path1-rerank",
		DefaultSidecar: "http://127.0.0.1:8765",
		ArtifactRoot:   dir,
		DefaultGraph:   graph,
		DefaultChunks:  chunks,
	})
	if !report.Ready {
		t.Fatalf("demo report should be ready: %#v err=%v", report, report.Err())
	}
	wantNames := []string{
		"profile",
		"http_addr",
		"default_dataset",
		"artifact_root",
		"worker_cwd",
		"python_bin",
		"golden_script",
		"parse_documents_script",
		"build_graph_script",
		"answer_script",
		"default_graph",
		"default_chunks",
		"sidecar_url",
	}
	var gotNames []string
	for _, check := range report.Checks {
		gotNames = append(gotNames, check.Name)
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("check names = %#v, want %#v", gotNames, wantNames)
	}

	demoMissing := config.Validate(config.Config{
		Profile:        "demo",
		HTTPAddr:       "127.0.0.1:0",
		DefaultDataset: "demo",
		DefaultMode:    "native-path1-rerank",
		ArtifactRoot:   filepath.Join(dir, "missing"),
		DefaultGraph:   filepath.Join(dir, "missing", "graph.json"),
		DefaultChunks:  filepath.Join(dir, "missing", "chunks.txt"),
	})
	if demoMissing.Ready || demoMissing.Err() == nil {
		t.Fatalf("demo missing report = %#v err=%v", demoMissing, demoMissing.Err())
	}
	assertValidationCheck(t, demoMissing, "artifact_root", "failed", true)
	assertValidationCheck(t, demoMissing, "default_graph", "failed", true)
	assertValidationCheck(t, demoMissing, "default_chunks", "failed", true)
	assertValidationCheck(t, demoMissing, "sidecar_url", "failed", true)

	invalidProfile := config.Validate(config.Config{
		Profile:        "staging",
		HTTPAddr:       "127.0.0.1:0",
		DefaultDataset: "demo",
		DefaultMode:    "native",
	})
	if invalidProfile.Ready || invalidProfile.Err() == nil {
		t.Fatalf("invalid profile report = %#v err=%v", invalidProfile, invalidProfile.Err())
	}
	assertValidationCheck(t, invalidProfile, "profile", "failed", true)
}

func TestServiceCheckConfigCommandAndValidateOnStart(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	bin := filepath.Join(t.TempDir(), "youtu-rag-service")
	build := exec.Command("go", "build", "-o", bin, "./cmd/youtu-rag-service")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build service binary: %v\n%s", err, out)
	}

	dir := t.TempDir()
	graph := filepath.Join(dir, "output", "graphs", "demo_new.json")
	chunks := filepath.Join(dir, "output", "chunks", "demo.txt")
	if err := os.MkdirAll(filepath.Dir(graph), 0o755); err != nil {
		t.Fatalf("mkdir graph: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(chunks), 0o755); err != nil {
		t.Fatalf("mkdir chunks: %v", err)
	}
	if err := os.WriteFile(graph, []byte("[]"), 0o644); err != nil {
		t.Fatalf("write graph: %v", err)
	}
	if err := os.WriteFile(chunks, []byte("id: c1\tChunk: hello\n"), 0o644); err != nil {
		t.Fatalf("write chunks: %v", err)
	}

	ready := exec.Command(bin, "--check-config")
	ready.Env = append(os.Environ(),
		"YOUTU_RAG_PROFILE=demo",
		"YOUTU_RAG_MODE=native",
		"YOUTU_RAG_DATASET=demo",
		"YOUTU_RAG_ARTIFACT_ROOT="+dir,
		"YOUTU_RAG_GRAPH="+graph,
		"YOUTU_RAG_CHUNKS="+chunks,
	)
	out, err := ready.CombinedOutput()
	if err != nil {
		t.Fatalf("ready check-config failed: %v\n%s", err, out)
	}
	var report config.ValidationReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode ready check-config: %v\n%s", err, out)
	}
	if report.SchemaVersion != config.ValidationSchemaVersion || !report.Ready || report.Profile != "demo" {
		t.Fatalf("ready report = %#v", report)
	}

	missing := exec.Command(bin, "--check-config")
	missing.Env = append(os.Environ(),
		"YOUTU_RAG_PROFILE=demo",
		"YOUTU_RAG_MODE=native-path1-rerank",
		"YOUTU_RAG_DATASET=demo",
		"YOUTU_RAG_ARTIFACT_ROOT="+filepath.Join(dir, "missing"),
		"YOUTU_RAG_GRAPH="+filepath.Join(dir, "missing", "graph.json"),
		"YOUTU_RAG_CHUNKS="+filepath.Join(dir, "missing", "chunks.txt"),
		"YOUTU_RAG_SIDECAR_URL=",
	)
	out, err = missing.CombinedOutput()
	if err == nil {
		t.Fatalf("missing check-config unexpectedly passed:\n%s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("missing check-config exit = %v, output:\n%s", err, out)
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode missing check-config: %v\n%s", err, out)
	}
	if report.Ready {
		t.Fatalf("missing report should not be ready: %#v", report)
	}
	assertValidationCheck(t, report, "sidecar_url", "failed", true)
	assertValidationCheck(t, report, "default_graph", "failed", true)

	strict := exec.Command(bin)
	strict.Env = append(os.Environ(),
		"YOUTU_RAG_PROFILE=staging",
		"YOUTU_RAG_VALIDATE_ON_START=true",
		"YOUTU_RAG_HTTP_ADDR=127.0.0.1:0",
	)
	out, err = strict.CombinedOutput()
	if err == nil {
		t.Fatalf("strict startup unexpectedly passed:\n%s", out)
	}
	if !strings.Contains(string(out), "service configuration is not ready") || !strings.Contains(string(out), "profile") {
		t.Fatalf("strict startup output = %s", out)
	}
}

func TestRunServiceLocalScriptHelp(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("sh", "scripts/run_service_local.sh", "--help")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("service local help failed: %v\n%s", err, out)
	}
	body := string(out)
	for _, want := range []string{"Usage: scripts/run_service_local.sh", "--check-only", "YOUTU_RAG_PROFILE=local|demo|production"} {
		if !strings.Contains(body, want) {
			t.Fatalf("help output missing %q:\n%s", want, body)
		}
	}
}

func TestLoadFallsBackOnInvalidNumericEnvironment(t *testing.T) {
	t.Setenv("YOUTU_RAG_DATASET", "demo")
	t.Setenv("YOUTU_RAG_DATASETS", " , ")
	t.Setenv("YOUTU_RAG_PATH2_THRESHOLD", "not-a-float")
	t.Setenv("YOUTU_RAG_SHUTDOWN_SECONDS", "not-an-int")

	cfg := config.Load()
	if !reflect.DeepEqual(cfg.DatasetNames, []string{"demo"}) {
		t.Fatalf("dataset fallback = %#v", cfg.DatasetNames)
	}
	if cfg.Path2Threshold != 0.1 {
		t.Fatalf("path2 fallback = %v", cfg.Path2Threshold)
	}
	if cfg.ShutdownGrace != 10*time.Second {
		t.Fatalf("shutdown fallback = %v", cfg.ShutdownGrace)
	}
}

func assertValidationCheck(t *testing.T, report config.ValidationReport, name string, status string, required bool) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != status || check.Required != required {
				t.Fatalf("check %s = %#v, want status=%s required=%v", name, check, status, required)
			}
			return
		}
	}
	t.Fatalf("missing check %s in %#v", name, report.Checks)
}
