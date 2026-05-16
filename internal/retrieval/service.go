package retrieval

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fuzzy-searcher-go/internal/chunks"
	"github.com/fuzzy-searcher-go/internal/dataset"
	"github.com/fuzzy-searcher-go/internal/graphtext"
	"github.com/fuzzy-searcher-go/internal/sidecar"
)

// Service is a phase-1 deterministic retriever core.
// Vector/embedding strategies are intentionally left behind interfaces for the
// Python sidecar task; this service establishes the Go contract and graph/chunk
// data path first.
type Service struct {
	graph   *dataset.Graph
	chunks  *chunks.Store
	sidecar *sidecar.Client
}

// Option configures Service.
type Option func(*Service)

// NewService constructs a retriever service over an already loaded graph.
func NewService(graph *dataset.Graph, chunkStore *chunks.Store, opts ...Option) *Service {
	s := &Service{graph: graph, chunks: chunkStore}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithSidecar enables vector-side chunk retrieval through Python.
func WithSidecar(client *sidecar.Client) Option {
	return func(s *Service) {
		s.sidecar = client
	}
}

// Retrieve runs deterministic retrieval strategies and returns stable JSON.
func (s *Service) Retrieve(ctx context.Context, req RetrieveRequest) (*RetrieveResult, error) {
	if s == nil || s.graph == nil {
		return nil, fmt.Errorf("retriever service has no graph")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 20
	}

	typeNodes := s.nodesBySchemaTypes(req.InvolvedTypes.Nodes)
	keywordNodes := s.keywordNodes(req.Question)
	candidateNodes := mergeNodeIDs(typeNodes, keywordNodes)
	if len(candidateNodes) == 0 {
		candidateNodes = s.allEntityNodes()
	}

	triples := s.oneHopTriples(candidateNodes)
	if len(triples) > topK {
		triples = triples[:topK]
	}

	chunkIDs := s.chunkIDsFromTriples(triples)
	chunkRetrievalResults, sidecarChunkIDs, sidecarErr := s.retrieveChunksViaSidecar(ctx, req, topK)
	chunkIDs = mergeStrings(chunkIDs, sidecarChunkIDs)

	chunkContents := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if content, ok := s.chunks.Get(id); ok {
			chunkContents = append(chunkContents, content)
		}
	}

	return &RetrieveResult{
		Triples:               formatTriples(s.graph, triples),
		ChunkIDs:              chunkIDs,
		ChunkContents:         chunkContents,
		ChunkRetrievalResults: chunkRetrievalResults,
		Debug: RetrieveDebug{Strategies: []StrategyDebug{
			{Name: "schema_type_filter", Count: len(typeNodes), Meta: map[string]any{"types": req.InvolvedTypes.Nodes}},
			{Name: "keyword_node_search", Count: len(keywordNodes)},
			{Name: "one_hop_triples", Count: len(triples)},
			{Name: "sidecar_chunk_faiss", Count: len(chunkRetrievalResults), Meta: sidecarMeta(s.sidecar != nil, req, sidecarErr)},
			{Name: "chunk_lookup", Count: len(chunkContents)},
		}},
	}, nil
}

func (s *Service) retrieveChunksViaSidecar(ctx context.Context, req RetrieveRequest, topK int) ([]string, []string, error) {
	if s.sidecar == nil {
		return nil, nil, nil
	}
	dataset := req.Dataset
	if dataset == "" {
		dataset = "demo"
	}
	embedResp, err := s.sidecar.Embed(ctx, sidecar.EmbedRequest{
		Texts:     []string{req.Question},
		Normalize: true,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(embedResp.Vectors) == 0 {
		return nil, nil, fmt.Errorf("sidecar embed returned no vectors")
	}
	searchResp, err := s.sidecar.Search(ctx, sidecar.SearchRequest{
		Dataset:     dataset,
		Index:       "chunk",
		QueryVector: embedResp.Vectors[0],
		TopK:        topK,
	})
	if err != nil {
		return nil, nil, err
	}

	results := make([]string, 0, len(searchResp.Hits))
	chunkIDs := make([]string, 0, len(searchResp.Hits))
	for _, hit := range searchResp.Hits {
		if hit.ID == "" {
			continue
		}
		content, ok := s.chunks.Get(hit.ID)
		if !ok {
			content = fmt.Sprintf("[Missing content for chunk %s]", hit.ID)
		}
		results = append(results, formatChunkResult(hit.ID, content, hit.Score))
		chunkIDs = append(chunkIDs, hit.ID)
	}
	return results, chunkIDs, nil
}

func sidecarMeta(enabled bool, req RetrieveRequest, err error) map[string]any {
	meta := map[string]any{
		"enabled": enabled,
		"dataset": req.Dataset,
	}
	if err != nil {
		meta["error"] = err.Error()
	}
	return meta
}

func (s *Service) nodesBySchemaTypes(types []string) []string {
	if len(types) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(types))
	for _, typ := range types {
		typ = strings.TrimSpace(strings.ToLower(typ))
		if typ != "" {
			want[typ] = struct{}{}
		}
	}

	var ids []string
	for id, node := range s.graph.Nodes {
		schemaType := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", node.Properties["schema_type"])))
		if _, ok := want[schemaType]; ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) keywordNodes(question string) []string {
	terms := queryTerms(question)
	if len(terms) == 0 {
		return nil
	}
	hits := map[string]struct{}{}
	for id, node := range s.graph.Nodes {
		text := strings.ToLower(graphtext.NodeText(node))
		for _, term := range terms {
			if strings.Contains(text, term) {
				hits[id] = struct{}{}
				break
			}
		}
	}
	return sortedKeys(hits)
}

func queryTerms(question string) []string {
	fields := strings.Fields(strings.ToLower(question))
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, " \t\n\r.,;:!?()[]{}\"'")
		if len(field) > 2 {
			terms = append(terms, field)
		}
	}
	return terms
}

func (s *Service) allEntityNodes() []string {
	var ids []string
	for id, node := range s.graph.Nodes {
		if node.Label == "entity" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

type triple struct {
	Source   string
	Relation string
	Target   string
}

func (s *Service) oneHopTriples(nodeIDs []string) []triple {
	allowed := make(map[string]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		allowed[id] = struct{}{}
	}
	var triples []triple
	seen := map[string]struct{}{}
	for _, edge := range s.graph.Edges {
		if _, sourceOK := allowed[edge.Source]; !sourceOK {
			if _, targetOK := allowed[edge.Target]; !targetOK {
				continue
			}
		}
		key := edge.Source + "\x00" + edge.Relation + "\x00" + edge.Target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		triples = append(triples, triple{Source: edge.Source, Relation: edge.Relation, Target: edge.Target})
	}
	sort.Slice(triples, func(i, j int) bool {
		if triples[i].Source != triples[j].Source {
			return triples[i].Source < triples[j].Source
		}
		if triples[i].Relation != triples[j].Relation {
			return triples[i].Relation < triples[j].Relation
		}
		return triples[i].Target < triples[j].Target
	})
	return triples
}

func (s *Service) chunkIDsFromTriples(triples []triple) []string {
	ids := map[string]struct{}{}
	for _, tr := range triples {
		for _, nodeID := range []string{tr.Source, tr.Target} {
			node := s.graph.Nodes[nodeID]
			if node == nil {
				continue
			}
			if id, ok := node.Properties["chunk id"].(string); ok && id != "" {
				ids[id] = struct{}{}
			}
		}
	}
	return sortedKeys(ids)
}

func formatTriples(graph *dataset.Graph, triples []triple) []string {
	out := make([]string, 0, len(triples))
	for _, tr := range triples {
		source := graph.Nodes[tr.Source].NodeName()
		target := graph.Nodes[tr.Target].NodeName()
		out = append(out, fmt.Sprintf("[%s, %s, %s]", source, tr.Relation, target))
	}
	return out
}

func mergeNodeIDs(groups ...[]string) []string {
	return mergeStrings(groups...)
}

func mergeStrings(groups ...[]string) []string {
	merged := map[string]struct{}{}
	for _, group := range groups {
		for _, id := range group {
			merged[id] = struct{}{}
		}
	}
	return sortedKeys(merged)
}

func formatChunkResult(chunkID, content string, score float64) string {
	if len(content) > 200 {
		content = content[:200]
	}
	return fmt.Sprintf("[Chunk %s] %s... [score: %.3f]", chunkID, content, score)
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
