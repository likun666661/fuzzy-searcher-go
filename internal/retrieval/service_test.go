package retrieval_test

import (
	"context"
	"testing"

	"github.com/fuzzy-searcher-go/internal/chunks"
	"github.com/fuzzy-searcher-go/internal/dataset"
	"github.com/fuzzy-searcher-go/internal/retrieval"
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
