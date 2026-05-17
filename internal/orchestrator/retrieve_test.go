package orchestrator_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuzzy-searcher-go/internal/config"
	"github.com/fuzzy-searcher-go/internal/orchestrator"
)

func TestRetrieveRejectsMissingArtifacts(t *testing.T) {
	retriever := orchestrator.NewRetriever(config.Config{})
	_, err := retriever.Retrieve(context.Background(), orchestrator.RetrieveInput{
		Question: "Alice",
	})
	if !errors.Is(err, orchestrator.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestRetrieveNative(t *testing.T) {
	dir := t.TempDir()
	graphPath, chunksPath := writeTinyGraphAndChunks(t, dir)

	retriever := orchestrator.NewRetriever(config.Config{})
	result, err := retriever.Retrieve(context.Background(), orchestrator.RetrieveInput{
		GraphPath:  graphPath,
		ChunksPath: chunksPath,
		Question:   "Alice",
		Mode:       "native",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.Triples) != 1 {
		t.Fatalf("triples = %#v", result.Triples)
	}
	if len(result.ChunkIDs) == 0 {
		t.Fatalf("chunk ids = %#v", result.ChunkIDs)
	}
}

func writeTinyGraphAndChunks(t *testing.T, dir string) (string, string) {
	t.Helper()
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
	return graphPath, chunksPath
}
