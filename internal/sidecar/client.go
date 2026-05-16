package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Python vector sidecar described by Phase 1B.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient returns a sidecar client. baseURL should look like http://127.0.0.1:8765.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// EmbedRequest is POST /v1/embed input.
type EmbedRequest struct {
	Texts     []string `json:"texts"`
	Model     string   `json:"model,omitempty"`
	Normalize bool     `json:"normalize"`
}

// EmbedResponse is POST /v1/embed output.
type EmbedResponse struct {
	Model     string      `json:"model"`
	Dimension int         `json:"dimension"`
	Vectors   [][]float32 `json:"vectors"`
}

// SearchRequest is POST /v1/faiss/search input.
type SearchRequest struct {
	Dataset     string    `json:"dataset"`
	Index       string    `json:"index"`
	QueryVector []float32 `json:"query_vector"`
	TopK        int       `json:"top_k"`
}

// TripleTraceRequest is POST /v1/retrieval/triple-trace input.
type TripleTraceRequest struct {
	Dataset       string        `json:"dataset"`
	Question      string        `json:"question"`
	TopK          int           `json:"top_k"`
	InvolvedTypes InvolvedTypes `json:"involved_types,omitempty"`
}

// Path2TriplesRequest is POST /v1/retrieval/path2-triples input.
type Path2TriplesRequest struct {
	Dataset           string  `json:"dataset"`
	Question          string  `json:"question"`
	TopK              int     `json:"top_k"`
	Threshold         float64 `json:"threshold,omitempty"`
	IncludeCandidates bool    `json:"include_candidates,omitempty"`
	IncludeIndexHits  bool    `json:"include_index_hits,omitempty"`
}

// Path1TriplesRequest is POST /v1/retrieval/path1-triples input.
type Path1TriplesRequest struct {
	Dataset       string        `json:"dataset"`
	Question      string        `json:"question"`
	TopK          int           `json:"top_k"`
	InvolvedTypes InvolvedTypes `json:"involved_types,omitempty"`
	IncludeRaw    bool          `json:"include_raw,omitempty"`
}

// InvolvedTypes mirrors the retriever request shape without importing retrieval.
type InvolvedTypes struct {
	Nodes      []string `json:"nodes,omitempty"`
	Relations  []string `json:"relations,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// SearchResponse is POST /v1/faiss/search output.
type SearchResponse struct {
	Dataset string      `json:"dataset"`
	Index   string      `json:"index"`
	Hits    []SearchHit `json:"hits"`
}

// SearchHit is one sidecar search hit.
type SearchHit struct {
	ID              string          `json:"id"`
	Score           float64         `json:"score"`
	Rank            int             `json:"rank"`
	Item            json.RawMessage `json:"item,omitempty"`
	Triple          *TripleMetadata `json:"triple,omitempty"`
	FormattedTriple string          `json:"formatted_triple,omitempty"`
	SubjectID       string          `json:"subject_id,omitempty"`
	Relation        string          `json:"relation,omitempty"`
	ObjectID        string          `json:"object_id,omitempty"`
}

// TripleMetadata is additive metadata returned for triple index hits.
type TripleMetadata struct {
	SubjectID       string   `json:"subject_id"`
	Relation        string   `json:"relation"`
	ObjectID        string   `json:"object_id"`
	ChunkIDs        []string `json:"chunk_ids,omitempty"`
	FormattedTriple string   `json:"formatted_triple,omitempty"`
}

type errorEnvelope struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Embed requests embeddings for texts.
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	var resp EmbedResponse
	if err := c.post(ctx, "/v1/embed", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Search runs FAISS search in the sidecar.
func (c *Client) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	var resp SearchResponse
	if err := c.post(ctx, "/v1/faiss/search", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// TripleTrace requests Python-authoritative triple trace output.
func (c *Client) TripleTrace(ctx context.Context, req TripleTraceRequest, out any) error {
	return c.post(ctx, "/v1/retrieval/triple-trace", req, out)
}

// Path2Triples requests Python-authoritative path2 expansion/rescore output.
func (c *Client) Path2Triples(ctx context.Context, req Path2TriplesRequest, out any) error {
	return c.post(ctx, "/v1/retrieval/path2-triples", req, out)
}

// Path1Triples requests Python-authoritative path1 one-hop rerank output.
func (c *Client) Path1Triples(ctx context.Context, req Path1TriplesRequest, out any) error {
	return c.post(ctx, "/v1/retrieval/path1-triples", req, out)
}

func (c *Client) post(ctx context.Context, path string, in any, out any) error {
	if c == nil || c.baseURL == "" {
		return fmt.Errorf("sidecar client is not configured")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal sidecar request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build sidecar request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call sidecar %s: %w", path, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		var env errorEnvelope
		_ = json.NewDecoder(httpResp.Body).Decode(&env)
		if env.Error != nil {
			return fmt.Errorf("sidecar %s failed: %s: %s", path, env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("sidecar %s failed: HTTP %d", path, httpResp.StatusCode)
	}

	if err := json.NewDecoder(httpResp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode sidecar response: %w", err)
	}
	return nil
}
