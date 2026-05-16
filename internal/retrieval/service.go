package retrieval

import (
	"context"
	"encoding/json"
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
	if req.TripleTrace != nil {
		if result := s.retrieveFromTripleTrace(req); result != nil {
			return result, nil
		}
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
	sidecarTriples, sidecarTripleChunkIDs, sidecarTripleErr := s.retrieveTriplesViaSidecar(ctx, req, topK)
	chunkIDs = mergeStrings(chunkIDs, sidecarTripleChunkIDs)

	chunkContents := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if content, ok := s.chunks.Get(id); ok {
			chunkContents = append(chunkContents, content)
		}
	}
	formattedTriples := formatTriples(s.graph, triples)
	if len(sidecarTriples) > 0 {
		formattedTriples = sidecarTriples
	}

	return &RetrieveResult{
		Triples:               formattedTriples,
		ChunkIDs:              chunkIDs,
		ChunkContents:         chunkContents,
		ChunkRetrievalResults: chunkRetrievalResults,
		Debug: RetrieveDebug{Strategies: []StrategyDebug{
			{Name: "schema_type_filter", Count: len(typeNodes), Meta: map[string]any{"types": req.InvolvedTypes.Nodes}},
			{Name: "keyword_node_search", Count: len(keywordNodes)},
			{Name: "one_hop_triples", Count: len(triples)},
			{Name: "sidecar_chunk_faiss", Count: len(chunkRetrievalResults), Meta: sidecarMeta(s.sidecar != nil, req, sidecarErr)},
			{Name: "sidecar_triple_faiss", Count: len(sidecarTriples), Meta: sidecarMeta(s.sidecar != nil, req, sidecarTripleErr)},
			{Name: "chunk_lookup", Count: len(chunkContents)},
		}},
	}, nil
}

func (s *Service) retrieveFromTripleTrace(req RetrieveRequest) *RetrieveResult {
	record := selectTripleTraceRecord(req.TripleTrace, req.Question)
	if record == nil {
		return nil
	}
	chunkIDs := append([]string(nil), record.Retrieval.ChunkIDs...)
	chunkContents := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if content, ok := record.Retrieval.ChunkContentsByID[id]; ok {
			chunkContents = append(chunkContents, content)
			continue
		}
		if content, ok := s.chunks.Get(id); ok {
			chunkContents = append(chunkContents, content)
		}
	}
	return &RetrieveResult{
		Triples:               append([]string(nil), record.Retrieval.Triples...),
		ChunkIDs:              chunkIDs,
		ChunkContents:         chunkContents,
		ChunkRetrievalResults: append([]string(nil), record.Retrieval.ChunkRetrievalResults...),
		Debug: RetrieveDebug{Strategies: []StrategyDebug{
			{
				Name:  "python_triple_trace",
				Count: len(record.Retrieval.Triples),
				Meta: map[string]any{
					"schema_version": req.TripleTrace.SchemaVersion,
					"record_id":      record.ID,
					"dataset":        req.TripleTrace.Dataset,
				},
			},
		}},
	}
}

func selectTripleTraceRecord(trace *TripleTrace, question string) *TripleTraceRecord {
	if trace == nil || len(trace.Records) == 0 {
		return nil
	}
	for i := range trace.Records {
		if trace.Records[i].Question == question {
			return &trace.Records[i]
		}
	}
	if len(trace.Records) == 1 {
		return &trace.Records[0]
	}
	return nil
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

func (s *Service) retrieveTriplesViaSidecar(ctx context.Context, req RetrieveRequest, topK int) ([]string, []string, error) {
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
		Index:       "triple",
		QueryVector: embedResp.Vectors[0],
		TopK:        topK,
	})
	if err != nil {
		return nil, nil, err
	}

	results := make([]string, 0, len(searchResp.Hits))
	chunkIDs := map[string]struct{}{}
	for _, hit := range searchResp.Hits {
		formatted, ids, ok := s.formatSidecarTriple(hit)
		if !ok {
			continue
		}
		results = append(results, formatted)
		for _, id := range ids {
			if id != "" {
				chunkIDs[id] = struct{}{}
			}
		}
	}
	return results, sortedKeys(chunkIDs), nil
}

func (s *Service) formatSidecarTriple(hit sidecar.SearchHit) (string, []string, bool) {
	if hit.Triple != nil {
		if shouldSkipTripleRelation(hit.Triple.Relation) {
			return "", nil, false
		}
		if hit.Triple.FormattedTriple != "" {
			ids := hit.Triple.ChunkIDs
			if len(ids) == 0 {
				ids = s.chunkIDsFromNodeIDs(hit.Triple.SubjectID, hit.Triple.ObjectID)
			}
			return ensureTripleScore(hit.Triple.FormattedTriple, hit.Score), ids, true
		}
		if hit.SubjectID == "" {
			hit.SubjectID = hit.Triple.SubjectID
		}
		if hit.Relation == "" {
			hit.Relation = hit.Triple.Relation
		}
		if hit.ObjectID == "" {
			hit.ObjectID = hit.Triple.ObjectID
		}
	}
	if shouldSkipTripleRelation(hit.Relation) {
		return "", nil, false
	}
	if hit.FormattedTriple != "" {
		return ensureTripleScore(hit.FormattedTriple, hit.Score), s.chunkIDsFromNodeIDs(hit.SubjectID, hit.ObjectID), true
	}

	subjectID, relation, objectID := hit.SubjectID, hit.Relation, hit.ObjectID
	if subjectID == "" || relation == "" || objectID == "" {
		parsedSubject, parsedRelation, parsedObject, ok := parseTripleItem(hit.Item)
		if !ok {
			return "", nil, false
		}
		subjectID, relation, objectID = parsedSubject, parsedRelation, parsedObject
	}
	if shouldSkipTripleRelation(relation) {
		return "", nil, false
	}
	subject := s.graph.Nodes[subjectID]
	object := s.graph.Nodes[objectID]
	if subject == nil || object == nil {
		return "", nil, false
	}

	formatted := fmt.Sprintf(
		"(%s %s, %s, %s %s) [score: %.3f]",
		nodeTextForTriple(subject),
		nodePropertiesForTriple(subject),
		relation,
		nodeTextForTriple(object),
		nodePropertiesForTriple(object),
		hit.Score,
	)
	return formatted, s.chunkIDsFromNodeIDs(subjectID, objectID), true
}

func shouldSkipTripleRelation(relation string) bool {
	return relation == "represented_by" || relation == "kw_filter_by"
}

func ensureTripleScore(formatted string, score float64) string {
	if strings.Contains(formatted, ") [score:") {
		return formatted
	}
	return fmt.Sprintf("%s [score: %.3f]", formatted, score)
}

func parseTripleItem(raw json.RawMessage) (string, string, string, bool) {
	if len(raw) == 0 {
		return "", "", "", false
	}
	var parts []string
	if err := json.Unmarshal(raw, &parts); err == nil && len(parts) >= 3 {
		return parts[0], parts[1], parts[2], true
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err == nil && len(items) >= 3 {
		return fmt.Sprintf("%v", items[0]), fmt.Sprintf("%v", items[1]), fmt.Sprintf("%v", items[2]), true
	}
	return "", "", "", false
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
		for _, id := range s.chunkIDsFromNodeIDs(tr.Source, tr.Target) {
			ids[id] = struct{}{}
		}
	}
	return sortedKeys(ids)
}

func (s *Service) chunkIDsFromNodeIDs(nodeIDs ...string) []string {
	ids := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		node := s.graph.Nodes[nodeID]
		if node == nil {
			continue
		}
		if id, ok := node.Properties["chunk id"].(string); ok && id != "" {
			ids[id] = struct{}{}
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

func nodeTextForTriple(node *dataset.Node) string {
	if node == nil {
		return ""
	}
	name := node.NodeName()
	description := node.NodeDescription()
	result := strings.TrimSpace(strings.TrimSpace(name) + " " + strings.TrimSpace(description))
	if result == "" {
		return "[Node: " + node.ID + "]"
	}
	return result
}

func nodePropertiesForTriple(node *dataset.Node) string {
	if node == nil {
		return ""
	}
	skip := map[string]struct{}{
		"name": {}, "description": {}, "properties": {}, "label": {}, "chunk id": {}, "level": {},
	}
	var props []string
	for key, value := range node.Properties {
		if _, ok := skip[key]; ok {
			continue
		}
		props = append(props, fmt.Sprintf("%s: %s", key, propertyValueString(value)))
	}
	sort.Strings(props)
	if len(props) == 0 {
		return ""
	}
	return "[" + strings.Join(props, ", ") + "]"
}

func propertyValueString(value any) string {
	switch v := value.(type) {
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", value)
	}
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
