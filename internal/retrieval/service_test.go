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

func TestRetrieveWithSidecarTripleResults(t *testing.T) {
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
			var req struct {
				Index string `json:"index"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode search request: %v", err)
			}
			hits := []map[string]any{}
			if req.Index == "chunk" {
				hits = []map[string]any{
					{"id": "c1", "score": 0.8, "rank": 1},
					{"id": "c2", "score": 0.7, "rank": 2},
				}
			}
			if req.Index == "triple" {
				hits = []map[string]any{
					{
						"id":    "n1 | played_for | n2",
						"score": 0.9,
						"rank":  1,
						"item":  []string{"n1", "played_for", "n2"},
						"triple": map[string]any{
							"subject_id":       "n1",
							"relation":         "played_for",
							"object_id":        "n2",
							"chunk_ids":        []string{"c1", "c2"},
							"formatted_triple": "(Lionel Messi footballer [schema_type: person], played_for, FC Barcelona [schema_type: organization])",
						},
					},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dataset": "demo",
				"index":   req.Index,
				"hits":    hits,
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
		TopK:     2,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	want := "(Lionel Messi footballer [schema_type: person], played_for, FC Barcelona [schema_type: organization]) [score: 0.900]"
	if len(result.Triples) != 1 || result.Triples[0] != want {
		t.Fatalf("triples = %#v, want %#v", result.Triples, want)
	}
	if len(result.ChunkRetrievalResults) != 2 {
		t.Fatalf("chunk retrieval results = %#v", result.ChunkRetrievalResults)
	}
	if len(result.ChunkIDs) != 2 {
		t.Fatalf("chunk ids = %#v", result.ChunkIDs)
	}
}

func TestRetrieveWithTripleTrace(t *testing.T) {
	graph := &dataset.Graph{
		Nodes: map[string]*dataset.Node{
			"n1": {
				ID: "n1",
				Properties: map[string]any{
					"name":     "Fallback",
					"chunk id": "c1",
				},
			},
		},
	}
	chunkStore := &chunks.Store{ByID: map[string]string{
		"c1": "fallback chunk",
	}}
	trace := &retrieval.TripleTrace{
		SchemaVersion: "triple-trace/v1",
		Dataset:       "demo",
		Records: []retrieval.TripleTraceRecord{
			{
				ID:       "qa_1",
				Question: "question",
				Retrieval: retrieval.TripleTraceResult{
					Triples:  []string{"(Python authority, relation, output) [score: 0.900]"},
					ChunkIDs: []string{"c1"},
					ChunkContentsByID: map[string]string{
						"c1": "python chunk",
					},
					ChunkRetrievalResults: []string{"[Chunk c1] python chunk... [score: 0.800]"},
				},
			},
		},
	}

	result, err := retrieval.NewService(graph, chunkStore).Retrieve(context.Background(), retrieval.RetrieveRequest{
		Question:    "question",
		TopK:        20,
		TripleTrace: trace,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := result.Triples; len(got) != 1 || got[0] != "(Python authority, relation, output) [score: 0.900]" {
		t.Fatalf("triples = %#v", got)
	}
	if got := result.ChunkContents; len(got) != 1 || got[0] != "python chunk" {
		t.Fatalf("chunk contents = %#v", got)
	}
	if len(result.Debug.Strategies) != 1 || result.Debug.Strategies[0].Name != "python_triple_trace" {
		t.Fatalf("debug strategies = %#v", result.Debug.Strategies)
	}
}

func TestRetrieveWithPath2Detrace(t *testing.T) {
	graph := &dataset.Graph{
		Nodes: map[string]*dataset.Node{
			"n1": {
				ID: "n1",
				Properties: map[string]any{
					"name":     "Fallback",
					"chunk id": "c1",
				},
			},
		},
	}
	chunkStore := &chunks.Store{ByID: map[string]string{
		"c1": "fallback chunk",
	}}
	trace := &retrieval.TripleTrace{
		SchemaVersion: "triple-trace/v1",
		Dataset:       "demo",
		Records: []retrieval.TripleTraceRecord{
			{
				ID:       "qa_1",
				Question: "question",
				Path1: retrieval.TripleTracePath1{RerankedTriples: []retrieval.TraceTriple{
					{
						Relation:        "path1_relation",
						Score:           0.7,
						FormattedTriple: "(path1, relation, output) [score: 0.700]",
						ChunkIDs:        []string{"c1"},
					},
				}},
				Retrieval: retrieval.TripleTraceResult{
					ChunkIDs: []string{"c1"},
					ChunkContentsByID: map[string]string{
						"c1": "python chunk",
					},
					ChunkRetrievalResults: []string{"[Chunk c1] python chunk... [score: 0.800]"},
				},
			},
		},
	}
	path2 := &retrieval.Path2Triples{
		SchemaVersion: "path2-triples/v1",
		Dataset:       "demo",
		RescoredTriples: []retrieval.TraceTriple{
			{
				Relation:        "path2_relation",
				Score:           0.9,
				FormattedTriple: "(path2, relation, output) [score: 0.900]",
				ChunkIDs:        []string{"c1"},
			},
		},
	}

	result, err := retrieval.NewService(graph, chunkStore).Retrieve(context.Background(), retrieval.RetrieveRequest{
		Question:     "question",
		TopK:         20,
		TripleTrace:  trace,
		Path2Triples: path2,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := result.Triples; len(got) != 2 ||
		got[0] != "(path2, relation, output) [score: 0.900]" ||
		got[1] != "(path1, relation, output) [score: 0.700]" {
		t.Fatalf("triples = %#v", got)
	}
	if len(result.Debug.Strategies) != 1 || result.Debug.Strategies[0].Name != "path2_detrace_merge" {
		t.Fatalf("debug strategies = %#v", result.Debug.Strategies)
	}
}

func TestRetrieveWithPrimitiveMerge(t *testing.T) {
	graph := &dataset.Graph{
		Nodes: map[string]*dataset.Node{
			"n1": {
				ID: "n1",
				Properties: map[string]any{
					"name":     "Fallback",
					"chunk id": "c1",
				},
			},
		},
	}
	chunkStore := &chunks.Store{ByID: map[string]string{
		"c1": "fallback chunk",
	}}
	path1 := &retrieval.Path1Triples{
		SchemaVersion:         "path1-triples/v1",
		Dataset:               "demo",
		RawOneHopTriplesCount: 4,
		RerankedTriples: []retrieval.TraceTriple{
			{
				Relation:        "path1_relation",
				Score:           0.7,
				FormattedTriple: "(path1, relation, output) [score: 0.700]",
				ChunkIDs:        []string{"c1"},
			},
		},
	}
	path2 := &retrieval.Path2Triples{
		SchemaVersion: "path2-triples/v1",
		Dataset:       "demo",
		RescoredTriples: []retrieval.TraceTriple{
			{
				Relation:        "path2_relation",
				Score:           0.9,
				FormattedTriple: "(path2, relation, output) [score: 0.900]",
				ChunkIDs:        []string{"c1"},
			},
		},
	}

	result, err := retrieval.NewService(graph, chunkStore).Retrieve(context.Background(), retrieval.RetrieveRequest{
		Question:     "question",
		TopK:         20,
		Path1Triples: path1,
		Path2Triples: path2,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := result.Triples; len(got) != 2 ||
		got[0] != "(path2, relation, output) [score: 0.900]" ||
		got[1] != "(path1, relation, output) [score: 0.700]" {
		t.Fatalf("triples = %#v", got)
	}
	if len(result.Debug.Strategies) != 1 || result.Debug.Strategies[0].Name != "path1_path2_primitive_merge" {
		t.Fatalf("debug strategies = %#v", result.Debug.Strategies)
	}
}

func TestRetrieveWithRerankPrimitiveMerge(t *testing.T) {
	graph := &dataset.Graph{
		Nodes: map[string]*dataset.Node{
			"n1": {
				ID: "n1",
				Properties: map[string]any{
					"name":     "Fallback",
					"chunk id": "c1",
				},
			},
		},
	}
	chunkStore := &chunks.Store{ByID: map[string]string{
		"c1": "fallback chunk",
	}}
	path1 := &retrieval.Path1Triples{
		SchemaVersion:         "path1-triples/v1",
		Dataset:               "demo",
		RawOneHopTriplesCount: 4,
		RawOneHopTriples: []retrieval.TraceTriple{
			{HeadID: "h", Relation: "path1_relation", TailID: "t"},
		},
	}
	rerank := &retrieval.RerankTriples{
		SchemaVersion: "rerank-triples/v1",
		Dataset:       "demo",
		InputCount:    4,
		RerankedTriples: []retrieval.TraceTriple{
			{
				Relation:        "path1_relation",
				Score:           0.7,
				FormattedTriple: "(path1 reranked, relation, output) [score: 0.700]",
				ChunkIDs:        []string{"c1"},
			},
		},
	}
	path2 := &retrieval.Path2Triples{
		SchemaVersion: "path2-triples/v1",
		Dataset:       "demo",
		RescoredTriples: []retrieval.TraceTriple{
			{
				Relation:        "path2_relation",
				Score:           0.9,
				FormattedTriple: "(path2, relation, output) [score: 0.900]",
				ChunkIDs:        []string{"c1"},
			},
		},
	}

	result, err := retrieval.NewService(graph, chunkStore).Retrieve(context.Background(), retrieval.RetrieveRequest{
		Question:      "question",
		TopK:          20,
		Path1Triples:  path1,
		Path2Triples:  path2,
		RerankTriples: rerank,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got := result.Triples; len(got) != 2 ||
		got[0] != "(path2, relation, output) [score: 0.900]" ||
		got[1] != "(path1 reranked, relation, output) [score: 0.700]" {
		t.Fatalf("triples = %#v", got)
	}
	if len(result.Debug.Strategies) != 1 || result.Debug.Strategies[0].Name != "path1_rerank_path2_primitive_merge" {
		t.Fatalf("debug strategies = %#v", result.Debug.Strategies)
	}
}
