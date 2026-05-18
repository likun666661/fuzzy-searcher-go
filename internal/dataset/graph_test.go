package dataset_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/likun666661/youtu-rag-service/internal/dataset"
)

func TestLoadGraphRAGRelationshipFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.json")
	raw := `[
  {
    "start_node": {"label": "entity", "properties": {"name": "A", "chunk id": "c1", "schema_type": "person"}},
    "relation": "knows",
    "end_node": {"label": "entity", "properties": {"name": "B", "chunk id": "c2", "schema_type": "person"}}
  },
  {
    "start_node": {"label": "entity", "properties": {"name": "A", "chunk id": "c1", "schema_type": "person"}},
    "relation": "has_attribute",
    "end_node": {"label": "attribute", "properties": {"name": "role: test", "chunk id": "c1"}}
  }
]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write graph: %v", err)
	}

	graph, err := dataset.LoadGraph(path)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(graph.Nodes))
	}
	if len(graph.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(graph.Edges))
	}
}
