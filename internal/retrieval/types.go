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
