package config_test

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fuzzy-searcher-go/internal/config"
)

func TestLoadParsesServiceEnvironment(t *testing.T) {
	t.Setenv("YOUTU_RAG_APP_NAME", "retriever-api")
	t.Setenv("YOUTU_RAG_ENV", "test")
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

	cfg := config.Load()
	if cfg.AppName != "retriever-api" || cfg.Env != "test" || cfg.ServerVersion != "v1" {
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
