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

func TestDeleteRemovesManagedArtifactsOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	sourceCorpus := filepath.Join(dir, "source", "corpus.json")
	sourceSchema := filepath.Join(dir, "source", "schema.json")
	mustWrite(t, sourceCorpus, `[]`)
	mustWrite(t, sourceSchema, `{}`)
	if _, err := datasetimport.Import(cfg, datasetimport.Request{Dataset: "demo", CorpusPath: sourceCorpus, SchemaPath: sourceSchema}); err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, path := range []string{
		filepath.Join(dir, "output", "graphs", "demo_new.json"),
		filepath.Join(dir, "output", "chunks", "demo.txt"),
		filepath.Join(dir, "retriever", "faiss_cache_new", "demo", "index.faiss"),
		filepath.Join(dir, "output", "retrieval_golden", "demo.json"),
		filepath.Join(dir, "output", "retrieval_traces", "demo_triple_trace.json"),
		filepath.Join(dir, "output", "answers", "demo.json"),
	} {
		mustWrite(t, path, "{}")
	}
	legacyCorpus := filepath.Join(dir, "data", "demo", "demo_corpus.json")
	mustWrite(t, legacyCorpus, `{"legacy":true}`)

	result, err := datasetimport.Delete(cfg, datasetimport.DeleteRequest{Dataset: "demo", IncludeOutput: true})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if result.SchemaVersion != "dataset-delete/v1" || result.Status != "deleted" || len(result.Errors) != 0 {
		t.Fatalf("delete result = %#v", result)
	}
	for _, path := range []string{
		filepath.Join(dir, "data", "uploaded", "demo", "corpus.json"),
		filepath.Join(dir, "schemas", "demo.json"),
		filepath.Join(dir, "output", "datasets", "demo.json"),
		filepath.Join(dir, "output", "graphs", "demo_new.json"),
		filepath.Join(dir, "output", "chunks", "demo.txt"),
		filepath.Join(dir, "retriever", "faiss_cache_new", "demo"),
		filepath.Join(dir, "output", "retrieval_golden", "demo.json"),
		filepath.Join(dir, "output", "retrieval_traces", "demo_triple_trace.json"),
		filepath.Join(dir, "output", "answers", "demo.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected deleted artifact %s, stat err = %v", path, err)
		}
	}
	if _, err := os.Stat(legacyCorpus); err != nil {
		t.Fatalf("legacy corpus should not be deleted: %v", err)
	}
}

func TestDeleteSupportsDryRunForceAndOutputScope(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	mustWrite(t, filepath.Join(dir, "data", "uploaded", "orphan", "corpus.json"), `[]`)
	if _, err := datasetimport.Delete(cfg, datasetimport.DeleteRequest{Dataset: "orphan", IncludeOutput: true}); !errors.Is(err, datasetimport.ErrNotManaged) {
		t.Fatalf("not managed delete err = %v", err)
	}
	dryRun, err := datasetimport.Delete(cfg, datasetimport.DeleteRequest{Dataset: "orphan", IncludeOutput: true, DryRun: true, Force: true})
	if err != nil {
		t.Fatalf("force dry-run delete: %v", err)
	}
	if dryRun.Status != "skipped" || !dryRun.DryRun {
		t.Fatalf("dry run result = %#v", dryRun)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "uploaded", "orphan", "corpus.json")); err != nil {
		t.Fatalf("dry run removed corpus: %v", err)
	}

	sourceCorpus := filepath.Join(dir, "source", "corpus.json")
	sourceSchema := filepath.Join(dir, "source", "schema.json")
	mustWrite(t, sourceCorpus, `[]`)
	mustWrite(t, sourceSchema, `{}`)
	if _, err := datasetimport.Import(cfg, datasetimport.Request{Dataset: "scoped", CorpusPath: sourceCorpus, SchemaPath: sourceSchema}); err != nil {
		t.Fatalf("import scoped: %v", err)
	}
	graph := filepath.Join(dir, "output", "graphs", "scoped_new.json")
	mustWrite(t, graph, "[]")
	result, err := datasetimport.Delete(cfg, datasetimport.DeleteRequest{Dataset: "scoped", IncludeOutput: false})
	if err != nil {
		t.Fatalf("scoped delete: %v", err)
	}
	if result.IncludeOutput {
		t.Fatalf("include outputs = true: %#v", result)
	}
	if _, err := os.Stat(graph); err != nil {
		t.Fatalf("graph output should be skipped: %v", err)
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
