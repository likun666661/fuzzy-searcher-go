package svc_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestRetrieveUnsupportedModeReturnsBadRequest(t *testing.T) {
	dir := t.TempDir()
	graphPath, chunksPath := writeTinyGraphAndChunks(t, dir)

	service := svc.NewService(config.Config{})
	routes := service.Routes()

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"graph_path":` + quote(graphPath) + `,"chunks_path":` + quote(chunksPath) + `,"question":"Alice","mode":"unknown-mode"}`)
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/retrieve", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("retrieve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `unsupported mode`) || !strings.Contains(rec.Body.String(), `unknown-mode`) {
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

func TestReadyzReportsMissingRetrievalArtifacts(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "data", "demo", "demo_corpus.json"), "{}")
	mustWrite(t, filepath.Join(root, "schemas", "demo.json"), "{}")

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

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"ready":false`, `"dataset_status":"schema_ready"`, `"graph"`, `"chunks"`, `"cache"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("ready body missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestSidecarEndpoints(t *testing.T) {
	sidecarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/datasets/demo/cache" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dataset":"demo","indices":{"chunk":{"dimension":384,"ntotal":3}}}`))
	}))
	defer sidecarServer.Close()

	service := svc.NewService(config.Config{
		DefaultDataset: "demo",
		DefaultSidecar: sidecarServer.URL,
	})
	routes := service.Routes()

	list := httptest.NewRecorder()
	routes.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/sidecars", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("sidecars status = %d, body = %s", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"reachable":true`) {
		t.Fatalf("sidecars body = %s", list.Body.String())
	}

	health := httptest.NewRecorder()
	routes.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/v1/sidecars/vector/health?dataset=demo", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("sidecar health status = %d, body = %s", health.Code, health.Body.String())
	}
	if !strings.Contains(health.Body.String(), `"dimension":384`) {
		t.Fatalf("sidecar health body = %s", health.Body.String())
	}
}

func TestSidecarEndpointUnconfigured(t *testing.T) {
	service := svc.NewService(config.Config{DefaultDataset: "demo"})
	routes := service.Routes()

	health := httptest.NewRecorder()
	routes.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/v1/sidecars/vector/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("sidecar health status = %d, body = %s", health.Code, health.Body.String())
	}
	if !strings.Contains(health.Body.String(), `"configured":false`) {
		t.Fatalf("sidecar health body = %s", health.Body.String())
	}
}

func TestRetrieveNativePath1RerankDoesNotCallPath1Primitive(t *testing.T) {
	dir := t.TempDir()
	graphPath, chunksPath := writeTinyGraphAndChunks(t, dir)
	requests := map[string]int{}

	sidecarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/retrieval/path1-triples":
			t.Fatalf("native-path1-rerank must not call %s", r.URL.Path)
		case "/v1/embed":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":     "test-model",
				"dimension": 3,
				"vectors":   [][]float32{{0.1, 0.2, 0.3}},
			})
		case "/v1/faiss/search":
			var req struct {
				Index string `json:"index"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode search request: %v", err)
			}
			hits := []map[string]any{}
			switch req.Index {
			case "node":
				hits = []map[string]any{
					{"id": "n1", "score": 0.9, "rank": 1},
					{"id": "n2", "score": 0.8, "rank": 2},
				}
			case "relation":
				hits = []map[string]any{{"id": "knows", "score": 0.7, "rank": 1}}
			case "chunk":
				hits = []map[string]any{{"id": "c1", "score": 0.6, "rank": 1}}
			default:
				t.Fatalf("unexpected search index %q", req.Index)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dataset": "demo",
				"index":   req.Index,
				"hits":    hits,
			})
		case "/v1/retrieval/path2-triples":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_version": "path2-triples/v1",
				"dataset":        "demo",
				"rescored_triples": []map[string]any{
					{
						"rank":             1,
						"key":              "n1\tknows\tn2",
						"head_id":          "n1",
						"relation":         "knows",
						"tail_id":          "n2",
						"score":            0.9,
						"formatted_triple": "(Alice, knows, Bob) [score: 0.900]",
						"chunk_ids":        []string{"c1", "c2"},
					},
				},
			})
		case "/v1/retrieval/rerank-triples":
			var req struct {
				Triples []map[string]any `json:"triples"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode rerank request: %v", err)
			}
			if len(req.Triples) == 0 {
				t.Fatalf("rerank request contained no raw triples")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_version": "rerank-triples/v1",
				"dataset":        "demo",
				"input_count":    len(req.Triples),
				"reranked_triples": []map[string]any{
					{
						"rank":             1,
						"key":              "n1\tknows\tn2",
						"head_id":          "n1",
						"relation":         "knows",
						"tail_id":          "n2",
						"score":            0.7,
						"formatted_triple": "(Alice, knows, Bob) [score: 0.700]",
						"chunk_ids":        []string{"c1", "c2"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer sidecarServer.Close()

	service := svc.NewService(config.Config{})
	routes := service.Routes()
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"graph_path":` + quote(graphPath) + `,"chunks_path":` + quote(chunksPath) + `,"sidecar_url":` + quote(sidecarServer.URL) + `,"dataset":"demo","question":"Alice knows Bob","mode":"native-path1-rerank","top_k":5}`)
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/retrieve", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/v1/embed", "/v1/faiss/search", "/v1/retrieval/path2-triples", "/v1/retrieval/rerank-triples"} {
		if requests[path] == 0 {
			t.Fatalf("expected sidecar path %s to be called; calls = %#v", path, requests)
		}
	}
	if got := requests["/v1/retrieval/path1-triples"]; got != 0 {
		t.Fatalf("path1 primitive called %d times", got)
	}
	if !strings.Contains(rec.Body.String(), `"name":"go_path1_rerank_path2_primitive_merge"`) ||
		!strings.Contains(rec.Body.String(), `"path1_schema_version":"go-path1-candidates/v1"`) ||
		!strings.Contains(rec.Body.String(), `"rerank_input_count":1`) {
		t.Fatalf("retrieve body missing native path1 debug meta: %s", rec.Body.String())
	}
}

func TestRetrieveNative(t *testing.T) {
	dir := t.TempDir()
	graphPath, chunksPath := writeTinyGraphAndChunks(t, dir)

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

func TestRetrieveJobLifecycle(t *testing.T) {
	dir := t.TempDir()
	graphPath, chunksPath := writeTinyGraphAndChunks(t, dir)

	service := svc.NewService(config.Config{})
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"retrieve","retrieve":{"graph_path":` + quote(graphPath) + `,"chunks_path":` + quote(chunksPath) + `,"question":"Alice","mode":"native","top_k":5}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("create job missing id: %s", create.Body.String())
	}

	job := waitForServiceJob(t, routes, created.ID, "succeeded")
	result, ok := job["result"].(map[string]any)
	if !ok {
		t.Fatalf("job missing result: %#v", job)
	}
	triples, ok := result["triples"].([]any)
	if !ok || len(triples) == 0 {
		t.Fatalf("job result missing triples: %#v", result)
	}

	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"retrieve_started"`) || !strings.Contains(events.Body.String(), `"succeeded"`) {
		t.Fatalf("events body = %s", events.Body.String())
	}
}

func TestJobValidation(t *testing.T) {
	service := svc.NewService(config.Config{})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"unknown"}`)))
	if create.Code != http.StatusBadRequest {
		t.Fatalf("create invalid job status = %d, body = %s", create.Code, create.Body.String())
	}

	missing := httptest.NewRecorder()
	routes.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/jobs/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing job status = %d, body = %s", missing.Code, missing.Body.String())
	}
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func waitForServiceJob(t *testing.T, routes http.Handler, id string, want string) map[string]any {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("job status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var job map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
			t.Fatalf("decode job: %v", err)
		}
		if job["status"] == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, want)
	return nil
}

func writeTinyGraphAndChunks(t *testing.T, dir string) (string, string) {
	t.Helper()
	graphPath := filepath.Join(dir, "graph.json")
	chunksPath := filepath.Join(dir, "chunks.txt")
	graphJSON := `[
  {
    "start_node": {"id": "n1", "label": "entity", "properties": {"name": "Alice", "chunk id": "c1", "schema_type": "person"}},
    "relation": "knows",
    "end_node": {"id": "n2", "label": "entity", "properties": {"name": "Bob", "chunk id": "c2", "schema_type": "person"}}
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
	return graphPath, chunksPath
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
