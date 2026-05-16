package retrieval

// InvolvedTypes mirrors GraphQ's schema-aware decomposition output.
type InvolvedTypes struct {
	Nodes      []string `json:"nodes,omitempty"`
	Relations  []string `json:"relations,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// RetrieveRequest is the stable Go-side retrieval contract.
type RetrieveRequest struct {
	Question      string        `json:"question"`
	TopK          int           `json:"top_k,omitempty"`
	InvolvedTypes InvolvedTypes `json:"involved_types,omitempty"`
	Dataset       string        `json:"dataset,omitempty"`
	TripleTrace   *TripleTrace  `json:"-"`
	Path2Triples  *Path2Triples `json:"-"`
}

// RetrieveResult is the JSON contract consumed by harnesses and backend glue.
type RetrieveResult struct {
	Triples               []string      `json:"triples"`
	ChunkIDs              []string      `json:"chunk_ids"`
	ChunkContents         []string      `json:"chunk_contents"`
	ChunkRetrievalResults []string      `json:"chunk_retrieval_results,omitempty"`
	Debug                 RetrieveDebug `json:"debug"`
}

// RetrieveDebug keeps phase-1 behavior observable without committing to internals.
type RetrieveDebug struct {
	Strategies []StrategyDebug `json:"strategies"`
}

// StrategyDebug describes one retrieval strategy contribution.
type StrategyDebug struct {
	Name  string         `json:"name"`
	Count int            `json:"count"`
	Meta  map[string]any `json:"meta,omitempty"`
}

// TripleTrace is the Python-authoritative triple trace emitted by Phase 4D.
type TripleTrace struct {
	SchemaVersion string              `json:"schema_version"`
	Dataset       string              `json:"dataset"`
	Records       []TripleTraceRecord `json:"records"`
}

// TripleTraceRecord captures one question's Python-authoritative triple merge.
type TripleTraceRecord struct {
	ID        string            `json:"id"`
	Question  string            `json:"question"`
	Path1     TripleTracePath1  `json:"path1"`
	Retrieval TripleTraceResult `json:"retrieval"`
}

// TripleTracePath1 contains Python-authoritative path1 traces.
type TripleTracePath1 struct {
	RerankedTriples []TraceTriple `json:"reranked_triples"`
}

// TripleTraceResult mirrors the public retrieval fields Go can consume.
type TripleTraceResult struct {
	Triples               []string          `json:"triples"`
	ChunkIDs              []string          `json:"chunk_ids"`
	ChunkContentsByID     map[string]string `json:"chunk_contents_by_id"`
	ChunkRetrievalResults []string          `json:"chunk_retrieval_results"`
}

// Path2Triples is the Python-authoritative path2 primitive emitted by Phase 6A.
type Path2Triples struct {
	SchemaVersion   string        `json:"schema_version"`
	Dataset         string        `json:"dataset"`
	Question        string        `json:"question"`
	TopK            int           `json:"top_k"`
	Threshold       float64       `json:"threshold"`
	RescoredTriples []TraceTriple `json:"rescored_triples"`
	ExpandedTriples []TraceTriple `json:"expanded_candidates,omitempty"`
	TripleIndexHits []TraceTriple `json:"triple_index_hits,omitempty"`
}

// TraceTriple is one Python-authoritative triple trace item.
type TraceTriple struct {
	Rank                  int      `json:"rank"`
	Source                string   `json:"source"`
	Key                   string   `json:"key"`
	HeadID                string   `json:"head_id"`
	Relation              string   `json:"relation"`
	TailID                string   `json:"tail_id"`
	Score                 float64  `json:"score"`
	FormattedTriple       string   `json:"formatted_triple"`
	FormattedWithoutScore string   `json:"formatted_without_score"`
	ChunkIDs              []string `json:"chunk_ids"`
}
