package chunks

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Store holds chunk text keyed by Youtu-GraphRAG chunk ID.
type Store struct {
	ByID map[string]string
}

// Load reads output/chunks/<dataset>.txt.
// Expected line format: id: <chunk_id>\tChunk: <escaped text>
func Load(path string) (*Store, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open chunks file: %w", err)
	}
	defer file.Close()

	store := &Store{ByID: make(map[string]string)}
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		idPart, chunkPart, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(idPart, "id: ") || !strings.HasPrefix(chunkPart, "Chunk: ") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(idPart, "id: "))
		text := strings.TrimPrefix(chunkPart, "Chunk: ")
		text = strings.ReplaceAll(text, `\n`, "\n")
		text = strings.ReplaceAll(text, `\t`, "\t")
		if id != "" {
			store.ByID[id] = text
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan chunks file: %w", err)
	}

	return store, nil
}

// Get returns the chunk text for an ID.
func (s *Store) Get(id string) (string, bool) {
	if s == nil {
		return "", false
	}
	text, ok := s.ByID[id]
	return text, ok
}
