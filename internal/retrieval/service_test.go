package retrieval_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fuzzy-searcher-go/internal/chunks"
	"github.com/fuzzy-searcher-go/internal/dataset"
	"github.com/fuzzy-searcher-go/internal/retrieval"
	"github.com/fuzzy-searcher-go/internal/sidecar"
)

func TestRetrieveDeterministicCore(t *testing.T) {
	graph := &dataset.Graph{
		Nodes: map[string]*dataset.Node{
			"n1": {
				ID:    "n1",
				Label: "entity",
				Level: 2,
				Properties: map[string]any{
					"name":        "Lionel Messi",
					"description": "footballer",
					"schema_type": "person",
					"chunk id":    "c1",
				},
			},
			"n2": {
				ID:    "n2",
				Label: "entity",
				Level: 2,
				Properties: map[string]any{
					"name":        "FC Barcelona",
					"schema_type": "organization",
					"chunk id":    "c2",
				},
			},
		},
		Edges: []dataset.Edge{{Source: "n1", Target: "n2", Relation: "played_for"}},
	}
	chunkStore := &chunks.Store{ByID: map[string]string{
		"c1": "Messi source chunk",
		"c2": "Barcelona source chunk",
	}}

	result, err := retrieval.NewService(graph, chunkStore).Retrieve(context.Background(), retrieval.RetrieveRequest{
		Question: "Messi footballer",
		TopK:     5,
		InvolvedTypes: retrieval.InvolvedTypes{
			Nodes: []string{"person"},
		},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.Triples) != 1 || result.Triples[0] != "[Lionel Messi, played_for, FC Barcelona]" {
		t.Fatalf("triples = %#v", result.Triples)
	}
	if len(result.ChunkIDs) != 2 {
		t.Fatalf("chunk ids = %#v", result.ChunkIDs)
	}
}

func TestRetrieveWithSidecarChunkResults(t *testing.T) {
	graph := &dataset.Graph{
		Nodes: map[string]*dataset.Node{
			"n1": {
				ID:    "n1",
				Label: "entity",
				Level: 2,
				Properties: map[string]any{
					"name":     "Lionel Messi",
					"chunk id": "c1",
				},
			},
		},
		Edges: []dataset.Edge{{Source: "n1", Target: "n1", Relation: "related_to"}},
	}
	chunkStore := &chunks.Store{ByID: map[string]string{
		"c1": "Messi source chunk",
		"c2": "Barcelona source chunk",
		"c3": "Maradona source chunk",
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/embed":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":     "test-model",
				"dimension": 3,
				"vectors":   [][]float32{{0.1, 0.2, 0.3}},
			})
		case "/v1/faiss/search":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dataset": "demo",
				"index":   "chunk",
				"hits": []map[string]any{
					{"id": "c2", "score": 0.7, "rank": 1, "item": "ignored"},
					{"id": "c1", "score": 0.6, "rank": 2},
					{"id": "c3", "score": 0.5, "rank": 3},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := retrieval.NewService(
		graph,
		chunkStore,
		retrieval.WithSidecar(sidecar.NewClient(server.URL)),
	).Retrieve(context.Background(), retrieval.RetrieveRequest{
		Dataset:  "demo",
		Question: "Messi Barcelona",
		TopK:     3,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.ChunkRetrievalResults) != 3 {
		t.Fatalf("chunk retrieval results = %#v", result.ChunkRetrievalResults)
	}
	if len(result.ChunkIDs) != 3 {
		t.Fatalf("chunk ids = %#v", result.ChunkIDs)
	}
}
