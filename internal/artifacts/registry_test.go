package artifacts_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuzzy-searcher-go/internal/artifacts"
	"github.com/fuzzy-searcher-go/internal/config"
)

func TestRegistryDiscoversDatasetAndStatus(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "data", "demo", "demo_corpus.json"), "{}")
	mustWrite(t, filepath.Join(root, "schemas", "demo.json"), "{}")
	mustWrite(t, filepath.Join(root, "output", "graphs", "demo_new.json"), "[]")
	mustWrite(t, filepath.Join(root, "output", "chunks", "demo.txt"), "")
	mustMkdir(t, filepath.Join(root, "retriever", "faiss_cache_new", "demo"))
	mustWrite(t, filepath.Join(root, "output", "retrieval_golden", "demo.json"), "{}")
	mustWrite(t, filepath.Join(root, "output", "retrieval_traces", "demo_triple_trace.json"), "{}")

	registry := artifacts.NewRegistry(testConfig(root, []string{"demo"}))
	datasets := registry.List()
	if len(datasets) != 1 {
		t.Fatalf("datasets = %#v", datasets)
	}
	dataset := datasets[0]
	if dataset.Name != "demo" || dataset.Status != "retrieval_ready" || !dataset.RetrievalReady {
		t.Fatalf("dataset = %#v", dataset)
	}
	if len(dataset.Artifacts) != 7 {
		t.Fatalf("artifacts = %#v", dataset.Artifacts)
	}
}

func TestRegistryReportsMissingRetrievalArtifacts(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "data", "demo", "demo_corpus.json"), "{}")
	mustWrite(t, filepath.Join(root, "schemas", "demo.json"), "{}")

	registry := artifacts.NewRegistry(testConfig(root, []string{"demo"}))
	dataset := registry.Get("demo")
	if dataset.Status != "schema_ready" {
		t.Fatalf("status = %s", dataset.Status)
	}
	want := map[string]bool{"graph": true, "chunks": true, "cache": true}
	for _, name := range dataset.MissingRetrievalArtifacts {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing retrieval artifacts = %#v, still want %#v", dataset.MissingRetrievalArtifacts, want)
	}
}

func testConfig(root string, names []string) config.Config {
	return config.Config{
		DefaultDataset: "demo",
		CorpusRoot:     filepath.Join(root, "data"),
		SchemaRoot:     filepath.Join(root, "schemas"),
		GraphRoot:      filepath.Join(root, "output", "graphs"),
		ChunksRoot:     filepath.Join(root, "output", "chunks"),
		CacheRoot:      filepath.Join(root, "retriever", "faiss_cache_new"),
		GoldenRoot:     filepath.Join(root, "output", "retrieval_golden"),
		TraceRoot:      filepath.Join(root, "output", "retrieval_traces"),
		DatasetNames:   names,
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

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
