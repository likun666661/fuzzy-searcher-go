package sidecar_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fuzzy-searcher-go/internal/sidecar"
)

func TestTripleTrace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/retrieval/triple-trace" {
			http.NotFound(w, r)
			return
		}
		var req sidecar.TripleTraceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Dataset != "demo" || req.Question != "question" || req.TopK != 20 {
			t.Fatalf("request = %#v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": "triple-trace/v1",
			"dataset":        req.Dataset,
			"records": []map[string]any{
				{"id": "qa_1", "question": req.Question},
			},
		})
	}))
	defer server.Close()

	var out struct {
		SchemaVersion string `json:"schema_version"`
		Dataset       string `json:"dataset"`
	}
	err := sidecar.NewClient(server.URL).TripleTrace(context.Background(), sidecar.TripleTraceRequest{
		Dataset:  "demo",
		Question: "question",
		TopK:     20,
	}, &out)
	if err != nil {
		t.Fatalf("TripleTrace: %v", err)
	}
	if out.SchemaVersion != "triple-trace/v1" || out.Dataset != "demo" {
		t.Fatalf("out = %#v", out)
	}
}

func TestPath2Triples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/retrieval/path2-triples" {
			http.NotFound(w, r)
			return
		}
		var req sidecar.Path2TriplesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Dataset != "demo" || req.Question != "question" || req.TopK != 20 {
			t.Fatalf("request = %#v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": "path2-triples/v1",
			"dataset":        req.Dataset,
			"rescored_triples": []map[string]any{
				{"rank": 1, "key": "h\tr\tt", "formatted_triple": "(h, r, t) [score: 0.900]"},
			},
		})
	}))
	defer server.Close()

	var out struct {
		SchemaVersion string `json:"schema_version"`
		Dataset       string `json:"dataset"`
	}
	err := sidecar.NewClient(server.URL).Path2Triples(context.Background(), sidecar.Path2TriplesRequest{
		Dataset:  "demo",
		Question: "question",
		TopK:     20,
	}, &out)
	if err != nil {
		t.Fatalf("Path2Triples: %v", err)
	}
	if out.SchemaVersion != "path2-triples/v1" || out.Dataset != "demo" {
		t.Fatalf("out = %#v", out)
	}
}
