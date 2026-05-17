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

func TestDatasetImportEndpoint(t *testing.T) {
	root := t.TempDir()
	sourceCorpus := filepath.Join(root, "incoming", "corpus.json")
	sourceSchema := filepath.Join(root, "incoming", "schema.json")
	badSchema := filepath.Join(root, "incoming", "bad_schema.json")
	buildScript := filepath.Join(root, "workers", "build_graph.py")
	mustWrite(t, sourceCorpus, `[{"id":"doc1","text":"hello"}]`)
	mustWrite(t, sourceSchema, `{"entities":[]}`)
	mustWrite(t, badSchema, `{"entities":`)
	mustMkdir(t, filepath.Dir(buildScript))
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import json
import os
import sys

def value(flag):
    return sys.argv[sys.argv.index(flag) + 1]

dataset = value("--dataset")
corpus = value("--corpus")
schema = value("--schema")
graph = value("--graph-output")
chunks = value("--chunks-output")
cache = value("--cache-dir")
assert dataset == "imported", dataset
assert corpus.endswith("data/uploaded/imported/corpus.json"), corpus
assert schema.endswith("schemas/imported.json"), schema
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
`)
	cfg := config.Config{
		ArtifactRoot:     root,
		DefaultDataset:   "demo",
		CorpusRoot:       filepath.Join(root, "data"),
		SchemaRoot:       filepath.Join(root, "schemas"),
		GraphRoot:        filepath.Join(root, "output", "graphs"),
		ChunksRoot:       filepath.Join(root, "output", "chunks"),
		CacheRoot:        filepath.Join(root, "retriever", "faiss_cache_new"),
		GoldenRoot:       filepath.Join(root, "output", "retrieval_golden"),
		TraceRoot:        filepath.Join(root, "output", "retrieval_traces"),
		DatasetMetaRoot:  filepath.Join(root, "output", "datasets"),
		JobRoot:          filepath.Join(root, "jobs"),
		PythonBin:        "python3",
		BuildGraphScript: buildScript,
		WorkerCWD:        root,
		DatasetNames:     []string{"demo"},
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	body := bytes.NewBufferString(`{"dataset":"imported","corpus_path":` + quote(sourceCorpus) + `,"schema_path":` + quote(sourceSchema) + `}`)
	created := httptest.NewRecorder()
	routes.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", body))
	if created.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", created.Code, created.Body.String())
	}
	for _, want := range []string{
		`"schema_version":"dataset-import/v1"`,
		`"dataset":"imported"`,
		`"status":"imported"`,
		`"name":"corpus"`,
		`"name":"schema"`,
		`"name":"metadata"`,
	} {
		if !strings.Contains(created.Body.String(), want) {
			t.Fatalf("import body missing %s: %s", want, created.Body.String())
		}
	}
	for _, path := range []string{
		filepath.Join(root, "data", "uploaded", "imported", "corpus.json"),
		filepath.Join(root, "schemas", "imported.json"),
		filepath.Join(root, "output", "datasets", "imported.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("import artifact missing %s: %v", path, err)
		}
	}

	list := httptest.NewRecorder()
	routes.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/datasets", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"imported"`) || !strings.Contains(list.Body.String(), `"status":"schema_ready"`) {
		t.Fatalf("datasets after import status = %d, body = %s", list.Code, list.Body.String())
	}

	restartedRoutes := svc.NewService(cfg).Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/datasets/imported/artifacts", nil))
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), `"name":"metadata"`) || !strings.Contains(loaded.Body.String(), `"exists":true`) {
		t.Fatalf("loaded import artifacts status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
	loadedDataset := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loadedDataset, httptest.NewRequest(http.MethodGet, "/v1/datasets/imported", nil))
	if loadedDataset.Code != http.StatusOK ||
		!strings.Contains(loadedDataset.Body.String(), `"name":"imported"`) ||
		!strings.Contains(loadedDataset.Body.String(), `"name":"corpus"`) ||
		!strings.Contains(loadedDataset.Body.String(), `"name":"metadata"`) {
		t.Fatalf("loaded import dataset status = %d, body = %s", loadedDataset.Code, loadedDataset.Body.String())
	}

	build := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(build, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"build_graph","build_graph":{"dataset":"imported"}}`)))
	if build.Code != http.StatusAccepted {
		t.Fatalf("imported build_graph job status = %d, body = %s", build.Code, build.Body.String())
	}
	var buildJob struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(build.Body.Bytes(), &buildJob); err != nil || buildJob.ID == "" {
		t.Fatalf("decode imported build_graph job: id=%q err=%v body=%s", buildJob.ID, err, build.Body.String())
	}
	finishedBuild := waitForServiceJob(t, restartedRoutes, buildJob.ID, "succeeded")
	if !containsArtifactStatus(finishedBuild, "corpus", "configured") ||
		!containsArtifactStatus(finishedBuild, "schema", "configured") ||
		!strings.Contains(toJSON(t, finishedBuild["spec"]), filepath.Join(root, "data", "uploaded", "imported", "corpus.json")) ||
		!strings.Contains(toJSON(t, finishedBuild["spec"]), filepath.Join(root, "schemas", "imported.json")) {
		t.Fatalf("imported build_graph job did not consume managed artifacts: %#v", finishedBuild)
	}

	duplicate := httptest.NewRecorder()
	routes.ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"imported","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), "dataset_exists") {
		t.Fatalf("duplicate import status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}
	invalid := httptest.NewRecorder()
	routes.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"../bad","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_dataset") {
		t.Fatalf("invalid import status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
	badJSON := httptest.NewRecorder()
	routes.ServeHTTP(badJSON, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"badjson","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(badSchema)+`}`)))
	if badJSON.Code != http.StatusBadRequest || !strings.Contains(badJSON.Body.String(), "dataset_import_failed") || !strings.Contains(badJSON.Body.String(), "parse schema json") {
		t.Fatalf("bad schema import status = %d, body = %s", badJSON.Code, badJSON.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "output", "datasets", "badjson.json")); !os.IsNotExist(err) {
		t.Fatalf("bad schema import should not persist metadata, stat err = %v", err)
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
	if !strings.Contains(create.Body.String(), `"schema_version":"service-job/v1"`) ||
		!strings.Contains(create.Body.String(), `"spec"`) ||
		!strings.Contains(create.Body.String(), `"artifacts"`) ||
		!strings.Contains(create.Body.String(), `"name":"graph"`) ||
		!strings.Contains(create.Body.String(), `"name":"chunks"`) ||
		!strings.Contains(create.Body.String(), `"schema_version":"retrieve-result/v1"`) {
		t.Fatalf("create job missing typed spec/artifact contract: %s", create.Body.String())
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

func TestRetrieveJobPersistsContractAcrossServiceRestart(t *testing.T) {
	dir := t.TempDir()
	graphPath, chunksPath := writeTinyGraphAndChunks(t, dir)
	jobRoot := filepath.Join(dir, "jobs")
	cfg := config.Config{JobRoot: jobRoot}

	service := svc.NewService(cfg)
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"retrieve","retrieve":{"dataset":"demo","graph_path":` + quote(graphPath) + `,"chunks_path":` + quote(chunksPath) + `,"question":"Alice","mode":"native","top_k":5}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	_ = waitForServiceJob(t, routes, created.ID, "succeeded")
	if _, err := os.Stat(filepath.Join(jobRoot, created.ID+".json")); err != nil {
		t.Fatalf("persisted job record missing: %v", err)
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	get := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("reloaded job status = %d, body = %s", get.Code, get.Body.String())
	}
	var job map[string]any
	if err := json.Unmarshal(get.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode reloaded job: %v", err)
	}
	if job["schema_version"] != "service-job/v1" || job["type"] != "retrieve" || job["status"] != "succeeded" {
		t.Fatalf("reloaded job envelope = %#v", job)
	}
	spec, ok := job["spec"].(map[string]any)
	if !ok || spec["dataset"] != "demo" || spec["question"] != "Alice" || spec["mode"] != "native" || spec["top_k"] != float64(5) {
		t.Fatalf("reloaded job spec = %#v", job["spec"])
	}
	assertJobArtifacts(t, job["artifacts"], map[string]string{
		"graph":           "input:graph_json:",
		"chunks":          "input:chunks_txt:",
		"retrieve_result": "output:retrieve_result_json:retrieve-result/v1",
	})
	if _, ok := job["result"].(map[string]any); !ok {
		t.Fatalf("reloaded job result = %#v", job["result"])
	}

	events := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("reloaded events status = %d, body = %s", events.Code, events.Body.String())
	}
	for _, want := range []string{`"queued"`, `"running"`, `"retrieve_started"`, `"artifact_result_inline"`, `"succeeded"`} {
		if !strings.Contains(events.Body.String(), want) {
			t.Fatalf("reloaded events missing %s: %s", want, events.Body.String())
		}
	}
}

func TestRetrieveJobFailureKeepsStableErrorAndEvents(t *testing.T) {
	service := svc.NewService(config.Config{})
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"retrieve","retrieve":{"question":"Alice","mode":"native"}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if got, _ := job["error"].(string); !strings.Contains(got, "graph_path is required") {
		t.Fatalf("failed job error = %#v", job["error"])
	}
	spec, ok := job["spec"].(map[string]any)
	if !ok || spec["question"] != "Alice" || spec["mode"] != "native" {
		t.Fatalf("failed job spec = %#v", job["spec"])
	}
	assertJobArtifacts(t, job["artifacts"], map[string]string{
		"retrieve_result": "output:retrieve_result_json:retrieve-result/v1",
	})

	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("failed events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"retrieve_started"`) || !strings.Contains(events.Body.String(), `"failed"`) || !strings.Contains(events.Body.String(), "graph_path is required") {
		t.Fatalf("failed events body = %s", events.Body.String())
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

func TestGenerateGoldenJobLifecycle(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	outputPath := filepath.Join(dir, "golden", "demo.json")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import sys
dataset = sys.argv[sys.argv.index("--dataset") + 1]
output = sys.argv[sys.argv.index("--output") + 1]
with open(output, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "retriever-golden/v1", "dataset": dataset}, f)
print(json.dumps({"ok": True, "output": output}))
`)

	cfg := config.Config{
		DefaultDataset: "demo",
		GoldenRoot:     filepath.Join(dir, "golden"),
		JobRoot:        filepath.Join(dir, "jobs"),
		PythonBin:      "python3",
		GoldenScript:   scriptPath,
		WorkerCWD:      dir,
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"generate_golden","generate_golden":{"dataset":"demo","limit":2,"output_path":` + quote(outputPath) + `}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	for _, want := range []string{
		`"type":"generate_golden"`,
		`"schema_version":"service-job/v1"`,
		`"name":"golden_fixture"`,
		`"schema_version":"retriever-golden/v1"`,
		`"status":"pending"`,
	} {
		if !strings.Contains(create.Body.String(), want) {
			t.Fatalf("create job missing %s: %s", want, create.Body.String())
		}
	}
	var createdJob map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &createdJob); err != nil {
		t.Fatalf("decode created generate_golden job: %v", err)
	}
	spec, ok := createdJob["spec"].(map[string]any)
	if !ok ||
		spec["dataset"] != "demo" ||
		spec["output_path"] != outputPath ||
		spec["limit"] != float64(2) ||
		spec["python_bin"] != "python3" ||
		spec["script_path"] != scriptPath ||
		spec["working_dir"] != dir {
		t.Fatalf("created generate_golden spec = %#v", createdJob["spec"])
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "succeeded")
	result, ok := job["result"].(map[string]any)
	if !ok || result["schema_version"] != "generate-golden-result/v1" {
		t.Fatalf("job result = %#v", job["result"])
	}
	if !containsArtifactStatus(job, "golden_fixture", "written") {
		t.Fatalf("job artifacts not written: %#v", job["artifacts"])
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("golden output missing: %v", err)
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
	if loaded.Code != http.StatusOK {
		t.Fatalf("loaded job status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
	if !strings.Contains(loaded.Body.String(), `"status":"succeeded"`) ||
		!strings.Contains(loaded.Body.String(), `"status":"written"`) {
		t.Fatalf("loaded job body = %s", loaded.Body.String())
	}
	events := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("loaded events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"worker_started"`) ||
		!strings.Contains(events.Body.String(), `"artifact_golden_written"`) ||
		!strings.Contains(events.Body.String(), `"succeeded"`) {
		t.Fatalf("loaded events body = %s", events.Body.String())
	}
}

func TestGenerateGoldenJobFailure(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import sys
print("golden runtime failed", file=sys.stderr)
raise SystemExit(5)
`)

	service := svc.NewService(config.Config{
		DefaultDataset: "demo",
		GoldenRoot:     filepath.Join(dir, "golden"),
		JobRoot:        filepath.Join(dir, "jobs"),
		PythonBin:      "python3",
		GoldenScript:   scriptPath,
		WorkerCWD:      dir,
	})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"generate_golden","generate_golden":{"dataset":"demo"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, "golden runtime failed") {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "golden_fixture", "failed") {
		t.Fatalf("job artifacts not failed: %#v", job["artifacts"])
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"worker_started"`) ||
		!strings.Contains(events.Body.String(), `"failed"`) {
		t.Fatalf("events body = %s", events.Body.String())
	}
}

func TestGenerateGoldenJobMissingOutputMarksArtifactMissing(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
print("ok but did not write output")
`)

	service := svc.NewService(config.Config{
		DefaultDataset: "demo",
		GoldenRoot:     filepath.Join(dir, "golden"),
		JobRoot:        filepath.Join(dir, "jobs"),
		PythonBin:      "python3",
		GoldenScript:   scriptPath,
		WorkerCWD:      dir,
	})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"generate_golden","generate_golden":{"dataset":"demo"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, "golden output missing") {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "golden_fixture", "missing") {
		t.Fatalf("job artifacts not missing: %#v", job["artifacts"])
	}
}

func TestBuildGraphJobLifecycle(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	corpusPath := filepath.Join(dir, "data", "demo", "demo_corpus.json")
	schemaPath := filepath.Join(dir, "schemas", "demo.json")
	graphPath := filepath.Join(dir, "output", "graphs", "demo_new.json")
	chunksPath := filepath.Join(dir, "output", "chunks", "demo.txt")
	cacheDir := filepath.Join(dir, "retriever", "faiss_cache_new", "demo")
	mustWrite(t, corpusPath, "[]")
	mustWrite(t, schemaPath, "{}")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys
dataset = sys.argv[sys.argv.index("--dataset") + 1]
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
cache = sys.argv[sys.argv.index("--cache-dir") + 1]
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
print(json.dumps({"ok": True, "dataset": dataset, "graph": graph, "chunks": chunks}))
`)

	cfg := config.Config{
		DefaultDataset:   "demo",
		CorpusRoot:       filepath.Join(dir, "data"),
		SchemaRoot:       filepath.Join(dir, "schemas"),
		GraphRoot:        filepath.Join(dir, "output", "graphs"),
		ChunksRoot:       filepath.Join(dir, "output", "chunks"),
		CacheRoot:        filepath.Join(dir, "retriever", "faiss_cache_new"),
		JobRoot:          filepath.Join(dir, "jobs"),
		PythonBin:        "python3",
		BuildGraphScript: scriptPath,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"build_graph","build_graph":{"dataset":"demo"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	for _, want := range []string{
		`"type":"build_graph"`,
		`"schema_version":"service-job/v1"`,
		`"name":"corpus"`,
		`"name":"schema"`,
		`"name":"graph"`,
		`"name":"chunks"`,
		`"name":"cache"`,
		`"status":"pending"`,
	} {
		if !strings.Contains(create.Body.String(), want) {
			t.Fatalf("create job missing %s: %s", want, create.Body.String())
		}
	}
	var createdJob map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &createdJob); err != nil {
		t.Fatalf("decode created build_graph job: %v", err)
	}
	spec, ok := createdJob["spec"].(map[string]any)
	if !ok ||
		spec["dataset"] != "demo" ||
		spec["corpus_path"] != corpusPath ||
		spec["schema_path"] != schemaPath ||
		spec["graph_output_path"] != graphPath ||
		spec["chunks_output_path"] != chunksPath ||
		spec["cache_dir"] != cacheDir ||
		spec["python_bin"] != "python3" ||
		spec["script_path"] != scriptPath ||
		spec["working_dir"] != dir {
		t.Fatalf("created build_graph spec = %#v", createdJob["spec"])
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "succeeded")
	result, ok := job["result"].(map[string]any)
	if !ok || result["schema_version"] != "build-graph-result/v1" {
		t.Fatalf("job result = %#v", job["result"])
	}
	for _, name := range []string{"graph", "chunks", "cache"} {
		if !containsArtifactStatus(job, name, "written") {
			t.Fatalf("job artifact %s not written: %#v", name, job["artifacts"])
		}
	}
	if _, err := os.Stat(graphPath); err != nil {
		t.Fatalf("graph output missing: %v", err)
	}
	if _, err := os.Stat(chunksPath); err != nil {
		t.Fatalf("chunks output missing: %v", err)
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
	if loaded.Code != http.StatusOK {
		t.Fatalf("loaded job status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
	if !strings.Contains(loaded.Body.String(), `"status":"succeeded"`) ||
		!strings.Contains(loaded.Body.String(), `"status":"written"`) {
		t.Fatalf("loaded job body = %s", loaded.Body.String())
	}
	events := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("loaded events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"worker_started"`) ||
		!strings.Contains(events.Body.String(), `"artifact_graph_written"`) ||
		!strings.Contains(events.Body.String(), `"succeeded"`) {
		t.Fatalf("loaded events body = %s", events.Body.String())
	}
}

func TestBuildGraphJobMissingOutputMarksArtifactsMissing(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
print("ok but did not write graph or chunks")
`)

	service := svc.NewService(config.Config{
		DefaultDataset:   "demo",
		CorpusRoot:       filepath.Join(dir, "data"),
		SchemaRoot:       filepath.Join(dir, "schemas"),
		GraphRoot:        filepath.Join(dir, "output", "graphs"),
		ChunksRoot:       filepath.Join(dir, "output", "chunks"),
		CacheRoot:        filepath.Join(dir, "retriever", "faiss_cache_new"),
		JobRoot:          filepath.Join(dir, "jobs"),
		PythonBin:        "python3",
		BuildGraphScript: scriptPath,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"build_graph","build_graph":{"dataset":"demo"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, "build graph output missing") {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "graph", "missing") || !containsArtifactStatus(job, "chunks", "missing") {
		t.Fatalf("job artifacts not missing: %#v", job["artifacts"])
	}
}

func TestBuildGraphJobWorkerFailureMarksArtifactsFailed(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import sys
print("graph runtime failed", file=sys.stderr)
raise SystemExit(8)
`)

	service := svc.NewService(config.Config{
		DefaultDataset:   "demo",
		CorpusRoot:       filepath.Join(dir, "data"),
		SchemaRoot:       filepath.Join(dir, "schemas"),
		GraphRoot:        filepath.Join(dir, "output", "graphs"),
		ChunksRoot:       filepath.Join(dir, "output", "chunks"),
		CacheRoot:        filepath.Join(dir, "retriever", "faiss_cache_new"),
		JobRoot:          filepath.Join(dir, "jobs"),
		PythonBin:        "python3",
		BuildGraphScript: scriptPath,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"build_graph","build_graph":{"dataset":"demo"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, "graph runtime failed") {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "graph", "failed") || !containsArtifactStatus(job, "chunks", "failed") {
		t.Fatalf("job artifacts not failed: %#v", job["artifacts"])
	}

	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"worker_started"`) ||
		!strings.Contains(events.Body.String(), `"failed"`) ||
		!strings.Contains(events.Body.String(), "graph runtime failed") {
		t.Fatalf("events body = %s", events.Body.String())
	}
}

func TestAnswerJobLifecycle(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	graphPath := filepath.Join(dir, "output", "graphs", "demo_new.json")
	chunksPath := filepath.Join(dir, "output", "chunks", "demo.txt")
	answerPath := filepath.Join(dir, "output", "answers", "demo.json")
	mustWrite(t, graphPath, "[]")
	mustWrite(t, chunksPath, "id: c1\tChunk: hello\n")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys
dataset = sys.argv[sys.argv.index("--dataset") + 1]
question = sys.argv[sys.argv.index("--question") + 1]
output = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "answer-output/v1", "dataset": dataset, "question": question, "answer": "Barcelona"}, f)
print(json.dumps({"ok": True, "output": output}))
`)

	cfg := config.Config{
		DefaultDataset: "demo",
		DefaultMode:    "native-path1-rerank",
		ArtifactRoot:   dir,
		GraphRoot:      filepath.Join(dir, "output", "graphs"),
		ChunksRoot:     filepath.Join(dir, "output", "chunks"),
		JobRoot:        filepath.Join(dir, "jobs"),
		PythonBin:      "python3",
		AnswerScript:   scriptPath,
		WorkerCWD:      dir,
		DatasetNames:   []string{"demo"},
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"answer","answer":{"dataset":"demo","question":"Who signed?","top_k":5,"output_path":` + quote(answerPath) + `}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	for _, want := range []string{
		`"type":"answer"`,
		`"schema_version":"service-job/v1"`,
		`"name":"graph"`,
		`"name":"chunks"`,
		`"name":"answer"`,
		`"schema_version":"answer-output/v1"`,
		`"status":"pending"`,
	} {
		if !strings.Contains(create.Body.String(), want) {
			t.Fatalf("create job missing %s: %s", want, create.Body.String())
		}
	}
	var createdJob map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &createdJob); err != nil {
		t.Fatalf("decode created answer job: %v", err)
	}
	spec, ok := createdJob["spec"].(map[string]any)
	if !ok ||
		spec["dataset"] != "demo" ||
		spec["question"] != "Who signed?" ||
		spec["output_path"] != answerPath ||
		spec["mode"] != "native-path1-rerank" ||
		spec["top_k"] != float64(5) ||
		spec["graph_path"] != graphPath ||
		spec["chunks_path"] != chunksPath ||
		spec["python_bin"] != "python3" ||
		spec["script_path"] != scriptPath ||
		spec["working_dir"] != dir {
		t.Fatalf("created answer spec = %#v", createdJob["spec"])
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "succeeded")
	result, ok := job["result"].(map[string]any)
	if !ok || result["schema_version"] != "answer-result/v1" {
		t.Fatalf("job result = %#v", job["result"])
	}
	if !containsArtifactStatus(job, "answer", "written") {
		t.Fatalf("job answer artifact not written: %#v", job["artifacts"])
	}
	if _, err := os.Stat(answerPath); err != nil {
		t.Fatalf("answer output missing: %v", err)
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
	if loaded.Code != http.StatusOK {
		t.Fatalf("loaded job status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
	if !strings.Contains(loaded.Body.String(), `"status":"succeeded"`) ||
		!strings.Contains(loaded.Body.String(), `"status":"written"`) {
		t.Fatalf("loaded job body = %s", loaded.Body.String())
	}
	events := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("loaded events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"worker_started"`) ||
		!strings.Contains(events.Body.String(), `"artifact_answer_written"`) ||
		!strings.Contains(events.Body.String(), `"succeeded"`) {
		t.Fatalf("loaded events body = %s", events.Body.String())
	}
}

func TestAnswerJobFailureMarksArtifactFailed(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import sys
print("answer runtime failed", file=sys.stderr)
raise SystemExit(6)
`)

	service := svc.NewService(config.Config{
		DefaultDataset: "demo",
		ArtifactRoot:   dir,
		JobRoot:        filepath.Join(dir, "jobs"),
		PythonBin:      "python3",
		AnswerScript:   scriptPath,
		WorkerCWD:      dir,
	})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"answer","answer":{"dataset":"demo","question":"Who?"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, "answer runtime failed") {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "answer", "failed") {
		t.Fatalf("job artifacts not failed: %#v", job["artifacts"])
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"worker_started"`) ||
		!strings.Contains(events.Body.String(), `"failed"`) ||
		!strings.Contains(events.Body.String(), "answer runtime failed") {
		t.Fatalf("events body = %s", events.Body.String())
	}
}

func TestAnswerJobMissingOutputMarksArtifactMissing(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
print("ok but did not write answer")
`)

	service := svc.NewService(config.Config{
		DefaultDataset: "demo",
		ArtifactRoot:   dir,
		JobRoot:        filepath.Join(dir, "jobs"),
		PythonBin:      "python3",
		AnswerScript:   scriptPath,
		WorkerCWD:      dir,
	})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"answer","answer":{"dataset":"demo","question":"Who?"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, "answer output missing") {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "answer", "missing") {
		t.Fatalf("job artifacts not missing: %#v", job["artifacts"])
	}
}

func TestAnswerJobBadOutputSchemaMarksArtifactFailed(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys
output = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "not-answer/v1"}, f)
`)

	service := svc.NewService(config.Config{
		DefaultDataset: "demo",
		ArtifactRoot:   dir,
		JobRoot:        filepath.Join(dir, "jobs"),
		PythonBin:      "python3",
		AnswerScript:   scriptPath,
		WorkerCWD:      dir,
	})
	routes := service.Routes()

	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"type":"answer","answer":{"dataset":"demo","question":"Who?"}}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create job status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, `unexpected answer schema_version "not-answer/v1"`) {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "answer", "failed") {
		t.Fatalf("job artifacts not failed: %#v", job["artifacts"])
	}
}

func TestBuildAndAnswerWorkflowLifecycle(t *testing.T) {
	dir := t.TempDir()
	buildScript := filepath.Join(dir, "build_worker.py")
	answerScript := filepath.Join(dir, "answer_worker.py")
	corpusPath := filepath.Join(dir, "data", "demo", "demo_corpus.json")
	schemaPath := filepath.Join(dir, "schemas", "demo.json")
	graphPath := filepath.Join(dir, "output", "graphs", "demo_new.json")
	chunksPath := filepath.Join(dir, "output", "chunks", "demo.txt")
	cacheDir := filepath.Join(dir, "retriever", "faiss_cache_new", "demo")
	answerPath := filepath.Join(dir, "output", "answers", "demo.json")
	mustWrite(t, corpusPath, "[]")
	mustWrite(t, schemaPath, "{}")
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import json
import os
import sys
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
cache = sys.argv[sys.argv.index("--cache-dir") + 1]
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
`)
	writeExecutable(t, answerScript, `#!/usr/bin/env python3
import json
import os
import sys
graph = sys.argv[sys.argv.index("--graph") + 1]
chunks = sys.argv[sys.argv.index("--chunks") + 1]
output = sys.argv[sys.argv.index("--output") + 1]
question = sys.argv[sys.argv.index("--question") + 1]
if not os.path.exists(graph) or not os.path.exists(chunks):
    raise SystemExit("missing handoff artifacts")
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "answer-output/v1", "question": question, "answer": "ok"}, f)
`)

	cfg := config.Config{
		DefaultDataset:   "demo",
		DefaultMode:      "noagent",
		ArtifactRoot:     dir,
		CorpusRoot:       filepath.Join(dir, "data"),
		SchemaRoot:       filepath.Join(dir, "schemas"),
		GraphRoot:        filepath.Join(dir, "output", "graphs"),
		ChunksRoot:       filepath.Join(dir, "output", "chunks"),
		CacheRoot:        filepath.Join(dir, "retriever", "faiss_cache_new"),
		JobRoot:          filepath.Join(dir, "jobs"),
		WorkflowRoot:     filepath.Join(dir, "workflows"),
		PythonBin:        "python3",
		BuildGraphScript: buildScript,
		AnswerScript:     answerScript,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"build_and_answer","build_and_answer":{"dataset":"demo","question":"Who?","answer_output_path":` + quote(answerPath) + `}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/workflows", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create workflow status = %d, body = %s", create.Code, create.Body.String())
	}
	for _, want := range []string{
		`"schema_version":"workflow/v1"`,
		`"type":"build_and_answer"`,
		`"name":"graph"`,
		`"name":"chunks"`,
		`"name":"answer"`,
		`"status":"pending"`,
	} {
		if !strings.Contains(create.Body.String(), want) {
			t.Fatalf("create workflow missing %s: %s", want, create.Body.String())
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create workflow: %v", err)
	}
	workflow := waitForWorkflow(t, routes, created.ID, "succeeded")
	steps, ok := workflow["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	if !containsWorkflowStep(workflow, "build_graph", "build_graph", "succeeded") ||
		!containsWorkflowStep(workflow, "answer", "answer", "succeeded") {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	for _, name := range []string{"graph", "chunks", "cache", "answer"} {
		if !containsArtifactStatus(workflow, name, "written") {
			t.Fatalf("workflow artifact %s not written: %#v", name, workflow["artifacts"])
		}
	}
	for _, path := range []string{graphPath, chunksPath, cacheDir, answerPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("workflow artifact missing %s: %v", path, err)
		}
	}
	result, ok := workflow["result"].(map[string]any)
	if !ok || result["schema_version"] != "build-and-answer-result/v1" || result["answer_output_path"] != answerPath {
		t.Fatalf("workflow result = %#v", workflow["result"])
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+created.ID, nil))
	if loaded.Code != http.StatusOK {
		t.Fatalf("loaded workflow status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
	if !strings.Contains(loaded.Body.String(), `"status":"succeeded"`) ||
		!strings.Contains(loaded.Body.String(), `"status":"written"`) {
		t.Fatalf("loaded workflow body = %s", loaded.Body.String())
	}
	events := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("loaded workflow events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"step_started"`) ||
		!strings.Contains(events.Body.String(), `"artifact_handoff"`) ||
		!strings.Contains(events.Body.String(), `"succeeded"`) {
		t.Fatalf("loaded workflow events body = %s", events.Body.String())
	}
}

func TestWorkflowListAndStepInspectionEndpoints(t *testing.T) {
	dir := t.TempDir()
	buildScript := filepath.Join(dir, "build_worker.py")
	answerScript := filepath.Join(dir, "answer_worker.py")
	mustWrite(t, filepath.Join(dir, "data", "demo", "demo_corpus.json"), "[]")
	mustWrite(t, filepath.Join(dir, "schemas", "demo.json"), "{}")
	writeBuildWorker(t, buildScript)
	writeExecutable(t, answerScript, `#!/usr/bin/env python3
import json
import os
import sys
output = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "answer-output/v1", "answer": "ok"}, f)
`)

	cfg := workflowTestConfig(dir, buildScript, answerScript)
	service := svc.NewService(cfg)
	routes := service.Routes()

	workflow := createBuildAndAnswerWorkflow(t, routes, "demo", "Who?", "")
	workflow = waitForWorkflow(t, routes, workflow["id"].(string), "succeeded")
	id := workflow["id"].(string)

	list := httptest.NewRecorder()
	routes.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/workflows", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("workflow list status = %d, body = %s", list.Code, list.Body.String())
	}
	for _, want := range []string{`"schema_version":"workflow-list/v1"`, `"count":1`, id} {
		if !strings.Contains(list.Body.String(), want) {
			t.Fatalf("workflow list missing %s: %s", want, list.Body.String())
		}
	}

	steps := httptest.NewRecorder()
	routes.ServeHTTP(steps, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/steps", nil))
	if steps.Code != http.StatusOK {
		t.Fatalf("workflow steps status = %d, body = %s", steps.Code, steps.Body.String())
	}
	for _, want := range []string{
		`"schema_version":"workflow-steps/v1"`,
		`"count":2`,
		`"name":"build_graph"`,
		`"name":"answer"`,
		`"job_id":"job_`,
		`"output_artifacts"`,
	} {
		if !strings.Contains(steps.Body.String(), want) {
			t.Fatalf("workflow steps missing %s: %s", want, steps.Body.String())
		}
	}

	step := httptest.NewRecorder()
	routes.ServeHTTP(step, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/steps/build_graph", nil))
	if step.Code != http.StatusOK {
		t.Fatalf("workflow step status = %d, body = %s", step.Code, step.Body.String())
	}
	for _, want := range []string{
		`"schema_version":"workflow-step/v1"`,
		`"workflow_id":"` + id + `"`,
		`"name":"build_graph"`,
		`"type":"build_graph"`,
		`"status":"succeeded"`,
		`"job_id":"job_`,
	} {
		if !strings.Contains(step.Body.String(), want) {
			t.Fatalf("workflow step missing %s: %s", want, step.Body.String())
		}
	}

	missing := httptest.NewRecorder()
	routes.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/steps/missing", nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "workflow_step_not_found") {
		t.Fatalf("missing workflow step status = %d, body = %s", missing.Code, missing.Body.String())
	}
	missingWorkflow := httptest.NewRecorder()
	routes.ServeHTTP(missingWorkflow, httptest.NewRequest(http.MethodGet, "/v1/workflows/missing/steps", nil))
	if missingWorkflow.Code != http.StatusNotFound || !strings.Contains(missingWorkflow.Body.String(), "workflow_not_found") {
		t.Fatalf("missing workflow steps status = %d, body = %s", missingWorkflow.Code, missingWorkflow.Body.String())
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	restartedList := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(restartedList, httptest.NewRequest(http.MethodGet, "/v1/workflows", nil))
	if restartedList.Code != http.StatusOK || !strings.Contains(restartedList.Body.String(), id) {
		t.Fatalf("restarted workflow list status = %d, body = %s", restartedList.Code, restartedList.Body.String())
	}
	restartedStep := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(restartedStep, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/steps/answer", nil))
	if restartedStep.Code != http.StatusOK || !strings.Contains(restartedStep.Body.String(), `"name":"answer"`) {
		t.Fatalf("restarted workflow step status = %d, body = %s", restartedStep.Code, restartedStep.Body.String())
	}
}

func TestBuildAndAnswerWorkflowBuildFailureStopsAnswer(t *testing.T) {
	dir := t.TempDir()
	buildScript := filepath.Join(dir, "build_worker.py")
	answerScript := filepath.Join(dir, "answer_worker.py")
	answerMarker := filepath.Join(dir, "answer-was-called")
	mustWrite(t, filepath.Join(dir, "data", "demo", "demo_corpus.json"), "[]")
	mustWrite(t, filepath.Join(dir, "schemas", "demo.json"), "{}")
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import sys
print("build exploded", file=sys.stderr)
raise SystemExit(4)
`)
	writeExecutable(t, answerScript, `#!/usr/bin/env python3
import pathlib
pathlib.Path("`+answerMarker+`").write_text("called", encoding="utf-8")
`)

	cfg := workflowTestConfig(dir, buildScript, answerScript)
	service := svc.NewService(cfg)
	routes := service.Routes()

	workflow := createBuildAndAnswerWorkflow(t, routes, "demo", "Who?", "")
	workflow = waitForWorkflow(t, routes, workflow["id"].(string), "failed")
	if errorText, _ := workflow["error"].(string); !strings.Contains(errorText, "build_graph step failed") || !strings.Contains(errorText, "build exploded") {
		t.Fatalf("workflow error = %#v", workflow["error"])
	}
	if !containsWorkflowStep(workflow, "build_graph", "build_graph", "failed") {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	if containsWorkflowStepNamed(workflow, "answer") {
		t.Fatalf("answer step should not start after build failure: %#v", workflow["steps"])
	}
	if _, err := os.Stat(answerMarker); !os.IsNotExist(err) {
		t.Fatalf("answer worker should not have been called; stat err = %v", err)
	}

	steps := httptest.NewRecorder()
	routes.ServeHTTP(steps, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/steps", nil))
	if steps.Code != http.StatusOK {
		t.Fatalf("workflow steps status = %d, body = %s", steps.Code, steps.Body.String())
	}
	for _, want := range []string{`"schema_version":"workflow-steps/v1"`, `"count":1`, `"name":"build_graph"`, `"status":"failed"`, "build exploded"} {
		if !strings.Contains(steps.Body.String(), want) {
			t.Fatalf("workflow failed steps missing %s: %s", want, steps.Body.String())
		}
	}
	answerStep := httptest.NewRecorder()
	routes.ServeHTTP(answerStep, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/steps/answer", nil))
	if answerStep.Code != http.StatusNotFound || !strings.Contains(answerStep.Body.String(), "workflow_step_not_found") {
		t.Fatalf("answer step after build failure status = %d, body = %s", answerStep.Code, answerStep.Body.String())
	}

	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("workflow events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"step_failed"`) ||
		!strings.Contains(events.Body.String(), `"failed"`) ||
		strings.Contains(events.Body.String(), "artifact_handoff") {
		t.Fatalf("workflow events body = %s", events.Body.String())
	}
}

func TestBuildAndAnswerWorkflowAnswerFailurePreservesBuildArtifacts(t *testing.T) {
	dir := t.TempDir()
	buildScript := filepath.Join(dir, "build_worker.py")
	answerScript := filepath.Join(dir, "answer_worker.py")
	mustWrite(t, filepath.Join(dir, "data", "demo", "demo_corpus.json"), "[]")
	mustWrite(t, filepath.Join(dir, "schemas", "demo.json"), "{}")
	writeBuildWorker(t, buildScript)
	writeExecutable(t, answerScript, `#!/usr/bin/env python3
import sys
print("answer exploded", file=sys.stderr)
raise SystemExit(5)
`)

	cfg := workflowTestConfig(dir, buildScript, answerScript)
	service := svc.NewService(cfg)
	routes := service.Routes()

	workflow := createBuildAndAnswerWorkflow(t, routes, "demo", "Who?", "")
	workflow = waitForWorkflow(t, routes, workflow["id"].(string), "failed")
	if errorText, _ := workflow["error"].(string); !strings.Contains(errorText, "answer step failed") || !strings.Contains(errorText, "answer exploded") {
		t.Fatalf("workflow error = %#v", workflow["error"])
	}
	if !containsWorkflowStep(workflow, "build_graph", "build_graph", "succeeded") ||
		!containsWorkflowStep(workflow, "answer", "answer", "failed") {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	for _, name := range []string{"graph", "chunks", "cache"} {
		if !containsArtifactStatus(workflow, name, "written") {
			t.Fatalf("workflow artifact %s not preserved as written: %#v", name, workflow["artifacts"])
		}
	}
	if containsArtifactStatus(workflow, "answer", "written") {
		t.Fatalf("answer artifact should not be written: %#v", workflow["artifacts"])
	}

	answerStep := httptest.NewRecorder()
	routes.ServeHTTP(answerStep, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/steps/answer", nil))
	if answerStep.Code != http.StatusOK {
		t.Fatalf("answer step status = %d, body = %s", answerStep.Code, answerStep.Body.String())
	}
	for _, want := range []string{`"schema_version":"workflow-step/v1"`, `"name":"answer"`, `"status":"failed"`, "answer exploded"} {
		if !strings.Contains(answerStep.Body.String(), want) {
			t.Fatalf("answer failed step missing %s: %s", want, answerStep.Body.String())
		}
	}
	buildStep := httptest.NewRecorder()
	routes.ServeHTTP(buildStep, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/steps/build_graph", nil))
	if buildStep.Code != http.StatusOK || !strings.Contains(buildStep.Body.String(), `"status":"succeeded"`) || !strings.Contains(buildStep.Body.String(), `"output_artifacts"`) {
		t.Fatalf("build step after answer failure status = %d, body = %s", buildStep.Code, buildStep.Body.String())
	}

	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("workflow events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"artifact_handoff"`) ||
		!strings.Contains(events.Body.String(), `"step_failed"`) ||
		!strings.Contains(events.Body.String(), "answer exploded") {
		t.Fatalf("workflow events body = %s", events.Body.String())
	}
}

func TestBuildAndAnswerWorkflowCancelPropagatesToChildJob(t *testing.T) {
	dir := t.TempDir()
	buildScript := filepath.Join(dir, "build_worker.py")
	answerScript := filepath.Join(dir, "answer_worker.py")
	mustWrite(t, filepath.Join(dir, "data", "demo", "demo_corpus.json"), "[]")
	mustWrite(t, filepath.Join(dir, "schemas", "demo.json"), "{}")
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import time
time.sleep(10)
`)
	writeExecutable(t, answerScript, `#!/usr/bin/env python3
raise SystemExit("answer should not run")
`)

	cfg := workflowTestConfig(dir, buildScript, answerScript)
	service := svc.NewService(cfg)
	routes := service.Routes()

	workflow := createBuildAndAnswerWorkflow(t, routes, "demo", "Who?", "")
	id := workflow["id"].(string)
	_ = waitForWorkflowStep(t, routes, id, "build_graph")

	cancel := httptest.NewRecorder()
	routes.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/v1/workflows/"+id+"/cancel", nil))
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel workflow status = %d, body = %s", cancel.Code, cancel.Body.String())
	}
	if !strings.Contains(cancel.Body.String(), `"canceled":true`) {
		t.Fatalf("cancel workflow body = %s", cancel.Body.String())
	}
	workflow = waitForWorkflow(t, routes, id, "canceled")
	if !containsWorkflowStep(workflow, "build_graph", "build_graph", "canceled") {
		t.Fatalf("build step not canceled: %#v", workflow["steps"])
	}
	if containsWorkflowStepNamed(workflow, "answer") {
		t.Fatalf("answer should not start after cancellation: %#v", workflow["steps"])
	}

	buildStep := httptest.NewRecorder()
	routes.ServeHTTP(buildStep, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/steps/build_graph", nil))
	if buildStep.Code != http.StatusOK || !strings.Contains(buildStep.Body.String(), `"status":"canceled"`) {
		t.Fatalf("build step after cancel status = %d, body = %s", buildStep.Code, buildStep.Body.String())
	}
	answerStep := httptest.NewRecorder()
	routes.ServeHTTP(answerStep, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/steps/answer", nil))
	if answerStep.Code != http.StatusNotFound || !strings.Contains(answerStep.Body.String(), "workflow_step_not_found") {
		t.Fatalf("answer step after cancel status = %d, body = %s", answerStep.Code, answerStep.Body.String())
	}

	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("workflow events status = %d, body = %s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), `"cancel_requested"`) ||
		!strings.Contains(events.Body.String(), `"canceled"`) {
		t.Fatalf("workflow events body = %s", events.Body.String())
	}
}

func workflowTestConfig(dir string, buildScript string, answerScript string) config.Config {
	return config.Config{
		DefaultDataset:   "demo",
		DefaultMode:      "noagent",
		ArtifactRoot:     dir,
		CorpusRoot:       filepath.Join(dir, "data"),
		SchemaRoot:       filepath.Join(dir, "schemas"),
		GraphRoot:        filepath.Join(dir, "output", "graphs"),
		ChunksRoot:       filepath.Join(dir, "output", "chunks"),
		CacheRoot:        filepath.Join(dir, "retriever", "faiss_cache_new"),
		JobRoot:          filepath.Join(dir, "jobs"),
		WorkflowRoot:     filepath.Join(dir, "workflows"),
		PythonBin:        "python3",
		BuildGraphScript: buildScript,
		AnswerScript:     answerScript,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	}
}

func writeBuildWorker(t *testing.T, path string) {
	t.Helper()
	writeExecutable(t, path, `#!/usr/bin/env python3
import json
import os
import sys
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
cache = sys.argv[sys.argv.index("--cache-dir") + 1]
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
`)
}

func createBuildAndAnswerWorkflow(t *testing.T, routes http.Handler, dataset string, question string, answerOutputPath string) map[string]any {
	t.Helper()
	body := `{"type":"build_and_answer","build_and_answer":{"dataset":` + quote(dataset) + `,"question":` + quote(question)
	if answerOutputPath != "" {
		body += `,"answer_output_path":` + quote(answerOutputPath)
	}
	body += `}}`
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(body)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create workflow status = %d, body = %s", create.Code, create.Body.String())
	}
	var workflow map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &workflow); err != nil {
		t.Fatalf("decode create workflow: %v", err)
	}
	if workflow["id"] == "" || workflow["schema_version"] != "workflow/v1" {
		t.Fatalf("created workflow = %#v", workflow)
	}
	return workflow
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func toJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	return string(body)
}

func containsArtifactStatus(job map[string]any, name string, status string) bool {
	artifacts, ok := job["artifacts"].([]any)
	if !ok {
		return false
	}
	for _, artifact := range artifacts {
		item, ok := artifact.(map[string]any)
		if ok && item["name"] == name && item["status"] == status {
			return true
		}
	}
	return false
}

func containsWorkflowStep(workflow map[string]any, name string, jobType string, status string) bool {
	steps, ok := workflow["steps"].([]any)
	if !ok {
		return false
	}
	for _, step := range steps {
		item, ok := step.(map[string]any)
		if ok && item["name"] == name && item["type"] == jobType && item["status"] == status {
			if item["job_id"] == "" {
				return false
			}
			return true
		}
	}
	return false
}

func containsWorkflowStepNamed(workflow map[string]any, name string) bool {
	steps, ok := workflow["steps"].([]any)
	if !ok {
		return false
	}
	for _, step := range steps {
		item, ok := step.(map[string]any)
		if ok && item["name"] == name {
			return true
		}
	}
	return false
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

func waitForWorkflowStep(t *testing.T, routes http.Handler, id string, stepName string) map[string]any {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("workflow status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var workflow map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &workflow); err != nil {
			t.Fatalf("decode workflow: %v", err)
		}
		if containsWorkflowStepNamed(workflow, stepName) {
			return workflow
		}
		time.Sleep(10 * time.Millisecond)
	}
	workflow := httptest.NewRecorder()
	routes.ServeHTTP(workflow, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id, nil))
	t.Fatalf("workflow did not record step %s: %s", stepName, workflow.Body.String())
	return nil
}

func waitForWorkflow(t *testing.T, routes http.Handler, id string, want string) map[string]any {
	t.Helper()
	for attempt := 0; attempt < 400; attempt++ {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("workflow status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var workflow map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &workflow); err != nil {
			t.Fatalf("decode workflow: %v", err)
		}
		if workflow["status"] == want {
			return workflow
		}
		time.Sleep(10 * time.Millisecond)
	}
	workflow := httptest.NewRecorder()
	routes.ServeHTTP(workflow, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id, nil))
	t.Fatalf("workflow did not reach %s: %s", want, workflow.Body.String())
	return nil
}

func assertJobArtifacts(t *testing.T, value any, expected map[string]string) {
	t.Helper()
	artifacts, ok := value.([]any)
	if !ok {
		t.Fatalf("job artifacts = %#v", value)
	}
	seen := map[string]string{}
	for _, artifactValue := range artifacts {
		artifact, ok := artifactValue.(map[string]any)
		if !ok {
			t.Fatalf("job artifact = %#v", artifactValue)
		}
		name, _ := artifact["name"].(string)
		role, _ := artifact["role"].(string)
		kind, _ := artifact["kind"].(string)
		schema, _ := artifact["schema_version"].(string)
		seen[name] = role + ":" + kind + ":" + schema
	}
	for name, want := range expected {
		if seen[name] != want {
			t.Fatalf("artifact %s = %q, want %q; all artifacts = %#v", name, seen[name], want, artifacts)
		}
	}
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

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
