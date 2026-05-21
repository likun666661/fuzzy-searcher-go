package svc_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/likun666661/youtu-rag-service/internal/config"
	"github.com/likun666661/youtu-rag-service/internal/svc"
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

func TestDatasetSchemaManagementEndpoint(t *testing.T) {
	root := t.TempDir()
	defaultSchema := `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`
	mustWrite(t, filepath.Join(root, "schemas", "default.json"), defaultSchema)
	routes := svc.NewService(config.Config{
		ArtifactRoot:   root,
		DefaultDataset: "demo",
		SchemaRoot:     filepath.Join(root, "schemas"),
		CorpusRoot:     filepath.Join(root, "data"),
		GraphRoot:      filepath.Join(root, "output", "graphs"),
		ChunksRoot:     filepath.Join(root, "output", "chunks"),
		CacheRoot:      filepath.Join(root, "retriever", "faiss_cache_new"),
		GoldenRoot:     filepath.Join(root, "output", "retrieval_golden"),
		TraceRoot:      filepath.Join(root, "output", "retrieval_traces"),
	}).Routes()

	fallback := httptest.NewRecorder()
	routes.ServeHTTP(fallback, httptest.NewRequest(http.MethodGet, "/v1/datasets/news/schema", nil))
	if fallback.Code != http.StatusOK ||
		!strings.Contains(fallback.Body.String(), `"schema_version":"dataset-schema/v1"`) ||
		!strings.Contains(fallback.Body.String(), `"status":"fallback"`) ||
		!strings.Contains(fallback.Body.String(), `"fallback":true`) {
		t.Fatalf("fallback schema status = %d, body = %s", fallback.Code, fallback.Body.String())
	}
	noFallback := httptest.NewRecorder()
	routes.ServeHTTP(noFallback, httptest.NewRequest(http.MethodGet, "/v1/datasets/news/schema?allow_fallback=false", nil))
	if noFallback.Code != http.StatusNotFound || !strings.Contains(noFallback.Body.String(), `"code":"schema_not_found"`) {
		t.Fatalf("no-fallback schema status = %d, body = %s", noFallback.Code, noFallback.Body.String())
	}

	put := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"schema":{"Nodes":["organization"],"Relations":["located_in"],"Attributes":["name"]},"source_path":"/tmp/schema.json"}`)
	routes.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/v1/datasets/news/schema", body))
	if put.Code != http.StatusOK ||
		!strings.Contains(put.Body.String(), `"schema_version":"dataset-schema-update/v1"`) ||
		!strings.Contains(put.Body.String(), `"status":"written"`) ||
		!strings.Contains(put.Body.String(), `"hash"`) ||
		!strings.Contains(put.Body.String(), `"version":1`) ||
		!strings.Contains(put.Body.String(), `"name":"schema_metadata"`) {
		t.Fatalf("put schema status = %d, body = %s", put.Code, put.Body.String())
	}
	managedPath := filepath.Join(root, "schemas", "news.json")
	if _, err := os.Stat(managedPath); err != nil {
		t.Fatalf("managed schema not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "output", "datasets", "news.schema.json")); err != nil {
		t.Fatalf("schema metadata not written: %v", err)
	}
	var original struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(put.Body.Bytes(), &original); err != nil {
		t.Fatalf("decode put schema: %v", err)
	}

	got := httptest.NewRecorder()
	routes.ServeHTTP(got, httptest.NewRequest(http.MethodGet, "/v1/datasets/news/schema", nil))
	if got.Code != http.StatusOK ||
		!strings.Contains(got.Body.String(), `"status":"ready"`) ||
		!strings.Contains(got.Body.String(), `"fallback":false`) ||
		!strings.Contains(got.Body.String(), managedPath) {
		t.Fatalf("get schema status = %d, body = %s", got.Code, got.Body.String())
	}
	metadataOnly := httptest.NewRecorder()
	routes.ServeHTTP(metadataOnly, httptest.NewRequest(http.MethodGet, "/v1/datasets/news/schema?include_body=false", nil))
	if metadataOnly.Code != http.StatusOK ||
		strings.Contains(metadataOnly.Body.String(), `"schema":`) ||
		!strings.Contains(metadataOnly.Body.String(), `"hash"`) {
		t.Fatalf("metadata-only schema status = %d, body = %s", metadataOnly.Code, metadataOnly.Body.String())
	}

	conflict := httptest.NewRecorder()
	routes.ServeHTTP(conflict, httptest.NewRequest(http.MethodPut, "/v1/datasets/news/schema", bytes.NewBufferString(`{"schema":{"Nodes":["event"],"Relations":["related_to"],"Attributes":["name"]}}`)))
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"schema_exists"`) {
		t.Fatalf("conflict status = %d, body = %s", conflict.Code, conflict.Body.String())
	}
	overwrite := httptest.NewRecorder()
	routes.ServeHTTP(overwrite, httptest.NewRequest(http.MethodPut, "/v1/datasets/news/schema", bytes.NewBufferString(`{"overwrite":true,"schema":{"Nodes":["event"],"Relations":["related_to"],"Attributes":["title"]}}`)))
	if overwrite.Code != http.StatusOK ||
		!strings.Contains(overwrite.Body.String(), `"version":2`) ||
		!strings.Contains(overwrite.Body.String(), `"nodes":1`) {
		t.Fatalf("overwrite status = %d, body = %s", overwrite.Code, overwrite.Body.String())
	}
	var overwritten struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(overwrite.Body.Bytes(), &overwritten); err != nil {
		t.Fatalf("decode overwrite: %v", err)
	}
	if overwritten.Hash == "" || overwritten.Hash == original.Hash {
		t.Fatalf("overwrite hash = %q, original = %q", overwritten.Hash, original.Hash)
	}

	validate := httptest.NewRecorder()
	routes.ServeHTTP(validate, httptest.NewRequest(http.MethodPost, "/v1/schemas/validate", bytes.NewBufferString(`{"schema":{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}}`)))
	if validate.Code != http.StatusOK || !strings.Contains(validate.Body.String(), `"schema_version":"schema-validation/v1"`) || !strings.Contains(validate.Body.String(), `"valid":true`) {
		t.Fatalf("validate status = %d, body = %s", validate.Code, validate.Body.String())
	}

	list := httptest.NewRecorder()
	routes.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/schemas", nil))
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `"schema_version":"schema-management-list/v1"`) ||
		!strings.Contains(list.Body.String(), `"dataset":"news"`) {
		t.Fatalf("list schemas status = %d, body = %s", list.Code, list.Body.String())
	}

	bad := httptest.NewRecorder()
	routes.ServeHTTP(bad, httptest.NewRequest(http.MethodPut, "/v1/datasets/bad/schema", bytes.NewBufferString(`{"schema":{"Nodes":"person"}}`)))
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), `"code":"invalid_schema"`) {
		t.Fatalf("bad schema status = %d, body = %s", bad.Code, bad.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "schemas", "bad.json")); !os.IsNotExist(err) {
		t.Fatalf("bad schema should not be written, stat err = %v", err)
	}
	badOverwrite := httptest.NewRecorder()
	routes.ServeHTTP(badOverwrite, httptest.NewRequest(http.MethodPut, "/v1/datasets/news/schema", bytes.NewBufferString(`{"overwrite":true,"schema":{"Nodes":["bad"],"Relations":[],"Attributes":["name"]}}`)))
	if badOverwrite.Code != http.StatusBadRequest || !strings.Contains(badOverwrite.Body.String(), `"code":"invalid_schema"`) {
		t.Fatalf("bad overwrite status = %d, body = %s", badOverwrite.Code, badOverwrite.Body.String())
	}
	afterBadBody, err := os.ReadFile(managedPath)
	if err != nil {
		t.Fatalf("read schema after bad overwrite: %v", err)
	}
	if !strings.Contains(string(afterBadBody), `"event"`) || strings.Contains(string(afterBadBody), `"bad"`) {
		t.Fatalf("bad overwrite changed schema unexpectedly: %s", string(afterBadBody))
	}

	duplicate := httptest.NewRecorder()
	routes.ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/v1/schemas/validate", bytes.NewBufferString(`{"schema":{"Nodes":["person","person"],"Relations":["knows"],"Attributes":["name"]}}`)))
	if duplicate.Code != http.StatusBadRequest || !strings.Contains(duplicate.Body.String(), `"code":"duplicate_schema_item"`) {
		t.Fatalf("duplicate status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}

	ops := httptest.NewRecorder()
	routes.ServeHTTP(ops, httptest.NewRequest(http.MethodGet, "/v1/datasets/news/operations", nil))
	if ops.Code != http.StatusOK ||
		!strings.Contains(ops.Body.String(), `"type":"schema_update"`) ||
		!strings.Contains(ops.Body.String(), `"status":"succeeded"`) ||
		!strings.Contains(ops.Body.String(), `"status":"failed"`) ||
		!strings.Contains(ops.Body.String(), `"name":"schema_metadata"`) {
		t.Fatalf("schema operation status = %d, body = %s", ops.Code, ops.Body.String())
	}

	invalidDataset := httptest.NewRecorder()
	routes.ServeHTTP(invalidDataset, httptest.NewRequest(http.MethodGet, "/v1/datasets/bad$name/schema", nil))
	if invalidDataset.Code != http.StatusBadRequest || !strings.Contains(invalidDataset.Body.String(), `"code":"invalid_dataset"`) {
		t.Fatalf("invalid dataset status = %d, body = %s", invalidDataset.Code, invalidDataset.Body.String())
	}
}

func TestDatasetImportEndpoint(t *testing.T) {
	root := t.TempDir()
	sourceCorpus := filepath.Join(root, "incoming", "corpus.json")
	sourceSchema := filepath.Join(root, "incoming", "schema.json")
	badSchema := filepath.Join(root, "incoming", "bad_schema.json")
	badShapeSchema := filepath.Join(root, "incoming", "bad_shape_schema.json")
	buildScript := filepath.Join(root, "workers", "build_graph.py")
	mustWrite(t, sourceCorpus, `[{"id":"doc1","text":"hello"}]`)
	mustWrite(t, sourceSchema, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
	mustWrite(t, badSchema, `{"entities":`)
	mustWrite(t, badShapeSchema, `{"Nodes":["person"],"Relations":[],"Attributes":["name"]}`)
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
		`"name":"schema_metadata"`,
		`"name":"metadata"`,
	} {
		if !strings.Contains(created.Body.String(), want) {
			t.Fatalf("import body missing %s: %s", want, created.Body.String())
		}
	}
	for _, path := range []string{
		filepath.Join(root, "data", "uploaded", "imported", "corpus.json"),
		filepath.Join(root, "schemas", "imported.json"),
		filepath.Join(root, "output", "datasets", "imported.schema.json"),
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
	badShape := httptest.NewRecorder()
	routes.ServeHTTP(badShape, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"badshape","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(badShapeSchema)+`}`)))
	if badShape.Code != http.StatusBadRequest || !strings.Contains(badShape.Body.String(), "dataset_import_failed") || !strings.Contains(badShape.Body.String(), "Relations must be a non-empty array") {
		t.Fatalf("bad shape schema import status = %d, body = %s", badShape.Code, badShape.Body.String())
	}
	for _, path := range []string{
		filepath.Join(root, "data", "uploaded", "badshape", "corpus.json"),
		filepath.Join(root, "schemas", "badshape.json"),
		filepath.Join(root, "output", "datasets", "badshape.schema.json"),
		filepath.Join(root, "output", "datasets", "badshape.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("bad shape import should not persist %s, stat err = %v", path, err)
		}
	}
}

func TestDatasetDeleteEndpoint(t *testing.T) {
	root := t.TempDir()
	sourceCorpus := filepath.Join(root, "incoming", "corpus.json")
	sourceSchema := filepath.Join(root, "incoming", "schema.json")
	mustWrite(t, sourceCorpus, `[{"id":"doc1","text":"hello"}]`)
	mustWrite(t, sourceSchema, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
	cfg := config.Config{
		ArtifactRoot:    root,
		DefaultDataset:  "demo",
		CorpusRoot:      filepath.Join(root, "data"),
		SchemaRoot:      filepath.Join(root, "schemas"),
		GraphRoot:       filepath.Join(root, "output", "graphs"),
		ChunksRoot:      filepath.Join(root, "output", "chunks"),
		CacheRoot:       filepath.Join(root, "retriever", "faiss_cache_new"),
		GoldenRoot:      filepath.Join(root, "output", "retrieval_golden"),
		TraceRoot:       filepath.Join(root, "output", "retrieval_traces"),
		DatasetMetaRoot: filepath.Join(root, "output", "datasets"),
		DatasetNames:    []string{"demo"},
	}
	routes := svc.NewService(cfg).Routes()
	imported := httptest.NewRecorder()
	routes.ServeHTTP(imported, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"imported","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", imported.Code, imported.Body.String())
	}
	for _, path := range []string{
		filepath.Join(root, "output", "graphs", "imported_new.json"),
		filepath.Join(root, "output", "chunks", "imported.txt"),
		filepath.Join(root, "retriever", "faiss_cache_new", "imported", "index.faiss"),
		filepath.Join(root, "output", "retrieval_golden", "imported.json"),
		filepath.Join(root, "output", "retrieval_traces", "imported_triple_trace.json"),
		filepath.Join(root, "output", "answers", "imported.json"),
		filepath.Join(root, "output", "graphs", "other_new.json"),
	} {
		mustWrite(t, path, "{}")
	}

	dryRun := httptest.NewRecorder()
	routes.ServeHTTP(dryRun, httptest.NewRequest(http.MethodDelete, "/v1/datasets/imported?dry_run=true", nil))
	if dryRun.Code != http.StatusOK ||
		!strings.Contains(dryRun.Body.String(), `"schema_version":"dataset-delete/v1"`) ||
		!strings.Contains(dryRun.Body.String(), `"dry_run":true`) ||
		!strings.Contains(dryRun.Body.String(), `"status":"skipped"`) {
		t.Fatalf("dry-run delete status = %d, body = %s", dryRun.Code, dryRun.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "output", "datasets", "imported.json")); err != nil {
		t.Fatalf("dry-run should not delete metadata: %v", err)
	}

	deleted := httptest.NewRecorder()
	routes.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/v1/datasets/imported", nil))
	if deleted.Code != http.StatusOK ||
		!strings.Contains(deleted.Body.String(), `"status":"deleted"`) ||
		!strings.Contains(deleted.Body.String(), `"name":"corpus"`) ||
		!strings.Contains(deleted.Body.String(), `"name":"answer"`) {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	for _, path := range []string{
		filepath.Join(root, "data", "uploaded", "imported", "corpus.json"),
		filepath.Join(root, "schemas", "imported.json"),
		filepath.Join(root, "output", "datasets", "imported.json"),
		filepath.Join(root, "output", "graphs", "imported_new.json"),
		filepath.Join(root, "output", "chunks", "imported.txt"),
		filepath.Join(root, "retriever", "faiss_cache_new", "imported"),
		filepath.Join(root, "output", "retrieval_golden", "imported.json"),
		filepath.Join(root, "output", "retrieval_traces", "imported_triple_trace.json"),
		filepath.Join(root, "output", "answers", "imported.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected deleted artifact %s, stat err = %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "output", "graphs", "other_new.json")); err != nil {
		t.Fatalf("other dataset graph should remain: %v", err)
	}
	loaded := httptest.NewRecorder()
	svc.NewService(cfg).Routes().ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/datasets/imported", nil))
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), `"status":"empty"`) {
		t.Fatalf("loaded deleted dataset status = %d, body = %s", loaded.Code, loaded.Body.String())
	}

	notManaged := httptest.NewRecorder()
	routes.ServeHTTP(notManaged, httptest.NewRequest(http.MethodDelete, "/v1/datasets/orphan", nil))
	if notManaged.Code != http.StatusNotFound || !strings.Contains(notManaged.Body.String(), "dataset_not_managed") {
		t.Fatalf("not managed delete status = %d, body = %s", notManaged.Code, notManaged.Body.String())
	}
	invalid := httptest.NewRecorder()
	routes.ServeHTTP(invalid, httptest.NewRequest(http.MethodDelete, "/v1/datasets/bad$name", nil))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_dataset") {
		t.Fatalf("invalid delete status = %d, body = %s", invalid.Code, invalid.Body.String())
	}

	scoped := httptest.NewRecorder()
	routes.ServeHTTP(scoped, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"scoped","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if scoped.Code != http.StatusCreated {
		t.Fatalf("scoped import status = %d, body = %s", scoped.Code, scoped.Body.String())
	}
	scopedCore := []string{
		filepath.Join(root, "data", "uploaded", "scoped", "corpus.json"),
		filepath.Join(root, "schemas", "scoped.json"),
		filepath.Join(root, "output", "datasets", "scoped.json"),
	}
	scopedOutputs := []string{
		filepath.Join(root, "output", "graphs", "scoped_new.json"),
		filepath.Join(root, "output", "chunks", "scoped.txt"),
		filepath.Join(root, "retriever", "faiss_cache_new", "scoped", "index.faiss"),
		filepath.Join(root, "output", "retrieval_golden", "scoped.json"),
		filepath.Join(root, "output", "retrieval_traces", "scoped_triple_trace.json"),
		filepath.Join(root, "output", "answers", "scoped.json"),
	}
	for _, path := range scopedOutputs {
		mustWrite(t, path, "{}")
	}
	scopedDelete := httptest.NewRecorder()
	routes.ServeHTTP(scopedDelete, httptest.NewRequest(http.MethodDelete, "/v1/datasets/scoped?include_outputs=false", nil))
	if scopedDelete.Code != http.StatusOK ||
		!strings.Contains(scopedDelete.Body.String(), `"include_outputs":false`) ||
		!strings.Contains(scopedDelete.Body.String(), `"status":"skipped"`) {
		t.Fatalf("scoped delete status = %d, body = %s", scopedDelete.Code, scopedDelete.Body.String())
	}
	for _, path := range scopedCore {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("scoped core artifact should be deleted %s, stat err = %v", path, err)
		}
	}
	for _, path := range scopedOutputs {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("scoped output artifact should remain %s: %v", path, err)
		}
	}

	orphanCorpus := filepath.Join(root, "data", "uploaded", "orphan", "corpus.json")
	mustWrite(t, orphanCorpus, `[]`)
	forced := httptest.NewRecorder()
	routes.ServeHTTP(forced, httptest.NewRequest(http.MethodDelete, "/v1/datasets/orphan?force=true&include_outputs=false", nil))
	if forced.Code != http.StatusOK || !strings.Contains(forced.Body.String(), `"include_outputs":false`) || !strings.Contains(forced.Body.String(), `"status":"skipped"`) {
		t.Fatalf("forced scoped delete status = %d, body = %s", forced.Code, forced.Body.String())
	}
	if _, err := os.Stat(orphanCorpus); !os.IsNotExist(err) {
		t.Fatalf("forced orphan corpus should be deleted, stat err = %v", err)
	}
}

func TestDatasetRebuildEndpoint(t *testing.T) {
	root := t.TempDir()
	sourceCorpus := filepath.Join(root, "incoming", "corpus.json")
	sourceSchema := filepath.Join(root, "incoming", "schema.json")
	buildScript := filepath.Join(root, "workers", "build_graph.py")
	mustWrite(t, sourceCorpus, `[{"id":"doc1","text":"hello"}]`)
	mustWrite(t, sourceSchema, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
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
if dataset != "imported":
    raise SystemExit("unexpected dataset: " + dataset)
if not corpus.endswith("data/uploaded/imported/corpus.json"):
    raise SystemExit("unexpected corpus: " + corpus)
if not schema.endswith("schemas/imported.json"):
    raise SystemExit("unexpected schema: " + schema)
if os.path.exists(graph):
    raise SystemExit("graph output was not cleaned before rebuild")
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([{"rebuilt": True}], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: rebuilt\tChunk: hello\n")
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
	routes := svc.NewService(cfg).Routes()
	imported := httptest.NewRecorder()
	routes.ServeHTTP(imported, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"imported","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", imported.Code, imported.Body.String())
	}
	staleGraph := filepath.Join(root, "output", "graphs", "imported_new.json")
	staleChunks := filepath.Join(root, "output", "chunks", "imported.txt")
	staleCache := filepath.Join(root, "retriever", "faiss_cache_new", "imported", "index.faiss")
	mustWrite(t, staleGraph, "stale")
	mustWrite(t, staleChunks, "stale")
	mustWrite(t, staleCache, "stale")
	mustWrite(t, filepath.Join(root, "output", "retrieval_golden", "imported.json"), `{"schema_version":"retriever-golden/v1"}`)
	mustWrite(t, filepath.Join(root, "output", "retrieval_traces", "imported_triple_trace.json"), `{"schema_version":"triple-trace/v1"}`)
	mustWrite(t, filepath.Join(root, "output", "answers", "imported.json"), `{"schema_version":"answer-output/v1"}`)

	dryRun := httptest.NewRecorder()
	routes.ServeHTTP(dryRun, httptest.NewRequest(http.MethodPost, "/v1/datasets/imported/rebuild", bytes.NewBufferString(`{"dry_run":true}`)))
	if dryRun.Code != http.StatusOK ||
		!strings.Contains(dryRun.Body.String(), `"schema_version":"dataset-rebuild/v1"`) ||
		!strings.Contains(dryRun.Body.String(), `"status":"planned"`) ||
		!strings.Contains(dryRun.Body.String(), `"status":"skipped"`) {
		t.Fatalf("dry-run rebuild status = %d, body = %s", dryRun.Code, dryRun.Body.String())
	}
	if _, err := os.Stat(staleGraph); err != nil {
		t.Fatalf("dry-run should not remove graph: %v", err)
	}
	if _, err := os.Stat(staleChunks); err != nil {
		t.Fatalf("dry-run should not remove chunks: %v", err)
	}
	if _, err := os.Stat(staleCache); err != nil {
		t.Fatalf("dry-run should not remove cache: %v", err)
	}

	conflict := httptest.NewRecorder()
	routes.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/v1/datasets/imported/rebuild", bytes.NewBufferString(`{"overwrite_outputs":false}`)))
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"status":"conflict"`) || !strings.Contains(conflict.Body.String(), `"status":"conflict"`) {
		t.Fatalf("conflict rebuild status = %d, body = %s", conflict.Code, conflict.Body.String())
	}

	submitted := httptest.NewRecorder()
	routes.ServeHTTP(submitted, httptest.NewRequest(http.MethodPost, "/v1/datasets/imported/rebuild", bytes.NewBufferString(`{}`)))
	if submitted.Code != http.StatusAccepted ||
		!strings.Contains(submitted.Body.String(), `"schema_version":"dataset-rebuild/v1"`) ||
		!strings.Contains(submitted.Body.String(), `"status":"submitted"`) ||
		!strings.Contains(submitted.Body.String(), `"job_type":"build_graph"`) {
		t.Fatalf("rebuild status = %d, body = %s", submitted.Code, submitted.Body.String())
	}
	var rebuild struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &rebuild); err != nil || rebuild.JobID == "" {
		t.Fatalf("decode rebuild: id=%q err=%v body=%s", rebuild.JobID, err, submitted.Body.String())
	}
	job := waitForServiceJob(t, routes, rebuild.JobID, "succeeded")
	if !containsArtifactStatus(job, "graph", "written") || !containsArtifactStatus(job, "chunks", "written") {
		t.Fatalf("rebuild job artifacts = %#v", job["artifacts"])
	}
	if _, err := os.Stat(filepath.Join(root, "output", "retrieval_golden", "imported.json")); err != nil {
		t.Fatalf("rebuild should preserve golden fixture: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "output", "retrieval_traces", "imported_triple_trace.json")); err != nil {
		t.Fatalf("rebuild should preserve triple trace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "output", "answers", "imported.json")); err != nil {
		t.Fatalf("rebuild should preserve answer output: %v", err)
	}
	loadedJob := httptest.NewRecorder()
	svc.NewService(cfg).Routes().ServeHTTP(loadedJob, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+rebuild.JobID, nil))
	if loadedJob.Code != http.StatusOK ||
		!strings.Contains(loadedJob.Body.String(), `"type":"build_graph"`) ||
		!strings.Contains(loadedJob.Body.String(), `"status":"written"`) ||
		!strings.Contains(loadedJob.Body.String(), filepath.Join(root, "data", "uploaded", "imported", "corpus.json")) ||
		!strings.Contains(loadedJob.Body.String(), filepath.Join(root, "schemas", "imported.json")) {
		t.Fatalf("restarted rebuild job status = %d, body = %s", loadedJob.Code, loadedJob.Body.String())
	}

	notManaged := httptest.NewRecorder()
	routes.ServeHTTP(notManaged, httptest.NewRequest(http.MethodPost, "/v1/datasets/orphan/rebuild", bytes.NewBufferString(`{}`)))
	if notManaged.Code != http.StatusNotFound || !strings.Contains(notManaged.Body.String(), "dataset_not_managed") {
		t.Fatalf("not managed rebuild status = %d, body = %s", notManaged.Code, notManaged.Body.String())
	}
	invalid := httptest.NewRecorder()
	routes.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/v1/datasets/bad$name/rebuild", bytes.NewBufferString(`{}`)))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_dataset") {
		t.Fatalf("invalid rebuild status = %d, body = %s", invalid.Code, invalid.Body.String())
	}

	missingSchemaImport := httptest.NewRecorder()
	routes.ServeHTTP(missingSchemaImport, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"missing_schema","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if missingSchemaImport.Code != http.StatusCreated {
		t.Fatalf("missing_schema import status = %d, body = %s", missingSchemaImport.Code, missingSchemaImport.Body.String())
	}
	if err := os.Remove(filepath.Join(root, "schemas", "missing_schema.json")); err != nil {
		t.Fatalf("remove managed schema: %v", err)
	}
	missingSchema := httptest.NewRecorder()
	routes.ServeHTTP(missingSchema, httptest.NewRequest(http.MethodPost, "/v1/datasets/missing_schema/rebuild", bytes.NewBufferString(`{}`)))
	if missingSchema.Code != http.StatusConflict || !strings.Contains(missingSchema.Body.String(), "dataset_not_ready") {
		t.Fatalf("missing schema rebuild status = %d, body = %s", missingSchema.Code, missingSchema.Body.String())
	}
}

func TestDatasetOperationHistoryEndpoints(t *testing.T) {
	root := t.TempDir()
	sourceCorpus := filepath.Join(root, "incoming", "corpus.json")
	sourceSchema := filepath.Join(root, "incoming", "schema.json")
	document := filepath.Join(root, "incoming", "doc.txt")
	parseScript := filepath.Join(root, "workers", "parse.py")
	buildScript := filepath.Join(root, "workers", "build.py")
	mustWrite(t, sourceCorpus, `[{"id":"doc1","text":"hello"}]`)
	mustWrite(t, sourceSchema, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
	mustWrite(t, document, "hello")
	mustMkdir(t, filepath.Dir(parseScript))
	writeExecutable(t, parseScript, `#!/usr/bin/env python3
import json
import os
import sys
dataset = sys.argv[sys.argv.index("--dataset") + 1]
output = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump([{"id": dataset + "-doc-1", "text": "hello"}], f)
`)
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
		DatasetOpsRoot:   filepath.Join(root, "output", "dataset_operations"),
		JobRoot:          filepath.Join(root, "jobs"),
		WorkflowRoot:     filepath.Join(root, "workflows"),
		PythonBin:        "python3",
		ParseDocsScript:  parseScript,
		BuildGraphScript: buildScript,
		WorkerCWD:        root,
		DatasetNames:     []string{"demo"},
	}
	routes := svc.NewService(cfg).Routes()

	imported := httptest.NewRecorder()
	routes.ServeHTTP(imported, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"imported","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", imported.Code, imported.Body.String())
	}
	rebuild := httptest.NewRecorder()
	routes.ServeHTTP(rebuild, httptest.NewRequest(http.MethodPost, "/v1/datasets/imported/rebuild", bytes.NewBufferString(`{"dry_run":true}`)))
	if rebuild.Code != http.StatusOK {
		t.Fatalf("rebuild status = %d, body = %s", rebuild.Code, rebuild.Body.String())
	}
	deleted := httptest.NewRecorder()
	routes.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/v1/datasets/imported?dry_run=true", nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	failedImport := httptest.NewRecorder()
	routes.ServeHTTP(failedImport, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"failed_import","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(filepath.Join(root, "incoming", "missing-schema.json"))+`}`)))
	if failedImport.Code != http.StatusBadRequest {
		t.Fatalf("failed import status = %d, body = %s", failedImport.Code, failedImport.Body.String())
	}
	missingSchemaImport := httptest.NewRecorder()
	routes.ServeHTTP(missingSchemaImport, httptest.NewRequest(http.MethodPost, "/v1/datasets/import", bytes.NewBufferString(`{"dataset":"failed_rebuild","corpus_path":`+quote(sourceCorpus)+`,"schema_path":`+quote(sourceSchema)+`}`)))
	if missingSchemaImport.Code != http.StatusCreated {
		t.Fatalf("failed_rebuild import status = %d, body = %s", missingSchemaImport.Code, missingSchemaImport.Body.String())
	}
	if err := os.Remove(filepath.Join(root, "schemas", "failed_rebuild.json")); err != nil {
		t.Fatalf("remove failed_rebuild schema: %v", err)
	}
	failedRebuild := httptest.NewRecorder()
	routes.ServeHTTP(failedRebuild, httptest.NewRequest(http.MethodPost, "/v1/datasets/failed_rebuild/rebuild", bytes.NewBufferString(`{}`)))
	if failedRebuild.Code != http.StatusConflict {
		t.Fatalf("failed rebuild status = %d, body = %s", failedRebuild.Code, failedRebuild.Body.String())
	}
	failedDelete := httptest.NewRecorder()
	routes.ServeHTTP(failedDelete, httptest.NewRequest(http.MethodDelete, "/v1/datasets/not_managed", nil))
	if failedDelete.Code != http.StatusNotFound {
		t.Fatalf("failed delete status = %d, body = %s", failedDelete.Code, failedDelete.Body.String())
	}
	created := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"create_dataset","create_dataset":{"dataset":"created","document_paths":[` + quote(document) + `],"schema_path":` + quote(sourceSchema) + `}}`)
	routes.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/v1/workflows", body))
	if created.Code != http.StatusAccepted {
		t.Fatalf("create_dataset workflow status = %d, body = %s", created.Code, created.Body.String())
	}
	var createdWorkflow struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdWorkflow); err != nil || createdWorkflow.ID == "" {
		t.Fatalf("decode create_dataset workflow: id=%q err=%v body=%s", createdWorkflow.ID, err, created.Body.String())
	}
	_ = waitForWorkflow(t, routes, createdWorkflow.ID, "succeeded")

	all := httptest.NewRecorder()
	routes.ServeHTTP(all, httptest.NewRequest(http.MethodGet, "/v1/dataset-operations", nil))
	if all.Code != http.StatusOK ||
		!strings.Contains(all.Body.String(), `"schema_version":"dataset-operation-list/v1"`) ||
		!strings.Contains(all.Body.String(), `"type":"import"`) ||
		!strings.Contains(all.Body.String(), `"type":"rebuild"`) ||
		!strings.Contains(all.Body.String(), `"type":"delete"`) ||
		!strings.Contains(all.Body.String(), `"type":"create_dataset"`) ||
		!strings.Contains(all.Body.String(), `"dataset":"failed_import"`) ||
		!strings.Contains(all.Body.String(), `"dataset":"failed_rebuild"`) ||
		!strings.Contains(all.Body.String(), `"dataset":"not_managed"`) ||
		!strings.Contains(all.Body.String(), `"status":"failed"`) ||
		!strings.Contains(all.Body.String(), `"workflow_id"`) {
		t.Fatalf("operations list status = %d, body = %s", all.Code, all.Body.String())
	}
	var listed struct {
		Operations []struct {
			ID      string `json:"id"`
			Dataset string `json:"dataset"`
			Type    string `json:"type"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(all.Body.Bytes(), &listed); err != nil || len(listed.Operations) < 4 {
		t.Fatalf("decode operations: count=%d err=%v body=%s", len(listed.Operations), err, all.Body.String())
	}
	detailID := listed.Operations[0].ID
	detail := httptest.NewRecorder()
	routes.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/v1/dataset-operations/"+detailID, nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"schema_version":"dataset-operation/v1"`) {
		t.Fatalf("operation detail status = %d, body = %s", detail.Code, detail.Body.String())
	}
	missingOperation := httptest.NewRecorder()
	routes.ServeHTTP(missingOperation, httptest.NewRequest(http.MethodGet, "/v1/dataset-operations/missing", nil))
	if missingOperation.Code != http.StatusNotFound || !strings.Contains(missingOperation.Body.String(), "dataset_operation_not_found") {
		t.Fatalf("missing operation status = %d, body = %s", missingOperation.Code, missingOperation.Body.String())
	}
	filtered := httptest.NewRecorder()
	routes.ServeHTTP(filtered, httptest.NewRequest(http.MethodGet, "/v1/datasets/imported/operations", nil))
	if filtered.Code != http.StatusOK ||
		!strings.Contains(filtered.Body.String(), `"dataset":"imported"`) ||
		strings.Contains(filtered.Body.String(), `"dataset":"created"`) {
		t.Fatalf("dataset operations status = %d, body = %s", filtered.Code, filtered.Body.String())
	}
	restarted := httptest.NewRecorder()
	svc.NewService(cfg).Routes().ServeHTTP(restarted, httptest.NewRequest(http.MethodGet, "/v1/dataset-operations", nil))
	if restarted.Code != http.StatusOK || !strings.Contains(restarted.Body.String(), `"type":"import"`) {
		t.Fatalf("restarted operations status = %d, body = %s", restarted.Code, restarted.Body.String())
	}
}

func TestParseDocumentsJobLifecycle(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "parse_worker.py")
	document := filepath.Join(dir, "incoming", "doc.txt")
	documentTwo := filepath.Join(dir, "incoming", "doc2.md")
	mustWrite(t, document, "hello from document")
	mustWrite(t, documentTwo, "second document")
	writeExecutable(t, script, `#!/usr/bin/env python3
import json
import os
import sys
dataset = sys.argv[sys.argv.index("--dataset") + 1]
output = sys.argv[sys.argv.index("--output") + 1]
document = sys.argv[sys.argv.index("--document") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump([{"id": dataset + "-doc-1", "text": open(document, encoding="utf-8").read()}], f)
print("parsed ok")
`)
	cfg := config.Config{
		DefaultDataset:  "demo",
		ArtifactRoot:    dir,
		CorpusRoot:      filepath.Join(dir, "data"),
		SchemaRoot:      filepath.Join(dir, "schemas"),
		JobRoot:         filepath.Join(dir, "jobs"),
		PythonBin:       "python3",
		ParseDocsScript: script,
		WorkerCWD:       dir,
		DatasetNames:    []string{"demo"},
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"parse_documents","parse_documents":{"dataset":"imported","document_paths":[` + quote(document) + `,` + quote(documentTwo) + `]}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create parse_documents status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode parse_documents job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "succeeded")
	if job["type"] != "parse_documents" {
		t.Fatalf("job = %#v", job)
	}
	outputPath := filepath.Join(dir, "data", "uploaded", "imported", "corpus.json")
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("parse_documents output missing: %v", err)
	}
	if !containsArtifactStatus(job, "corpus", "written") ||
		!containsArtifactStatus(job, "document_1", "configured") ||
		!containsArtifactStatus(job, "document_2", "configured") ||
		!strings.Contains(fmt.Sprint(job["result"]), "parse-documents-result/v1") {
		t.Fatalf("parse_documents job = %#v", job)
	}
	spec, ok := job["spec"].(map[string]any)
	if !ok || spec["output_path"] != outputPath || spec["script_path"] != script ||
		!strings.Contains(toJSON(t, spec["document_paths"]), documentTwo) {
		t.Fatalf("parse_documents spec = %#v", job["spec"])
	}

	restartedRoutes := svc.NewService(cfg).Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
	if loaded.Code != http.StatusOK ||
		!strings.Contains(loaded.Body.String(), `"type":"parse_documents"`) ||
		!strings.Contains(loaded.Body.String(), `"name":"document_2"`) ||
		!strings.Contains(loaded.Body.String(), `"status":"written"`) {
		t.Fatalf("loaded parse_documents job status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
}

func TestParseDocumentsJobFailureArtifacts(t *testing.T) {
	t.Run("missing output marks corpus missing", func(t *testing.T) {
		dir := t.TempDir()
		document := filepath.Join(dir, "incoming", "doc.txt")
		script := filepath.Join(dir, "missing_parse.py")
		mustWrite(t, document, "hello")
		writeExecutable(t, script, `#!/usr/bin/env python3
print("no corpus written")
`)
		routes := svc.NewService(parseDocumentsTestConfig(dir, script)).Routes()

		job := createParseDocumentsJob(t, routes, document)
		job = waitForServiceJob(t, routes, job["id"].(string), "failed")
		if !containsArtifactStatus(job, "document_1", "configured") ||
			!containsArtifactStatus(job, "corpus", "missing") ||
			!strings.Contains(toJSON(t, job["error"]), "parse documents output missing") {
			t.Fatalf("missing output parse_documents job = %#v", job)
		}
	})

	t.Run("bad output schema marks corpus failed", func(t *testing.T) {
		dir := t.TempDir()
		document := filepath.Join(dir, "incoming", "doc.txt")
		script := filepath.Join(dir, "bad_parse.py")
		mustWrite(t, document, "hello")
		writeExecutable(t, script, `#!/usr/bin/env python3
import os
import sys
output = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
open(output, "w", encoding="utf-8").write("not-json")
`)
		routes := svc.NewService(parseDocumentsTestConfig(dir, script)).Routes()

		job := createParseDocumentsJob(t, routes, document)
		job = waitForServiceJob(t, routes, job["id"].(string), "failed")
		if !containsArtifactStatus(job, "corpus", "failed") ||
			!strings.Contains(toJSON(t, job["error"]), "parse corpus output") {
			t.Fatalf("bad output parse_documents job = %#v", job)
		}
	})

	t.Run("worker failure preserves stderr and marks corpus failed", func(t *testing.T) {
		dir := t.TempDir()
		document := filepath.Join(dir, "incoming", "doc.txt")
		script := filepath.Join(dir, "failed_parse.py")
		mustWrite(t, document, "hello")
		writeExecutable(t, script, `#!/usr/bin/env python3
import sys
print("parse exploded", file=sys.stderr)
raise SystemExit(7)
`)
		routes := svc.NewService(parseDocumentsTestConfig(dir, script)).Routes()

		job := createParseDocumentsJob(t, routes, document)
		job = waitForServiceJob(t, routes, job["id"].(string), "failed")
		if !containsArtifactStatus(job, "document_1", "configured") ||
			!containsArtifactStatus(job, "corpus", "failed") ||
			!strings.Contains(toJSON(t, job["error"]), "parse exploded") {
			t.Fatalf("worker failure parse_documents job = %#v", job)
		}
		events := httptest.NewRecorder()
		routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job["id"].(string)+"/events", nil))
		if events.Code != http.StatusOK ||
			!strings.Contains(events.Body.String(), `"worker_started"`) ||
			!strings.Contains(events.Body.String(), `"failed"`) ||
			!strings.Contains(events.Body.String(), "parse exploded") {
			t.Fatalf("parse_documents failure events status = %d, body = %s", events.Code, events.Body.String())
		}
	})
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
wal = sys.argv[sys.argv.index("--wal") + 1]
assert "--skip-communities" in sys.argv
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
os.makedirs(os.path.dirname(wal), exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
with open(wal, "w", encoding="utf-8") as f:
    f.write("{}\n")
print(json.dumps({
    "schema_version": "build-graph-result/v1",
    "dataset": dataset,
    "graph_output_path": graph,
    "chunks_output_path": chunks,
    "wal_path": wal,
    "total_chunks": 1,
    "succeeded_chunks": 1,
    "skipped_chunks": 0,
    "skip_communities": True,
}))
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
		`"name":"graph_wal"`,
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
		spec["wal_path"] == "" ||
		spec["skip_communities"] != true ||
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
	for _, name := range []string{"graph", "chunks", "graph_wal", "cache"} {
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

func TestBuildGraphJobWALResumeSkipsCompletedChunks(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	corpusPath := filepath.Join(dir, "data", "demo", "demo_corpus.json")
	schemaPath := filepath.Join(dir, "schemas", "demo.json")
	graphPath := filepath.Join(dir, "output", "graphs", "demo_new.json")
	chunksPath := filepath.Join(dir, "output", "chunks", "demo.txt")
	walPath := filepath.Join(dir, "output", "graph_wal", "demo.jsonl")
	mustWrite(t, corpusPath, "[]")
	mustWrite(t, schemaPath, "{}")
	mustWrite(t, walPath, `{"schema_version":"graph-build-wal/v1","event":"chunk_succeeded","status":"succeeded","chunk_key":"0:0:abc","chunk_id":"c1","chunk_text":"hello","extraction":{"triples":[]}}`+"\n"+
		`{"schema_version":"graph-build-wal/v1","event":"chunk_succeeded","status":"succeeded","chunk_key":"0:1:def","chunk_id":"c2","chunk_text":"world","extraction":{"triples":[]}}`+"\n")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
wal = sys.argv[sys.argv.index("--wal") + 1]
assert "--resume" in sys.argv
assert "--skip-communities" in sys.argv
assert sys.argv[sys.argv.index("--max-workers") + 1] == "2"
assert sys.argv[sys.argv.index("--runner-count") + 1] == "3"
assert sys.argv[sys.argv.index("--llm-rate-limit-rpm") + 1] == "120"
assert sys.argv[sys.argv.index("--llm-max-attempts") + 1] == "3"
assert sys.argv[sys.argv.index("--llm-retry-base-seconds") + 1] == "2"
assert sys.argv[sys.argv.index("--llm-retry-max-seconds") + 1] == "30"
with open(wal, "r", encoding="utf-8") as f:
    succeeded = [line for line in f if '"chunk_succeeded"' in line]
if len(succeeded) != 2:
    raise SystemExit("resume did not see completed chunks")
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([{"id": "n1"}], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
    f.write("id: c2\tChunk: world\n")
with open(wal, "a", encoding="utf-8") as f:
    f.write(json.dumps({"schema_version":"graph-build-wal-compact/v1","event":"compacted","status":"succeeded"}) + "\n")
print(json.dumps({
    "schema_version": "build-graph-result/v1",
    "dataset": "demo",
    "graph_output_path": graph,
    "chunks_output_path": chunks,
    "wal_path": wal,
    "total_chunks": 2,
    "succeeded_chunks": 2,
    "skipped_chunks": 2,
    "runner_count": 3,
    "llm_rate_limit_rpm": 120,
    "llm_max_attempts": 3,
    "skip_communities": True,
}))
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

	body := bytes.NewBufferString(`{"type":"build_graph","build_graph":{"dataset":"demo","wal_path":` + quote(walPath) + `,"resume":true,"max_workers":2,"runner_count":3,"llm_rate_limit_rpm":120,"skip_communities":true}}`)
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create build_graph status = %d, body = %s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created build_graph job: %v", err)
	}
	spec, _ := created["spec"].(map[string]any)
	if spec["wal_path"] != walPath || spec["resume"] != true ||
		spec["max_workers"].(float64) != 2 || spec["runner_count"].(float64) != 3 ||
		spec["llm_rate_limit_rpm"].(float64) != 120 || spec["llm_rate_limit_file"] == "" ||
		spec["llm_max_attempts"].(float64) != 3 ||
		spec["llm_retry_base_seconds"].(float64) != 2 ||
		spec["llm_retry_max_seconds"].(float64) != 30 ||
		spec["skip_communities"] != true {
		t.Fatalf("created build_graph spec = %#v", spec)
	}

	job := waitForServiceJob(t, routes, created["id"].(string), "succeeded")
	result, _ := job["result"].(map[string]any)
	if result["graph_output_path"] != graphPath || result["chunks_output_path"] != chunksPath ||
		result["wal_path"] != walPath || result["skipped_chunks"].(float64) != 2 ||
		result["succeeded_chunks"].(float64) != 2 || result["runner_count"].(float64) != 3 ||
		result["llm_rate_limit_rpm"].(float64) != 120 ||
		result["llm_max_attempts"].(float64) != 3 ||
		result["skip_communities"] != true {
		t.Fatalf("build_graph result = %#v", result)
	}
	if !containsArtifactStatus(job, "graph_wal", "written") ||
		!containsArtifactStatus(job, "graph", "written") ||
		!containsArtifactStatus(job, "chunks", "written") {
		t.Fatalf("build_graph artifacts = %#v", job["artifacts"])
	}
	walBody, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	if !strings.Contains(string(walBody), `"graph-build-wal-compact/v1"`) {
		t.Fatalf("wal missing compact record: %s", walBody)
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string), nil))
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), `"skipped_chunks":2`) ||
		!strings.Contains(loaded.Body.String(), `"name":"graph_wal"`) ||
		!strings.Contains(loaded.Body.String(), `"status":"written"`) {
		t.Fatalf("reloaded build_graph job status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
}

func TestBuildGraphJobInvalidWALMarksArtifactsFailed(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	corpusPath := filepath.Join(dir, "data", "demo", "demo_corpus.json")
	schemaPath := filepath.Join(dir, "schemas", "demo.json")
	walPath := filepath.Join(dir, "output", "graph_wal", "demo.jsonl")
	mustWrite(t, corpusPath, "[]")
	mustWrite(t, schemaPath, "{}")
	mustWrite(t, walPath, "{bad json\n")
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import sys
wal = sys.argv[sys.argv.index("--wal") + 1]
with open(wal, "r", encoding="utf-8") as f:
    f.read()
print("graph_build_wal_invalid: line 1", file=sys.stderr)
raise SystemExit(2)
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

	body := bytes.NewBufferString(`{"type":"build_graph","build_graph":{"dataset":"demo","wal_path":` + quote(walPath) + `,"resume":true}}`)
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create build_graph status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created build_graph job: %v", err)
	}
	job := waitForServiceJob(t, routes, created.ID, "failed")
	if errorText, _ := job["error"].(string); !strings.Contains(errorText, "graph_build_wal_invalid") {
		t.Fatalf("job error = %#v", job["error"])
	}
	if !containsArtifactStatus(job, "graph_wal", "failed") ||
		!containsArtifactStatus(job, "graph", "failed") ||
		!containsArtifactStatus(job, "chunks", "failed") {
		t.Fatalf("build_graph artifacts = %#v", job["artifacts"])
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), "graph_build_wal_invalid") {
		t.Fatalf("build_graph events status = %d, body = %s", events.Code, events.Body.String())
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

func TestBenchmarkJobEndpoint(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	outputPath := filepath.Join(dir, "output", "benchmarks", "anony_eng.json")
	qaPath := filepath.Join(dir, "data", "anony_eng", "final_qa_pairs.json")
	mustWrite(t, qaPath, `[{"question":"Q?","answer":"A"}]`)
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys

def value(flag):
    return sys.argv[sys.argv.index(flag) + 1]

dataset = value("--dataset")
qa = value("--qa")
out = value("--output")
assert value("--answer-model") == "deepseek-v4-pro"
assert value("--retrieve-url") == "http://127.0.0.1:18082"
assert value("--retrieve-mode") == "native-path1-rerank"
assert value("--sidecar-url") == "http://127.0.0.1:18765"
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    json.dump({
        "schema_version": "benchmark-result/v1",
        "dataset": dataset,
        "qa_path": qa,
        "question_count": 1,
        "correct_count": 1,
        "accuracy": 1.0,
        "items": [{"id": "qa_1", "judge": "1", "correct": True}],
    }, f)
print("benchmark ok")
`)
	service := svc.NewService(config.Config{
		DefaultDataset:  "demo",
		DefaultMode:     "noagent",
		ArtifactRoot:    dir,
		CorpusRoot:      filepath.Join(dir, "data"),
		SchemaRoot:      filepath.Join(dir, "schemas"),
		GraphRoot:       filepath.Join(dir, "output", "graphs"),
		ChunksRoot:      filepath.Join(dir, "output", "chunks"),
		CacheRoot:       filepath.Join(dir, "retriever", "faiss_cache_new"),
		JobRoot:         filepath.Join(dir, "jobs"),
		PythonBin:       "python3",
		BenchmarkScript: scriptPath,
		WorkerCWD:       dir,
		DefaultSidecar:  "http://127.0.0.1:18765",
		DatasetNames:    []string{"demo"},
	})
	routes := service.Routes()

	body := bytes.NewBufferString(`{"type":"benchmark","benchmark":{"dataset":"anony_eng","qa_path":` + quote(qaPath) + `,"output_path":` + quote(outputPath) + `,"limit":1,"retrieve_url":"http://127.0.0.1:18082","retrieve_mode":"native-path1-rerank","answer_model":"deepseek-v4-pro","judge_model":"deepseek-v4-pro","llm_base_url":"https://api.deepseek.com"}}`)
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create benchmark status = %d, body = %s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created benchmark job: %v", err)
	}
	spec, _ := created["spec"].(map[string]any)
	if created["type"] != "benchmark" || spec["script_path"] != scriptPath ||
		spec["qa_path"] != qaPath || spec["output_path"] != outputPath ||
		spec["retrieve_url"] != "http://127.0.0.1:18082" ||
		spec["retrieve_mode"] != "native-path1-rerank" {
		t.Fatalf("created benchmark job = %#v", created)
	}

	job := waitForServiceJob(t, routes, created["id"].(string), "succeeded")
	result, _ := job["result"].(map[string]any)
	if result["schema_version"] != "benchmark-job-result/v1" || result["dataset"] != "anony_eng" ||
		result["question_count"].(float64) != 1 || result["accuracy"].(float64) != 1.0 {
		t.Fatalf("benchmark result = %#v", result)
	}
	if !containsArtifactStatus(job, "qa", "configured") ||
		!containsArtifactStatus(job, "benchmark_result", "written") {
		t.Fatalf("benchmark artifacts = %#v", job["artifacts"])
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("benchmark output missing: %v", err)
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string)+"/events", nil))
	if !strings.Contains(events.Body.String(), `"worker_started"`) ||
		!strings.Contains(events.Body.String(), `"artifact_benchmark_written"`) {
		t.Fatalf("benchmark events = %s", events.Body.String())
	}
}

func TestBenchmarkJobProgressCheckpointAndConcurrency(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	outputPath := filepath.Join(dir, "output", "benchmarks", "anony_eng.json")
	progressPath := filepath.Join(dir, "output", "benchmarks", "anony_eng.progress.json")
	checkpointPath := filepath.Join(dir, "output", "benchmarks", "anony_eng.checkpoint.jsonl")
	qaPath := filepath.Join(dir, "data", "anony_eng", "final_qa_pairs.json")
	jobRoot := filepath.Join(dir, "jobs")
	mustWrite(t, qaPath, `[{"id":"qa_1","question":"Q?","answer":"A"}]`)
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys

def value(flag):
    return sys.argv[sys.argv.index(flag) + 1]

dataset = value("--dataset")
out = value("--output")
progress = value("--progress")
checkpoint = value("--checkpoint")
concurrency = int(value("--concurrency"))
assert concurrency == 3
assert value("--checkpoint-every") == "1"
os.makedirs(os.path.dirname(out), exist_ok=True)
payload = {
    "schema_version": "benchmark-progress/v1",
    "dataset": dataset,
    "status": "succeeded",
    "total": 1,
    "completed": 1,
    "running": 0,
    "correct_count": 1,
    "failed_count": 0,
    "accuracy_so_far": 1.0,
    "concurrency": concurrency,
    "checkpoint_path": checkpoint,
}
with open(progress, "w", encoding="utf-8") as f:
    json.dump(payload, f)
with open(checkpoint, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "benchmark-checkpoint-item/v1", "id": "qa_1", "judge": "1", "correct": True}, f)
    f.write("\n")
with open(out, "w", encoding="utf-8") as f:
    json.dump({
        "schema_version": "benchmark-result/v1",
        "dataset": dataset,
        "question_count": 1,
        "correct_count": 1,
        "accuracy": 1.0,
        "items": [{"id": "qa_1", "judge": "1", "correct": True}],
    }, f)
`)
	cfg := config.Config{
		DefaultDataset:  "demo",
		DefaultMode:     "noagent",
		ArtifactRoot:    dir,
		CorpusRoot:      filepath.Join(dir, "data"),
		JobRoot:         jobRoot,
		PythonBin:       "python3",
		BenchmarkScript: scriptPath,
		WorkerCWD:       dir,
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	body := bytes.NewBufferString(`{"type":"benchmark","benchmark":{"dataset":"anony_eng","qa_path":` + quote(qaPath) + `,"output_path":` + quote(outputPath) + `,"progress_path":` + quote(progressPath) + `,"checkpoint_path":` + quote(checkpointPath) + `,"limit":1,"concurrency":3,"checkpoint_every":1}}`)
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create benchmark status = %d, body = %s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created benchmark job: %v", err)
	}
	spec, _ := created["spec"].(map[string]any)
	if spec["progress_path"] != progressPath || spec["checkpoint_path"] != checkpointPath ||
		spec["concurrency"].(float64) != 3 {
		t.Fatalf("created benchmark spec = %#v", spec)
	}

	job := waitForServiceJob(t, routes, created["id"].(string), "succeeded")
	result, _ := job["result"].(map[string]any)
	if result["progress_path"] != progressPath || result["accuracy"].(float64) != 1.0 {
		t.Fatalf("benchmark result = %#v", result)
	}
	for _, name := range []string{"benchmark_result", "benchmark_progress", "benchmark_checkpoint"} {
		if !containsArtifactStatus(job, name, "written") {
			t.Fatalf("benchmark artifacts missing %s written: %#v", name, job["artifacts"])
		}
	}
	for _, path := range []string{outputPath, progressPath, checkpointPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected benchmark artifact missing %s: %v", path, err)
		}
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK ||
		!strings.Contains(events.Body.String(), `"benchmark_progress"`) ||
		!strings.Contains(events.Body.String(), `concurrency`) {
		t.Fatalf("benchmark events status = %d, body = %s", events.Code, events.Body.String())
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	get := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string), nil))
	if get.Code != http.StatusOK {
		t.Fatalf("reloaded benchmark job status = %d, body = %s", get.Code, get.Body.String())
	}
	var reloaded map[string]any
	if err := json.Unmarshal(get.Body.Bytes(), &reloaded); err != nil {
		t.Fatalf("decode reloaded benchmark job: %v", err)
	}
	if !containsArtifactStatus(reloaded, "benchmark_progress", "written") ||
		!containsArtifactStatus(reloaded, "benchmark_checkpoint", "written") {
		t.Fatalf("reloaded benchmark artifacts = %#v", reloaded["artifacts"])
	}
	reloadedEvents := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(reloadedEvents, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string)+"/events", nil))
	if reloadedEvents.Code != http.StatusOK || !strings.Contains(reloadedEvents.Body.String(), `"benchmark_progress"`) {
		t.Fatalf("reloaded benchmark events status = %d, body = %s", reloadedEvents.Code, reloadedEvents.Body.String())
	}
}

func TestBenchmarkWorkflowEndpoint(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	qaPath := filepath.Join(dir, "data", "anony_eng", "final_qa_pairs.json")
	mustWrite(t, qaPath, `[{"question":"Q?","answer":"A"}]`)
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys
out = sys.argv[sys.argv.index("--output") + 1]
dataset = sys.argv[sys.argv.index("--dataset") + 1]
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    json.dump({
        "schema_version": "benchmark-result/v1",
        "dataset": dataset,
        "question_count": 2,
        "correct_count": 1,
        "accuracy": 0.5,
        "items": [{"id": "qa_1"}, {"id": "qa_2"}],
    }, f)
`)
	service := svc.NewService(config.Config{
		DefaultDataset:  "demo",
		DefaultMode:     "noagent",
		ArtifactRoot:    dir,
		CorpusRoot:      filepath.Join(dir, "data"),
		SchemaRoot:      filepath.Join(dir, "schemas"),
		GraphRoot:       filepath.Join(dir, "output", "graphs"),
		ChunksRoot:      filepath.Join(dir, "output", "chunks"),
		CacheRoot:       filepath.Join(dir, "retriever", "faiss_cache_new"),
		JobRoot:         filepath.Join(dir, "jobs"),
		WorkflowRoot:    filepath.Join(dir, "workflows"),
		PythonBin:       "python3",
		BenchmarkScript: scriptPath,
		WorkerCWD:       dir,
		DatasetNames:    []string{"demo"},
	})
	routes := service.Routes()

	body := bytes.NewBufferString(`{"type":"benchmark","benchmark":{"dataset":"anony_eng","qa_path":` + quote(qaPath) + `,"limit":2}}`)
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/workflows", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create benchmark workflow status = %d, body = %s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created benchmark workflow: %v", err)
	}
	workflow := waitForWorkflow(t, routes, created["id"].(string), "succeeded")
	if !containsWorkflowStep(workflow, "benchmark", "benchmark", "succeeded") ||
		!containsArtifactStatus(workflow, "benchmark_result", "written") {
		t.Fatalf("benchmark workflow = %#v", workflow)
	}
	result, _ := workflow["result"].(map[string]any)
	if result["schema_version"] != "benchmark-workflow-result/v1" ||
		result["benchmark_job_id"] == "" ||
		result["question_count"].(float64) != 2 ||
		result["accuracy"].(float64) != 0.5 {
		t.Fatalf("benchmark workflow result = %#v", result)
	}
}

func TestBenchmarkJobFailureArtifacts(t *testing.T) {
	cases := []struct {
		name           string
		script         string
		wantStatus     string
		wantErr        string
		wantEventError string
	}{
		{
			name: "missing output",
			script: `#!/usr/bin/env python3
print("ok but did not write output")
`,
			wantStatus: "missing",
			wantErr:    "benchmark output missing",
		},
		{
			name: "bad schema",
			script: `#!/usr/bin/env python3
import json
import os
import sys
out = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    json.dump({"schema_version": "wrong/v1", "items": []}, f)
`,
			wantStatus: "failed",
			wantErr:    `unexpected benchmark schema_version "wrong/v1"`,
		},
		{
			name: "worker failure",
			script: `#!/usr/bin/env python3
import sys
print("deepseek key missing", file=sys.stderr)
raise SystemExit(3)
`,
			wantStatus:     "failed",
			wantErr:        "deepseek key missing",
			wantEventError: "deepseek key missing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			scriptPath := filepath.Join(dir, "worker.py")
			outputPath := filepath.Join(dir, "output", "benchmarks", "anony_eng.json")
			qaPath := filepath.Join(dir, "data", "anony_eng", "final_qa_pairs.json")
			mustWrite(t, qaPath, `[{"question":"Q?","answer":"A"}]`)
			writeExecutable(t, scriptPath, tc.script)

			service := svc.NewService(config.Config{
				DefaultDataset:  "demo",
				ArtifactRoot:    dir,
				CorpusRoot:      filepath.Join(dir, "data"),
				JobRoot:         filepath.Join(dir, "jobs"),
				PythonBin:       "python3",
				BenchmarkScript: scriptPath,
				WorkerCWD:       dir,
			})
			routes := service.Routes()

			body := bytes.NewBufferString(`{"type":"benchmark","benchmark":{"dataset":"anony_eng","qa_path":` + quote(qaPath) + `,"output_path":` + quote(outputPath) + `,"limit":1}}`)
			create := httptest.NewRecorder()
			routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
			if create.Code != http.StatusAccepted {
				t.Fatalf("create benchmark status = %d, body = %s", create.Code, create.Body.String())
			}
			var created map[string]any
			if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
				t.Fatalf("decode created benchmark job: %v", err)
			}
			job := waitForServiceJob(t, routes, created["id"].(string), "failed")
			if errorText, _ := job["error"].(string); !strings.Contains(errorText, tc.wantErr) {
				t.Fatalf("job error = %#v, want %q", job["error"], tc.wantErr)
			}
			if !containsArtifactStatus(job, "benchmark_result", tc.wantStatus) {
				t.Fatalf("benchmark artifacts = %#v", job["artifacts"])
			}
			events := httptest.NewRecorder()
			routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string)+"/events", nil))
			if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"failed"`) {
				t.Fatalf("benchmark events status = %d, body = %s", events.Code, events.Body.String())
			}
			if tc.wantEventError != "" && !strings.Contains(events.Body.String(), tc.wantEventError) {
				t.Fatalf("benchmark events missing %q: %s", tc.wantEventError, events.Body.String())
			}
		})
	}
}

func TestBenchmarkJobPersistsAcrossServiceRestart(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.py")
	outputPath := filepath.Join(dir, "output", "benchmarks", "anony_eng.json")
	qaPath := filepath.Join(dir, "data", "anony_eng", "final_qa_pairs.json")
	jobRoot := filepath.Join(dir, "jobs")
	mustWrite(t, qaPath, `[{"question":"Q?","answer":"A"}]`)
	writeExecutable(t, scriptPath, `#!/usr/bin/env python3
import json
import os
import sys
out = sys.argv[sys.argv.index("--output") + 1]
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    json.dump({
        "schema_version": "benchmark-result/v1",
        "dataset": "anony_eng",
        "question_count": 1,
        "correct_count": 1,
        "accuracy": 1.0,
        "items": [{"id": "qa_1", "judge": "1"}],
    }, f)
`)
	cfg := config.Config{
		DefaultDataset:  "demo",
		ArtifactRoot:    dir,
		CorpusRoot:      filepath.Join(dir, "data"),
		JobRoot:         jobRoot,
		PythonBin:       "python3",
		BenchmarkScript: scriptPath,
		WorkerCWD:       dir,
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	body := bytes.NewBufferString(`{"type":"benchmark","benchmark":{"dataset":"anony_eng","qa_path":` + quote(qaPath) + `,"output_path":` + quote(outputPath) + `,"limit":1}}`)
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create benchmark status = %d, body = %s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created benchmark job: %v", err)
	}
	_ = waitForServiceJob(t, routes, created["id"].(string), "succeeded")
	if _, err := os.Stat(filepath.Join(jobRoot, created["id"].(string)+".json")); err != nil {
		t.Fatalf("persisted benchmark job missing: %v", err)
	}

	restarted := svc.NewService(cfg)
	restartedRoutes := restarted.Routes()
	get := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string), nil))
	if get.Code != http.StatusOK {
		t.Fatalf("reloaded benchmark job status = %d, body = %s", get.Code, get.Body.String())
	}
	var job map[string]any
	if err := json.Unmarshal(get.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode reloaded benchmark job: %v", err)
	}
	result, _ := job["result"].(map[string]any)
	if job["schema_version"] != "service-job/v1" || job["type"] != "benchmark" ||
		job["status"] != "succeeded" || result["schema_version"] != "benchmark-job-result/v1" ||
		result["accuracy"].(float64) != 1.0 {
		t.Fatalf("reloaded benchmark job = %#v", job)
	}
	if !containsArtifactStatus(job, "benchmark_result", "written") {
		t.Fatalf("reloaded benchmark artifacts = %#v", job["artifacts"])
	}
	events := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"artifact_benchmark_written"`) {
		t.Fatalf("reloaded benchmark events status = %d, body = %s", events.Code, events.Body.String())
	}
}

func TestBenchmarkWorkflowBuildFirstHandoff(t *testing.T) {
	dir := t.TempDir()
	buildScript := filepath.Join(dir, "build_worker.py")
	benchmarkScript := filepath.Join(dir, "benchmark_worker.py")
	corpusPath := filepath.Join(dir, "data", "anony_eng", "final_chunk_corpus.json")
	qaPath := filepath.Join(dir, "data", "anony_eng", "final_qa_pairs.json")
	schemaPath := filepath.Join(dir, "schemas", "anony_eng.json")
	mustWrite(t, corpusPath, `[{"id":"doc1","text":"hello"}]`)
	mustWrite(t, qaPath, `[{"question":"Q?","answer":"A"}]`)
	mustWrite(t, schemaPath, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
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
	writeExecutable(t, benchmarkScript, `#!/usr/bin/env python3
import json
import os
import sys

def value(flag):
    return sys.argv[sys.argv.index(flag) + 1]

out = value("--output")
graph = value("--graph")
chunks = value("--chunks")
cache = value("--cache-dir")
assert os.path.exists(graph), graph
assert os.path.exists(chunks), chunks
assert os.path.isdir(cache), cache
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8") as f:
    json.dump({
        "schema_version": "benchmark-result/v1",
        "dataset": value("--dataset"),
        "question_count": 2,
        "correct_count": 1,
        "accuracy": 0.5,
        "items": [{"id": "qa_1"}, {"id": "qa_2"}],
    }, f)
`)
	service := svc.NewService(config.Config{
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
		BenchmarkScript:  benchmarkScript,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	})
	routes := service.Routes()

	body := bytes.NewBufferString(`{"type":"benchmark","benchmark":{"dataset":"anony_eng","qa_path":` + quote(qaPath) + `,"corpus_path":` + quote(corpusPath) + `,"schema_path":` + quote(schemaPath) + `,"build_first":true,"limit":2}}`)
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/workflows", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create benchmark workflow status = %d, body = %s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode benchmark workflow: %v", err)
	}
	workflow := waitForWorkflow(t, routes, created["id"].(string), "succeeded")
	if !containsWorkflowStep(workflow, "build_graph", "build_graph", "succeeded") ||
		!containsWorkflowStep(workflow, "benchmark", "benchmark", "succeeded") {
		t.Fatalf("benchmark build-first workflow steps = %#v", workflow["steps"])
	}
	if !containsWorkflowStepOutputArtifact(workflow, "build_graph", "graph", "written") ||
		!containsWorkflowStepOutputArtifact(workflow, "build_graph", "chunks", "written") ||
		!containsWorkflowStepOutputArtifact(workflow, "benchmark", "benchmark_result", "written") {
		t.Fatalf("benchmark build-first workflow artifacts = %#v", workflow["steps"])
	}
	result, _ := workflow["result"].(map[string]any)
	if result["schema_version"] != "benchmark-workflow-result/v1" ||
		result["build_graph_job_id"] == "" ||
		result["benchmark_job_id"] == "" ||
		result["accuracy"].(float64) != 0.5 {
		t.Fatalf("benchmark build-first result = %#v", result)
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+created["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"artifact_handoff"`) {
		t.Fatalf("benchmark workflow events status = %d, body = %s", events.Code, events.Body.String())
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

func TestCreateDatasetWorkflowLifecycle(t *testing.T) {
	dir := t.TempDir()
	parseScript := filepath.Join(dir, "parse_worker.py")
	buildScript := filepath.Join(dir, "build_worker.py")
	document := filepath.Join(dir, "incoming", "doc.txt")
	schemaPath := filepath.Join(dir, "incoming", "schema.json")
	mustWrite(t, document, "hello dataset")
	mustWrite(t, schemaPath, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
	writeExecutable(t, parseScript, `#!/usr/bin/env python3
import json
import os
import sys
dataset = sys.argv[sys.argv.index("--dataset") + 1]
output = sys.argv[sys.argv.index("--output") + 1]
document = sys.argv[sys.argv.index("--document") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump([{"id": dataset + "-doc-1", "text": open(document, encoding="utf-8").read()}], f)
`)
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import json
import os
import sys
corpus = sys.argv[sys.argv.index("--corpus") + 1]
schema = sys.argv[sys.argv.index("--schema") + 1]
graph = sys.argv[sys.argv.index("--graph-output") + 1]
chunks = sys.argv[sys.argv.index("--chunks-output") + 1]
cache = sys.argv[sys.argv.index("--cache-dir") + 1]
if "data/uploaded/imported/corpus.json" not in corpus:
    raise SystemExit("unexpected corpus path: " + corpus)
if "schemas/imported.json" not in schema:
    raise SystemExit("unexpected schema path: " + schema)
os.makedirs(os.path.dirname(graph), exist_ok=True)
os.makedirs(os.path.dirname(chunks), exist_ok=True)
os.makedirs(cache, exist_ok=True)
with open(graph, "w", encoding="utf-8") as f:
    json.dump([], f)
with open(chunks, "w", encoding="utf-8") as f:
    f.write("id: c1\tChunk: hello\n")
`)
	cfg := config.Config{
		DefaultDataset:   "demo",
		ArtifactRoot:     dir,
		CorpusRoot:       filepath.Join(dir, "data"),
		SchemaRoot:       filepath.Join(dir, "schemas"),
		GraphRoot:        filepath.Join(dir, "output", "graphs"),
		ChunksRoot:       filepath.Join(dir, "output", "chunks"),
		CacheRoot:        filepath.Join(dir, "retriever", "faiss_cache_new"),
		DatasetMetaRoot:  filepath.Join(dir, "output", "datasets"),
		JobRoot:          filepath.Join(dir, "jobs"),
		WorkflowRoot:     filepath.Join(dir, "workflows"),
		PythonBin:        "python3",
		ParseDocsScript:  parseScript,
		BuildGraphScript: buildScript,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	}
	service := svc.NewService(cfg)
	routes := service.Routes()

	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"create_dataset","create_dataset":{"dataset":"imported","document_paths":[` + quote(document) + `],"schema_path":` + quote(schemaPath) + `}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/workflows", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create workflow status = %d, body = %s", create.Code, create.Body.String())
	}
	for _, want := range []string{
		`"type":"create_dataset"`,
		`"name":"document_1"`,
		`"name":"schema_source"`,
		`"name":"parsed_corpus"`,
		`"name":"corpus"`,
		`"name":"schema"`,
		`"name":"metadata"`,
		`"name":"graph"`,
		`"name":"chunks"`,
		`"name":"cache"`,
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
	if !containsWorkflowStep(workflow, "parse_documents", "parse_documents", "succeeded") ||
		!containsWorkflowStep(workflow, "dataset_import", "dataset_import", "succeeded") ||
		!containsWorkflowStep(workflow, "build_graph", "build_graph", "succeeded") {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	for _, name := range []string{"parsed_corpus", "corpus", "schema", "metadata", "graph", "chunks", "cache"} {
		if !containsArtifactStatus(workflow, name, "written") {
			t.Fatalf("workflow artifact %s not written: %#v", name, workflow["artifacts"])
		}
	}
	result, ok := workflow["result"].(map[string]any)
	if !ok || result["schema_version"] != "create-dataset-result/v1" {
		t.Fatalf("workflow result = %#v", workflow["result"])
	}
	expectedPaths := []string{
		filepath.Join(dir, "data", "uploaded", "imported", "corpus.json"),
		filepath.Join(dir, "schemas", "imported.json"),
		filepath.Join(dir, "output", "datasets", "imported.json"),
		filepath.Join(dir, "output", "graphs", "imported_new.json"),
		filepath.Join(dir, "output", "chunks", "imported.txt"),
		filepath.Join(dir, "retriever", "faiss_cache_new", "imported"),
	}
	for _, path := range expectedPaths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected workflow artifact %s: %v", path, err)
		}
	}

	restartedRoutes := svc.NewService(cfg).Routes()
	loaded := httptest.NewRecorder()
	restartedRoutes.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+created.ID, nil))
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), `"type":"create_dataset"`) || !strings.Contains(loaded.Body.String(), `"status":"written"`) {
		t.Fatalf("loaded workflow status = %d, body = %s", loaded.Code, loaded.Body.String())
	}
}

func TestCreateDatasetWorkflowParseFailureStopsImportAndBuild(t *testing.T) {
	dir := t.TempDir()
	parseScript := filepath.Join(dir, "parse_worker.py")
	buildScript := filepath.Join(dir, "build_worker.py")
	document := filepath.Join(dir, "incoming", "doc.txt")
	schemaPath := filepath.Join(dir, "incoming", "schema.json")
	buildMarker := filepath.Join(dir, "build-called")
	mustWrite(t, document, "hello dataset")
	mustWrite(t, schemaPath, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
	writeExecutable(t, parseScript, `#!/usr/bin/env python3
import sys
print("parse exploded", file=sys.stderr)
raise SystemExit(8)
`)
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import pathlib
pathlib.Path("`+buildMarker+`").write_text("called", encoding="utf-8")
`)
	routes := svc.NewService(createDatasetTestConfig(dir, parseScript, buildScript)).Routes()

	workflow := createDatasetWorkflow(t, routes, "imported", []string{document}, schemaPath)
	workflow = waitForWorkflow(t, routes, workflow["id"].(string), "failed")
	if errorText, _ := workflow["error"].(string); !strings.Contains(errorText, "parse_documents step failed") || !strings.Contains(errorText, "parse exploded") {
		t.Fatalf("workflow error = %#v", workflow["error"])
	}
	if !containsWorkflowStep(workflow, "parse_documents", "parse_documents", "failed") {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	if containsWorkflowStepNamed(workflow, "dataset_import") || containsWorkflowStepNamed(workflow, "build_graph") {
		t.Fatalf("workflow should stop after parse failure: %#v", workflow["steps"])
	}
	if _, err := os.Stat(buildMarker); !os.IsNotExist(err) {
		t.Fatalf("build worker should not have been called; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "output", "datasets", "imported.json")); !os.IsNotExist(err) {
		t.Fatalf("dataset metadata should not exist after parse failure, stat err = %v", err)
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK ||
		!strings.Contains(events.Body.String(), `"step_failed"`) ||
		strings.Contains(events.Body.String(), "dataset_imported") ||
		strings.Contains(events.Body.String(), "artifact_handoff") {
		t.Fatalf("parse failure workflow events status = %d, body = %s", events.Code, events.Body.String())
	}
}

func TestCreateDatasetWorkflowImportFailureStopsBuild(t *testing.T) {
	dir := t.TempDir()
	parseScript := filepath.Join(dir, "parse_worker.py")
	buildScript := filepath.Join(dir, "build_worker.py")
	document := filepath.Join(dir, "incoming", "doc.txt")
	schemaPath := filepath.Join(dir, "incoming", "bad_schema.json")
	buildMarker := filepath.Join(dir, "build-called")
	mustWrite(t, document, "hello dataset")
	mustWrite(t, schemaPath, `{"entities":`)
	writeCreateDatasetParseWorker(t, parseScript)
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import pathlib
pathlib.Path("`+buildMarker+`").write_text("called", encoding="utf-8")
`)
	routes := svc.NewService(createDatasetTestConfig(dir, parseScript, buildScript)).Routes()

	workflow := createDatasetWorkflow(t, routes, "imported", []string{document}, schemaPath)
	workflow = waitForWorkflow(t, routes, workflow["id"].(string), "failed")
	if errorText, _ := workflow["error"].(string); !strings.Contains(errorText, "dataset import step failed") || !strings.Contains(errorText, "parse schema json") {
		t.Fatalf("workflow error = %#v", workflow["error"])
	}
	if !containsWorkflowStep(workflow, "parse_documents", "parse_documents", "succeeded") ||
		!containsWorkflowStep(workflow, "dataset_import", "dataset_import", "failed") {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	if containsWorkflowStepNamed(workflow, "build_graph") {
		t.Fatalf("build step should not start after import failure: %#v", workflow["steps"])
	}
	if !containsArtifactStatus(workflow, "parsed_corpus", "written") ||
		!containsArtifactStatus(workflow, "metadata", "failed") {
		t.Fatalf("workflow artifacts after import failure = %#v", workflow["artifacts"])
	}
	if _, err := os.Stat(buildMarker); !os.IsNotExist(err) {
		t.Fatalf("build worker should not have been called; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "output", "datasets", "imported.json")); !os.IsNotExist(err) {
		t.Fatalf("dataset metadata should not exist after import failure, stat err = %v", err)
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK ||
		!strings.Contains(events.Body.String(), `"dataset_import_failed"`) ||
		strings.Contains(events.Body.String(), "artifact_handoff") {
		t.Fatalf("import failure workflow events status = %d, body = %s", events.Code, events.Body.String())
	}
}

func TestCreateDatasetWorkflowBuildFailurePreservesImportedArtifacts(t *testing.T) {
	dir := t.TempDir()
	parseScript := filepath.Join(dir, "parse_worker.py")
	buildScript := filepath.Join(dir, "build_worker.py")
	document := filepath.Join(dir, "incoming", "doc.txt")
	schemaPath := filepath.Join(dir, "incoming", "schema.json")
	mustWrite(t, document, "hello dataset")
	mustWrite(t, schemaPath, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
	writeCreateDatasetParseWorker(t, parseScript)
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import sys
print("build exploded", file=sys.stderr)
raise SystemExit(9)
`)
	routes := svc.NewService(createDatasetTestConfig(dir, parseScript, buildScript)).Routes()

	workflow := createDatasetWorkflow(t, routes, "imported", []string{document}, schemaPath)
	workflow = waitForWorkflow(t, routes, workflow["id"].(string), "failed")
	if errorText, _ := workflow["error"].(string); !strings.Contains(errorText, "build_graph step failed") || !strings.Contains(errorText, "build exploded") {
		t.Fatalf("workflow error = %#v", workflow["error"])
	}
	if !containsWorkflowStep(workflow, "parse_documents", "parse_documents", "succeeded") ||
		!containsWorkflowStep(workflow, "dataset_import", "dataset_import", "succeeded") ||
		!containsWorkflowStep(workflow, "build_graph", "build_graph", "failed") {
		t.Fatalf("workflow steps = %#v", workflow["steps"])
	}
	for _, name := range []string{"parsed_corpus", "corpus", "schema", "metadata"} {
		if !containsArtifactStatus(workflow, name, "written") {
			t.Fatalf("workflow artifact %s not preserved as written: %#v", name, workflow["artifacts"])
		}
	}
	if containsArtifactStatus(workflow, "graph", "written") || containsArtifactStatus(workflow, "chunks", "written") {
		t.Fatalf("graph/chunks should not be written after build failure: %#v", workflow["artifacts"])
	}
	for _, path := range []string{
		filepath.Join(dir, "data", "uploaded", "imported", "corpus.json"),
		filepath.Join(dir, "schemas", "imported.json"),
		filepath.Join(dir, "output", "datasets", "imported.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("imported artifact should be preserved %s: %v", path, err)
		}
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+workflow["id"].(string)+"/events", nil))
	if events.Code != http.StatusOK ||
		!strings.Contains(events.Body.String(), `"dataset_imported"`) ||
		!strings.Contains(events.Body.String(), `"artifact_handoff"`) ||
		!strings.Contains(events.Body.String(), "build exploded") {
		t.Fatalf("build failure workflow events status = %d, body = %s", events.Code, events.Body.String())
	}
}

func TestCreateDatasetWorkflowCancelPropagatesToParseJob(t *testing.T) {
	dir := t.TempDir()
	parseScript := filepath.Join(dir, "parse_worker.py")
	buildScript := filepath.Join(dir, "build_worker.py")
	document := filepath.Join(dir, "incoming", "doc.txt")
	schemaPath := filepath.Join(dir, "incoming", "schema.json")
	buildMarker := filepath.Join(dir, "build-called")
	mustWrite(t, document, "hello dataset")
	mustWrite(t, schemaPath, `{"Nodes":["person"],"Relations":["knows"],"Attributes":["name"]}`)
	writeExecutable(t, parseScript, `#!/usr/bin/env python3
import time
time.sleep(10)
`)
	writeExecutable(t, buildScript, `#!/usr/bin/env python3
import pathlib
pathlib.Path("`+buildMarker+`").write_text("called", encoding="utf-8")
`)
	routes := svc.NewService(createDatasetTestConfig(dir, parseScript, buildScript)).Routes()

	workflow := createDatasetWorkflow(t, routes, "imported", []string{document}, schemaPath)
	id := workflow["id"].(string)
	_ = waitForWorkflowStep(t, routes, id, "parse_documents")

	cancel := httptest.NewRecorder()
	routes.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/v1/workflows/"+id+"/cancel", nil))
	if cancel.Code != http.StatusOK || !strings.Contains(cancel.Body.String(), `"canceled":true`) {
		t.Fatalf("cancel create_dataset status = %d, body = %s", cancel.Code, cancel.Body.String())
	}
	workflow = waitForWorkflow(t, routes, id, "canceled")
	if !containsWorkflowStep(workflow, "parse_documents", "parse_documents", "canceled") {
		t.Fatalf("parse step not canceled: %#v", workflow["steps"])
	}
	if containsWorkflowStepNamed(workflow, "dataset_import") || containsWorkflowStepNamed(workflow, "build_graph") {
		t.Fatalf("workflow should stop after cancellation: %#v", workflow["steps"])
	}
	if _, err := os.Stat(buildMarker); !os.IsNotExist(err) {
		t.Fatalf("build worker should not have been called; stat err = %v", err)
	}
	events := httptest.NewRecorder()
	routes.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/v1/workflows/"+id+"/events", nil))
	if events.Code != http.StatusOK ||
		!strings.Contains(events.Body.String(), `"cancel_requested"`) ||
		!strings.Contains(events.Body.String(), `"canceled"`) ||
		strings.Contains(events.Body.String(), "dataset_imported") {
		t.Fatalf("create_dataset cancel events status = %d, body = %s", events.Code, events.Body.String())
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

func parseDocumentsTestConfig(dir string, script string) config.Config {
	return config.Config{
		DefaultDataset:  "demo",
		ArtifactRoot:    dir,
		CorpusRoot:      filepath.Join(dir, "data"),
		SchemaRoot:      filepath.Join(dir, "schemas"),
		JobRoot:         filepath.Join(dir, "jobs"),
		PythonBin:       "python3",
		ParseDocsScript: script,
		WorkerCWD:       dir,
		DatasetNames:    []string{"demo"},
	}
}

func createDatasetTestConfig(dir string, parseScript string, buildScript string) config.Config {
	return config.Config{
		DefaultDataset:   "demo",
		ArtifactRoot:     dir,
		CorpusRoot:       filepath.Join(dir, "data"),
		SchemaRoot:       filepath.Join(dir, "schemas"),
		GraphRoot:        filepath.Join(dir, "output", "graphs"),
		ChunksRoot:       filepath.Join(dir, "output", "chunks"),
		CacheRoot:        filepath.Join(dir, "retriever", "faiss_cache_new"),
		DatasetMetaRoot:  filepath.Join(dir, "output", "datasets"),
		JobRoot:          filepath.Join(dir, "jobs"),
		WorkflowRoot:     filepath.Join(dir, "workflows"),
		PythonBin:        "python3",
		ParseDocsScript:  parseScript,
		BuildGraphScript: buildScript,
		WorkerCWD:        dir,
		DatasetNames:     []string{"demo"},
	}
}

func writeCreateDatasetParseWorker(t *testing.T, path string) {
	t.Helper()
	writeExecutable(t, path, `#!/usr/bin/env python3
import json
import os
import sys
dataset = sys.argv[sys.argv.index("--dataset") + 1]
output = sys.argv[sys.argv.index("--output") + 1]
document = sys.argv[sys.argv.index("--document") + 1]
os.makedirs(os.path.dirname(output), exist_ok=True)
with open(output, "w", encoding="utf-8") as f:
    json.dump([{"id": dataset + "-doc-1", "text": open(document, encoding="utf-8").read()}], f)
`)
}

func createDatasetWorkflow(t *testing.T, routes http.Handler, dataset string, documents []string, schemaPath string) map[string]any {
	t.Helper()
	documentJSON := make([]string, 0, len(documents))
	for _, document := range documents {
		documentJSON = append(documentJSON, quote(document))
	}
	body := `{"type":"create_dataset","create_dataset":{"dataset":` + quote(dataset) + `,"document_paths":[` + strings.Join(documentJSON, ",") + `],"schema_path":` + quote(schemaPath) + `}}`
	create := httptest.NewRecorder()
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewBufferString(body)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create create_dataset workflow status = %d, body = %s", create.Code, create.Body.String())
	}
	var workflow map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &workflow); err != nil {
		t.Fatalf("decode create_dataset workflow: %v", err)
	}
	if workflow["id"] == "" || workflow["schema_version"] != "workflow/v1" || workflow["type"] != "create_dataset" {
		t.Fatalf("created create_dataset workflow = %#v", workflow)
	}
	return workflow
}

func createParseDocumentsJob(t *testing.T, routes http.Handler, document string) map[string]any {
	t.Helper()
	create := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"type":"parse_documents","parse_documents":{"dataset":"imported","document_paths":[` + quote(document) + `]}}`)
	routes.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create parse_documents status = %d, body = %s", create.Code, create.Body.String())
	}
	var job map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode parse_documents job: %v", err)
	}
	if job["id"] == "" || job["type"] != "parse_documents" {
		t.Fatalf("created parse_documents job = %#v", job)
	}
	return job
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

func containsWorkflowStepOutputArtifact(workflow map[string]any, stepName string, artifactName string, status string) bool {
	steps, ok := workflow["steps"].([]any)
	if !ok {
		return false
	}
	for _, step := range steps {
		item, ok := step.(map[string]any)
		if !ok || item["name"] != stepName {
			continue
		}
		artifacts, ok := item["output_artifacts"].([]any)
		if !ok {
			return false
		}
		for _, artifact := range artifacts {
			artifactMap, ok := artifact.(map[string]any)
			if ok && artifactMap["name"] == artifactName && artifactMap["status"] == status {
				return true
			}
		}
		return false
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
