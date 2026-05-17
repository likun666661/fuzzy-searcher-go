package datasetimport_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuzzy-searcher-go/internal/config"
	"github.com/fuzzy-searcher-go/internal/datasetimport"
)

func TestImportCopiesArtifactsAndWritesMetadata(t *testing.T) {
	dir := t.TempDir()
	sourceCorpus := filepath.Join(dir, "source", "corpus.json")
	sourceSchema := filepath.Join(dir, "source", "schema.json")
	mustWrite(t, sourceCorpus, `[{"id":"doc1","text":"hello"}]`)
	mustWrite(t, sourceSchema, `{"entities":[]}`)
	cfg := testConfig(dir)

	metadata, err := datasetimport.Import(cfg, datasetimport.Request{
		Dataset:    "news_2026",
		CorpusPath: sourceCorpus,
		SchemaPath: sourceSchema,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if metadata.SchemaVersion != datasetimport.SchemaVersion || metadata.Dataset != "news_2026" || metadata.Status != "imported" {
		t.Fatalf("metadata = %#v", metadata)
	}
	for _, path := range []string{
		filepath.Join(dir, "data", "uploaded", "news_2026", "corpus.json"),
		filepath.Join(dir, "schemas", "news_2026.json"),
		filepath.Join(dir, "output", "datasets", "news_2026.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}
	if len(metadata.Artifacts) != 3 || metadata.Artifacts[2].Name != "metadata" || metadata.Artifacts[2].Status != "written" {
		t.Fatalf("artifacts = %#v", metadata.Artifacts)
	}
}

func TestImportRejectsUnsafeDatasetAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	sourceCorpus := filepath.Join(dir, "source", "corpus.json")
	sourceSchema := filepath.Join(dir, "source", "schema.json")
	mustWrite(t, sourceCorpus, `[]`)
	mustWrite(t, sourceSchema, `{}`)
	cfg := testConfig(dir)

	if _, err := datasetimport.Import(cfg, datasetimport.Request{Dataset: "../bad", CorpusPath: sourceCorpus, SchemaPath: sourceSchema}); !errors.Is(err, datasetimport.ErrInvalidDataset) {
		t.Fatalf("unsafe dataset err = %v", err)
	}
	_, err := datasetimport.Import(cfg, datasetimport.Request{Dataset: "demo", CorpusPath: sourceCorpus, SchemaPath: sourceSchema})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	_, err = datasetimport.Import(cfg, datasetimport.Request{Dataset: "demo", CorpusPath: sourceCorpus, SchemaPath: sourceSchema})
	if !errors.Is(err, datasetimport.ErrAlreadyExists) {
		t.Fatalf("duplicate import err = %v", err)
	}
}

func TestImportRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	sourceCorpus := filepath.Join(dir, "source", "corpus.json")
	sourceSchema := filepath.Join(dir, "source", "schema.json")
	mustWrite(t, sourceCorpus, `not-json`)
	mustWrite(t, sourceSchema, `{}`)

	_, err := datasetimport.Import(testConfig(dir), datasetimport.Request{Dataset: "demo", CorpusPath: sourceCorpus, SchemaPath: sourceSchema})
	if err == nil {
		t.Fatal("expected invalid json error")
	}
}

func testConfig(root string) config.Config {
	return config.Config{
		ArtifactRoot:    root,
		CorpusRoot:      filepath.Join(root, "data"),
		SchemaRoot:      filepath.Join(root, "schemas"),
		DatasetMetaRoot: filepath.Join(root, "output", "datasets"),
		DefaultDataset:  "demo",
		DefaultGraph:    filepath.Join(root, "output", "graphs", "demo_new.json"),
		DefaultChunks:   filepath.Join(root, "output", "chunks", "demo.txt"),
		DatasetNames:    []string{"demo"},
		GraphRoot:       filepath.Join(root, "output", "graphs"),
		ChunksRoot:      filepath.Join(root, "output", "chunks"),
		CacheRoot:       filepath.Join(root, "retriever", "faiss_cache_new"),
		GoldenRoot:      filepath.Join(root, "output", "retrieval_golden"),
		TraceRoot:       filepath.Join(root, "output", "retrieval_traces"),
		JobRoot:         filepath.Join(root, "output", "jobs"),
		WorkflowRoot:    filepath.Join(root, "output", "workflows"),
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
