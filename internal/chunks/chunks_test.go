package chunks_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuzzy-searcher-go/internal/chunks"
)

func TestLoadChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.txt")
	content := "id: c1\tChunk: first\\nchunk\nid: c2\tChunk: second\\tchunk\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write chunks: %v", err)
	}

	store, err := chunks.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, _ := store.Get("c1"); got != "first\nchunk" {
		t.Fatalf("c1 = %q", got)
	}
	if got, _ := store.Get("c2"); got != "second\tchunk" {
		t.Fatalf("c2 = %q", got)
	}
}
