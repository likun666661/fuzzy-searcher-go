package svc_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuzzy-searcher-go/internal/config"
	"github.com/fuzzy-searcher-go/internal/svc"
)

func TestHealthAndVersion(t *testing.T) {
	service := svc.NewService(config.Config{
		AppName:       "test-service",
		Env:           "test",
		ServerVersion: "v-test",
	})
	routes := service.Routes()

	health := httptest.NewRecorder()
	routes.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}
	if !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("health body = %s", health.Body.String())
	}

	version := httptest.NewRecorder()
	routes.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/v1/version", nil))
	if version.Code != http.StatusOK {
		t.Fatalf("version status = %d", version.Code)
	}
	if !strings.Contains(version.Body.String(), `"version":"v-test"`) {
		t.Fatalf("version body = %s", version.Body.String())
	}
}

func TestRetrieveValidation(t *testing.T) {
	service := svc.NewService(config.Config{})
	routes := service.Routes()

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"question": ""}`)
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/retrieve", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("retrieve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "graph_path is required") {
		t.Fatalf("retrieve body = %s", rec.Body.String())
	}
}

func TestDatasetEndpoints(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "data", "demo", "demo_corpus.json"), "{}")
	mustWrite(t, filepath.Join(root, "schemas", "demo.json"), "{}")
	mustWrite(t, filepath.Join(root, "output", "graphs", "demo_new.json"), "[]")
	mustWrite(t, filepath.Join(root, "output", "chunks", "demo.txt"), "")
	mustMkdir(t, filepath.Join(root, "retriever", "faiss_cache_new", "demo"))

	service := svc.NewService(config.Config{
		DefaultDataset: "demo",
		CorpusRoot:     filepath.Join(root, "data"),
		SchemaRoot:     filepath.Join(root, "schemas"),
		GraphRoot:      filepath.Join(root, "output", "graphs"),
		ChunksRoot:     filepath.Join(root, "output", "chunks"),
		CacheRoot:      filepath.Join(root, "retriever", "faiss_cache_new"),
		GoldenRoot:     filepath.Join(root, "output", "retrieval_golden"),
		TraceRoot:      filepath.Join(root, "output", "retrieval_traces"),
		DatasetNames:   []string{"demo"},
	})
	routes := service.Routes()

	list := httptest.NewRecorder()
	routes.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/datasets", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("datasets status = %d, body = %s", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"status":"retrieval_ready"`) {
		t.Fatalf("datasets body = %s", list.Body.String())
	}

	artifacts := httptest.NewRecorder()
	routes.ServeHTTP(artifacts, httptest.NewRequest(http.MethodGet, "/v1/datasets/demo/artifacts", nil))
	if artifacts.Code != http.StatusOK {
		t.Fatalf("artifacts status = %d, body = %s", artifacts.Code, artifacts.Body.String())
	}
	if !strings.Contains(artifacts.Body.String(), `"name":"graph"`) {
		t.Fatalf("artifacts body = %s", artifacts.Body.String())
	}
}

func TestRetrieveNative(t *testing.T) {
	dir := t.TempDir()
	graphPath := filepath.Join(dir, "graph.json")
	chunksPath := filepath.Join(dir, "chunks.txt")
	graphJSON := `[
  {
    "start_node": {"label": "entity", "properties": {"name": "Alice", "chunk id": "c1", "schema_type": "person"}},
    "relation": "knows",
    "end_node": {"label": "entity", "properties": {"name": "Bob", "chunk id": "c2", "schema_type": "person"}}
  }
]`
	chunksText := "id: c1\tChunk: Alice knows Bob.\n" +
		"id: c2\tChunk: Bob is known by Alice.\n"
	if err := os.WriteFile(graphPath, []byte(graphJSON), 0o600); err != nil {
		t.Fatalf("write graph: %v", err)
	}
	if err := os.WriteFile(chunksPath, []byte(chunksText), 0o600); err != nil {
		t.Fatalf("write chunks: %v", err)
	}

	service := svc.NewService(config.Config{})
	routes := service.Routes()
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"graph_path":` + quote(graphPath) + `,"chunks_path":` + quote(chunksPath) + `,"question":"Alice","mode":"native","top_k":5}`)
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/retrieve", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Alice") || !strings.Contains(rec.Body.String(), "knows") {
		t.Fatalf("retrieve body = %s", rec.Body.String())
	}
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
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
