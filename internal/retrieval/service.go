package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/likun666661/youtu-rag-service/internal/chunks"
	"github.com/likun666661/youtu-rag-service/internal/dataset"
	"github.com/likun666661/youtu-rag-service/internal/graphtext"
	"github.com/likun666661/youtu-rag-service/internal/sidecar"
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
	if req.RerankTriples != nil && req.Path2Triples != nil {
		result, err := s.retrieveFromRerankPrimitiveMerge(ctx, req)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
	}
	if req.Path1Triples != nil && req.Path2Triples != nil {
		result, err := s.retrieveFromPrimitiveMerge(ctx, req)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
	}
	if req.TripleTrace != nil && req.Path2Triples != nil {
		if result := s.retrieveFromPath2Detrace(req); result != nil {
			return result, nil
		}
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

func (s *Service) retrieveFromPath2Detrace(req RetrieveRequest) *RetrieveResult {
	record := selectTripleTraceRecord(req.TripleTrace, req.Question)
	if record == nil || req.Path2Triples == nil {
		return nil
	}

	scored := make([]TraceTriple, 0, len(req.Path2Triples.RescoredTriples)+len(record.Path1.RerankedTriples))
	scored = append(scored, req.Path2Triples.RescoredTriples...)
	scored = append(scored, record.Path1.RerankedTriples...)
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	topK := req.TopK
	if topK <= 0 {
		topK = len(scored)
	}
	if len(scored) > topK {
		scored = scored[:topK]
	}

	triples := make([]string, 0, len(scored))
	chunkSet := map[string]struct{}{}
	for _, item := range scored {
		if shouldSkipTripleRelation(item.Relation) {
			continue
		}
		formatted := item.FormattedTriple
		if formatted == "" && item.FormattedWithoutScore != "" {
			formatted = ensureTripleScore(item.FormattedWithoutScore, item.Score)
		}
		if formatted == "" {
			continue
		}
		triples = append(triples, formatted)
		for _, chunkID := range item.ChunkIDs {
			if chunkID != "" {
				chunkSet[chunkID] = struct{}{}
			}
		}
	}

	chunkRetrievalResults := append([]string(nil), record.Retrieval.ChunkRetrievalResults...)
	for _, chunkID := range record.Retrieval.ChunkIDs {
		if chunkID != "" {
			chunkSet[chunkID] = struct{}{}
		}
	}
	chunkIDs := sortedKeys(chunkSet)
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
		Triples:               triples,
		ChunkIDs:              chunkIDs,
		ChunkContents:         chunkContents,
		ChunkRetrievalResults: chunkRetrievalResults,
		Debug: RetrieveDebug{Strategies: []StrategyDebug{
			{
				Name:  "path2_detrace_merge",
				Count: len(triples),
				Meta: map[string]any{
					"path2_schema_version": req.Path2Triples.SchemaVersion,
					"path2_count":          len(req.Path2Triples.RescoredTriples),
					"path1_count":          len(record.Path1.RerankedTriples),
					"record_id":            record.ID,
				},
			},
		}},
	}
}

func (s *Service) retrieveFromPrimitiveMerge(ctx context.Context, req RetrieveRequest) (*RetrieveResult, error) {
	if req.Path1Triples == nil || req.Path2Triples == nil {
		return nil, nil
	}
	scored := mergeTraceTriples(req.Path2Triples.RescoredTriples, req.Path1Triples.RerankedTriples, req.TopK)
	triples, tripleChunkIDs := formatScoredTraceTriples(scored)

	topK := req.TopK
	if topK <= 0 {
		topK = len(scored)
	}
	chunkRetrievalResults, sidecarChunkIDs, sidecarErr := s.retrieveChunksViaSidecar(ctx, req, topK)
	chunkIDs := mergeStrings(sidecarChunkIDs, tripleChunkIDs)
	chunkContents := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if content, ok := s.chunks.Get(id); ok {
			chunkContents = append(chunkContents, content)
		}
	}
	meta := map[string]any{
		"path1_schema_version": req.Path1Triples.SchemaVersion,
		"path2_schema_version": req.Path2Triples.SchemaVersion,
		"path1_count":          len(req.Path1Triples.RerankedTriples),
		"path2_count":          len(req.Path2Triples.RescoredTriples),
		"path1_raw_count":      req.Path1Triples.RawOneHopTriplesCount,
	}
	for k, v := range sidecarMeta(s.sidecar != nil, req, sidecarErr) {
		meta["chunk_"+k] = v
	}
	return &RetrieveResult{
		Triples:               triples,
		ChunkIDs:              chunkIDs,
		ChunkContents:         chunkContents,
		ChunkRetrievalResults: chunkRetrievalResults,
		Debug: RetrieveDebug{Strategies: []StrategyDebug{
			{
				Name:  "path1_path2_primitive_merge",
				Count: len(triples),
				Meta:  meta,
			},
		}},
	}, nil
}

func (s *Service) retrieveFromRerankPrimitiveMerge(ctx context.Context, req RetrieveRequest) (*RetrieveResult, error) {
	if req.RerankTriples == nil || req.Path2Triples == nil {
		return nil, nil
	}
	scored := mergeTraceTriples(req.Path2Triples.RescoredTriples, req.RerankTriples.RerankedTriples, req.TopK)
	triples, tripleChunkIDs := formatScoredTraceTriples(scored)

	topK := req.TopK
	if topK <= 0 {
		topK = len(scored)
	}
	chunkRetrievalResults, sidecarChunkIDs, sidecarErr := s.retrieveChunksViaSidecar(ctx, req, topK)
	chunkIDs := mergeStrings(sidecarChunkIDs, tripleChunkIDs)
	chunkContents := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if content, ok := s.chunks.Get(id); ok {
			chunkContents = append(chunkContents, content)
		}
	}
	strategyName := "path1_rerank_path2_primitive_merge"
	if req.Path1Triples != nil && req.Path1Triples.SchemaVersion == "go-path1-candidates/v1" {
		strategyName = "go_path1_rerank_path2_primitive_merge"
	}
	meta := map[string]any{
		"rerank_schema_version": req.RerankTriples.SchemaVersion,
		"path2_schema_version":  req.Path2Triples.SchemaVersion,
		"rerank_count":          len(req.RerankTriples.RerankedTriples),
		"path2_count":           len(req.Path2Triples.RescoredTriples),
		"rerank_input_count":    rerankInputCount(req.RerankTriples),
	}
	if req.Path1Triples != nil {
		meta["path1_raw_count"] = req.Path1Triples.RawOneHopTriplesCount
		meta["path1_schema_version"] = req.Path1Triples.SchemaVersion
		for k, v := range req.Path1Triples.Meta {
			meta["path1_"+k] = v
		}
	}
	for k, v := range sidecarMeta(s.sidecar != nil, req, sidecarErr) {
		meta["chunk_"+k] = v
	}
	return &RetrieveResult{
		Triples:               triples,
		ChunkIDs:              chunkIDs,
		ChunkContents:         chunkContents,
		ChunkRetrievalResults: chunkRetrievalResults,
		Debug: RetrieveDebug{Strategies: []StrategyDebug{
			{
				Name:  strategyName,
				Count: len(triples),
				Meta:  meta,
			},
		}},
	}, nil
}

func rerankInputCount(rerank *RerankTriples) int {
	if rerank == nil {
		return 0
	}
	if rerank.InputCount > 0 {
		return rerank.InputCount
	}
	return rerank.Stats.InputTriples
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

func mergeTraceTriples(path2 []TraceTriple, path1 []TraceTriple, topK int) []TraceTriple {
	scored := make([]TraceTriple, 0, len(path2)+len(path1))
	scored = append(scored, path2...)
	scored = append(scored, path1...)
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if topK <= 0 || len(scored) <= topK {
		return scored
	}
	return scored[:topK]
}

func formatScoredTraceTriples(scored []TraceTriple) ([]string, []string) {
	triples := make([]string, 0, len(scored))
	chunkSet := map[string]struct{}{}
	for _, item := range scored {
		if shouldSkipTripleRelation(item.Relation) {
			continue
		}
		formatted := item.FormattedTriple
		if formatted == "" && item.FormattedWithoutScore != "" {
			formatted = ensureTripleScore(item.FormattedWithoutScore, item.Score)
		}
		if formatted == "" {
			continue
		}
		triples = append(triples, formatted)
		for _, chunkID := range item.ChunkIDs {
			if chunkID != "" {
				chunkSet[chunkID] = struct{}{}
			}
		}
	}
	return triples, sortedKeys(chunkSet)
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

// BuildNativePath1Triples builds path1 raw candidates in Go, leaving only
// embedding rerank scoring to the Python rerank-triples primitive.
func (s *Service) BuildNativePath1Triples(ctx context.Context, req RetrieveRequest) (*Path1Triples, error) {
	if s == nil || s.graph == nil {
		return nil, fmt.Errorf("retriever service has no graph")
	}
	if s.sidecar == nil {
		return nil, fmt.Errorf("native path1 candidates require --sidecar-url for node/relation FAISS")
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 20
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
		return nil, fmt.Errorf("embed native path1 query: %w", err)
	}
	if len(embedResp.Vectors) == 0 {
		return nil, fmt.Errorf("sidecar embed returned no vectors")
	}
	queryVector := embedResp.Vectors[0]

	searchK := topK * 3
	if searchK > 50 {
		searchK = 50
	}
	nodeResp, err := s.sidecar.Search(ctx, sidecar.SearchRequest{
		Dataset:     dataset,
		Index:       "node",
		QueryVector: queryVector,
		TopK:        searchK,
	})
	if err != nil {
		return nil, fmt.Errorf("search native path1 nodes: %w", err)
	}
	relationResp, err := s.sidecar.Search(ctx, sidecar.SearchRequest{
		Dataset:     dataset,
		Index:       "relation",
		QueryVector: queryVector,
		TopK:        topK,
	})
	if err != nil {
		return nil, fmt.Errorf("search native path1 relations: %w", err)
	}

	topNodes := s.nativePath1TopNodes(req.Question, nodeResp.Hits, topK)
	topRelations := nativePath1TopRelations(relationResp.Hits)
	keywords := queryTerms(req.Question)
	rawTriples := s.nativePath1RawTriples(topNodes, topRelations, keywords)

	traceTriples := make([]TraceTriple, 0, len(rawTriples))
	for i, tr := range rawTriples {
		traceTriples = append(traceTriples, TraceTriple{
			Rank:     i + 1,
			Source:   "go_path1_raw",
			Key:      traceTripleKey(tr.Source, tr.Relation, tr.Target),
			HeadID:   tr.Source,
			Relation: tr.Relation,
			TailID:   tr.Target,
			ChunkIDs: s.chunkIDsFromNodeIDs(tr.Source, tr.Target),
		})
	}

	return &Path1Triples{
		SchemaVersion: "go-path1-candidates/v1",
		Dataset:       dataset,
		Question:      req.Question,
		TopK:          topK,
		Meta: map[string]any{
			"top_nodes_count":     len(topNodes),
			"top_relations_count": len(topRelations),
			"keyword_count":       len(keywords),
			"node_search_k":       searchK,
		},
		RawOneHopTriplesCount: len(traceTriples),
		RawOneHopTriples:      traceTriples,
	}, nil
}

func (s *Service) nativePath1TopNodes(question string, nodeHits []sidecar.SearchHit, topK int) []string {
	seen := map[string]struct{}{}
	topNodes := make([]string, 0, topK)
	for _, hit := range nodeHits {
		if hit.ID == "" || hit.Score <= 0.05 {
			continue
		}
		if _, ok := s.graph.Nodes[hit.ID]; !ok {
			continue
		}
		if _, ok := seen[hit.ID]; ok {
			continue
		}
		seen[hit.ID] = struct{}{}
		topNodes = append(topNodes, hit.ID)
		if len(topNodes) >= topK {
			break
		}
	}
	for _, id := range s.keywordNodes(question) {
		if len(topNodes) >= topK {
			break
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		topNodes = append(topNodes, id)
	}
	return topNodes
}

func nativePath1TopRelations(hits []sidecar.SearchHit) []string {
	relations := make([]string, 0, len(hits))
	seen := map[string]struct{}{}
	for _, hit := range hits {
		relation := hit.ID
		if relation == "" {
			relation = hit.Relation
		}
		relation = strings.TrimSpace(relation)
		if relation == "" {
			continue
		}
		if _, ok := seen[relation]; ok {
			continue
		}
		seen[relation] = struct{}{}
		relations = append(relations, relation)
	}
	return relations
}

func (s *Service) nativePath1RawTriples(topNodes []string, topRelations []string, keywords []string) []triple {
	combined := make([]triple, 0)
	combined = append(combined, s.optimizedNeighborExpansion(topNodes)...)
	combined = append(combined, s.pathBasedTriples(topNodes, keywords, 2)...)
	combined = append(combined, s.relationMatchedTriples(topNodes, topRelations)...)
	return dedupeTriples(combined)
}

func (s *Service) optimizedNeighborExpansion(topNodes []string) []triple {
	outgoing := make(map[string][]string)
	edgeByPair := make(map[string]triple)
	for _, edge := range s.graph.Edges {
		outgoing[edge.Source] = append(outgoing[edge.Source], edge.Target)
		key := edge.Source + "\x00" + edge.Target
		if _, ok := edgeByPair[key]; !ok {
			edgeByPair[key] = triple{Source: edge.Source, Relation: edge.Relation, Target: edge.Target}
		}
	}

	allNeighbors := map[string]struct{}{}
	pairs := map[string][2]string{}
	for _, node := range topNodes {
		for _, neighbor := range outgoing[node] {
			allNeighbors[neighbor] = struct{}{}
		}
		for neighbor := range allNeighbors {
			pairs[node+"\x00"+neighbor] = [2]string{node, neighbor}
			pairs[neighbor+"\x00"+node] = [2]string{neighbor, node}
		}
	}

	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	triples := make([]triple, 0, len(keys))
	for _, key := range keys {
		pair := pairs[key]
		if tr, ok := edgeByPair[pair[0]+"\x00"+pair[1]]; ok && tr.Relation != "" {
			triples = append(triples, tr)
		}
	}
	return triples
}

func (s *Service) pathBasedTriples(startNodes []string, keywords []string, maxDepth int) []triple {
	if len(startNodes) == 0 || len(keywords) == 0 {
		return nil
	}
	outgoing := make(map[string][]string)
	edgeByPair := make(map[string]triple)
	for _, edge := range s.graph.Edges {
		outgoing[edge.Source] = append(outgoing[edge.Source], edge.Target)
		key := edge.Source + "\x00" + edge.Target
		if _, ok := edgeByPair[key]; !ok {
			edgeByPair[key] = triple{Source: edge.Source, Relation: edge.Relation, Target: edge.Target}
		}
	}
	for node := range outgoing {
		sort.Strings(outgoing[node])
	}

	var found []triple
	visited := map[string]struct{}{}
	var dfs func(node string, depth int, path []string)
	dfs = func(node string, depth int, path []string) {
		if depth > maxDepth {
			return
		}
		if _, ok := visited[node]; ok {
			return
		}
		visited[node] = struct{}{}

		nodeText := strings.ToLower(nodeTextForTriple(s.graph.Nodes[node]))
		for _, keyword := range keywords {
			if keyword != "" && strings.Contains(nodeText, keyword) {
				for i := 0; i < len(path)-1; i++ {
					if tr, ok := edgeByPair[path[i]+"\x00"+path[i+1]]; ok && tr.Relation != "" {
						found = append(found, tr)
					}
				}
				break
			}
		}
		if depth >= maxDepth {
			return
		}
		for _, neighbor := range outgoing[node] {
			if _, ok := visited[neighbor]; !ok {
				dfs(neighbor, depth+1, append(path, neighbor))
			}
		}
	}

	for _, start := range startNodes {
		dfs(start, 0, []string{start})
	}
	return found
}

func (s *Service) relationMatchedTriples(topNodes []string, relations []string) []triple {
	if len(topNodes) == 0 || len(relations) == 0 {
		return nil
	}
	nodeSet := make(map[string]struct{}, len(topNodes))
	for _, node := range topNodes {
		nodeSet[node] = struct{}{}
	}
	relationSet := make(map[string]struct{}, len(relations))
	for _, relation := range relations {
		relationSet[relation] = struct{}{}
	}

	triples := make([]triple, 0)
	for _, edge := range s.graph.Edges {
		if _, ok := relationSet[edge.Relation]; !ok {
			continue
		}
		if _, sourceOK := nodeSet[edge.Source]; !sourceOK {
			if _, targetOK := nodeSet[edge.Target]; !targetOK {
				continue
			}
		}
		triples = append(triples, triple{Source: edge.Source, Relation: edge.Relation, Target: edge.Target})
	}
	return triples
}

func dedupeTriples(in []triple) []triple {
	seen := map[string]struct{}{}
	out := make([]triple, 0, len(in))
	for _, tr := range in {
		key := traceTripleKey(tr.Source, tr.Relation, tr.Target)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Relation != out[j].Relation {
			return out[i].Relation < out[j].Relation
		}
		return out[i].Target < out[j].Target
	})
	return out
}

func traceTripleKey(head, relation, tail string) string {
	return head + "\t" + relation + "\t" + tail
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
