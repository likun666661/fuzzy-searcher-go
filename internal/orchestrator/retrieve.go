package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fuzzy-searcher-go/internal/chunks"
	"github.com/fuzzy-searcher-go/internal/config"
	"github.com/fuzzy-searcher-go/internal/dataset"
	"github.com/fuzzy-searcher-go/internal/retrieval"
	"github.com/fuzzy-searcher-go/internal/sidecar"
)

// ErrInvalidRequest marks client-correctable service requests.
var ErrInvalidRequest = errors.New("invalid retrieve request")

// RetrieveInput is the HTTP-facing retrieve request shape.
type RetrieveInput struct {
	GraphPath      string                  `json:"graph_path,omitempty"`
	ChunksPath     string                  `json:"chunks_path,omitempty"`
	SidecarURL     string                  `json:"sidecar_url,omitempty"`
	Mode           string                  `json:"mode,omitempty"`
	Dataset        string                  `json:"dataset,omitempty"`
	Question       string                  `json:"question"`
	TopK           int                     `json:"top_k,omitempty"`
	Path2Threshold float64                 `json:"path2_threshold,omitempty"`
	InvolvedTypes  retrieval.InvolvedTypes `json:"involved_types,omitempty"`
}

// Retriever wires service-level requests to the retrieval domain engine.
type Retriever struct {
	config config.Config
}

// NewRetriever constructs retrieval orchestration over service config.
func NewRetriever(config config.Config) *Retriever {
	return &Retriever{config: config}
}

// Retrieve validates one request and delegates to the retrieval engine.
func (r *Retriever) Retrieve(ctx context.Context, input RetrieveInput) (*retrieval.RetrieveResult, error) {
	graphPath := firstNonEmpty(input.GraphPath, r.config.DefaultGraph)
	chunksPath := firstNonEmpty(input.ChunksPath, r.config.DefaultChunks)
	sidecarURL := firstNonEmpty(input.SidecarURL, r.config.DefaultSidecar)
	mode := firstNonEmpty(input.Mode, r.config.DefaultMode)
	datasetName := firstNonEmpty(input.Dataset, r.config.DefaultDataset)
	path2Threshold := input.Path2Threshold
	if path2Threshold == 0 {
		path2Threshold = r.config.Path2Threshold
	}

	if graphPath == "" {
		return nil, fmt.Errorf("%w: graph_path is required or YOUTU_RAG_GRAPH must be set", ErrInvalidRequest)
	}
	if chunksPath == "" {
		return nil, fmt.Errorf("%w: chunks_path is required or YOUTU_RAG_CHUNKS must be set", ErrInvalidRequest)
	}
	if input.Question == "" {
		return nil, fmt.Errorf("%w: question is required", ErrInvalidRequest)
	}

	graph, err := dataset.LoadGraph(graphPath)
	if err != nil {
		return nil, fmt.Errorf("load graph: %w", err)
	}
	chunkStore, err := chunks.Load(chunksPath)
	if err != nil {
		return nil, fmt.Errorf("load chunks: %w", err)
	}

	var client *sidecar.Client
	opts := []retrieval.Option{}
	if sidecarURL != "" {
		client = sidecar.NewClient(sidecarURL)
		opts = append(opts, retrieval.WithSidecar(client))
	}
	engine := retrieval.NewService(graph, chunkStore, opts...)
	req := retrieval.RetrieveRequest{
		Question:      input.Question,
		TopK:          input.TopK,
		InvolvedTypes: input.InvolvedTypes,
		Dataset:       datasetName,
	}
	if req.TopK <= 0 {
		req.TopK = 20
	}

	if err := applyMode(ctx, mode, client, engine, &req, path2Threshold); err != nil {
		return nil, err
	}
	return engine.Retrieve(ctx, req)
}

func applyMode(ctx context.Context, mode string, client *sidecar.Client, engine *retrieval.Service, req *retrieval.RetrieveRequest, path2Threshold float64) error {
	switch mode {
	case "", "native":
		return nil
	case "sidecar":
		if client == nil {
			return fmt.Errorf("%w: mode %q requires sidecar_url or YOUTU_RAG_SIDECAR_URL", ErrInvalidRequest, mode)
		}
		return nil
	case "runtime-trace":
		if client == nil {
			return fmt.Errorf("%w: mode %q requires sidecar_url or YOUTU_RAG_SIDECAR_URL", ErrInvalidRequest, mode)
		}
		trace, err := fetchTripleTrace(ctx, client, *req)
		if err != nil {
			return err
		}
		req.TripleTrace = trace
	case "path2-detrace":
		if client == nil {
			return fmt.Errorf("%w: mode %q requires sidecar_url or YOUTU_RAG_SIDECAR_URL", ErrInvalidRequest, mode)
		}
		trace, err := fetchTripleTrace(ctx, client, *req)
		if err != nil {
			return err
		}
		req.TripleTrace = trace
		path2, err := fetchPath2Triples(ctx, client, *req, path2Threshold)
		if err != nil {
			return err
		}
		req.Path2Triples = path2
	case "primitive-merge":
		if client == nil {
			return fmt.Errorf("%w: mode %q requires sidecar_url or YOUTU_RAG_SIDECAR_URL", ErrInvalidRequest, mode)
		}
		path1, err := fetchPath1Triples(ctx, client, *req, false)
		if err != nil {
			return err
		}
		req.Path1Triples = path1
		path2, err := fetchPath2Triples(ctx, client, *req, path2Threshold)
		if err != nil {
			return err
		}
		req.Path2Triples = path2
	case "rerank-merge":
		if client == nil {
			return fmt.Errorf("%w: mode %q requires sidecar_url or YOUTU_RAG_SIDECAR_URL", ErrInvalidRequest, mode)
		}
		path1, err := fetchPath1Triples(ctx, client, *req, true)
		if err != nil {
			return err
		}
		req.Path1Triples = path1
		path2, err := fetchPath2Triples(ctx, client, *req, path2Threshold)
		if err != nil {
			return err
		}
		req.Path2Triples = path2
		rerank, err := fetchRerankTriples(ctx, client, *req)
		if err != nil {
			return err
		}
		req.RerankTriples = rerank
	case "native-path1-rerank":
		if client == nil {
			return fmt.Errorf("%w: mode %q requires sidecar_url or YOUTU_RAG_SIDECAR_URL", ErrInvalidRequest, mode)
		}
		path1, err := engine.BuildNativePath1Triples(ctx, *req)
		if err != nil {
			return err
		}
		req.Path1Triples = path1
		path2, err := fetchPath2Triples(ctx, client, *req, path2Threshold)
		if err != nil {
			return err
		}
		req.Path2Triples = path2
		rerank, err := fetchRerankTriples(ctx, client, *req)
		if err != nil {
			return err
		}
		req.RerankTriples = rerank
	default:
		return fmt.Errorf("%w: unsupported mode %q", ErrInvalidRequest, mode)
	}
	return nil
}

func fetchTripleTrace(ctx context.Context, client *sidecar.Client, req retrieval.RetrieveRequest) (*retrieval.TripleTrace, error) {
	var trace retrieval.TripleTrace
	err := client.TripleTrace(ctx, sidecar.TripleTraceRequest{
		Dataset:  req.Dataset,
		Question: req.Question,
		TopK:     req.TopK,
		InvolvedTypes: sidecar.InvolvedTypes{
			Nodes:      req.InvolvedTypes.Nodes,
			Relations:  req.InvolvedTypes.Relations,
			Attributes: req.InvolvedTypes.Attributes,
		},
	}, &trace)
	if err != nil {
		return nil, fmt.Errorf("fetch triple trace: %w", err)
	}
	if trace.SchemaVersion != "triple-trace/v1" {
		return nil, fmt.Errorf("unsupported triple trace schema: %q", trace.SchemaVersion)
	}
	return &trace, nil
}

func fetchPath1Triples(ctx context.Context, client *sidecar.Client, req retrieval.RetrieveRequest, includeRaw bool) (*retrieval.Path1Triples, error) {
	var path1 retrieval.Path1Triples
	err := client.Path1Triples(ctx, sidecar.Path1TriplesRequest{
		Dataset:    req.Dataset,
		Question:   req.Question,
		TopK:       req.TopK,
		IncludeRaw: includeRaw,
		InvolvedTypes: sidecar.InvolvedTypes{
			Nodes:      req.InvolvedTypes.Nodes,
			Relations:  req.InvolvedTypes.Relations,
			Attributes: req.InvolvedTypes.Attributes,
		},
	}, &path1)
	if err != nil {
		return nil, fmt.Errorf("fetch path1 triples: %w", err)
	}
	if path1.SchemaVersion != "path1-triples/v1" {
		return nil, fmt.Errorf("unsupported path1 triples schema: %q", path1.SchemaVersion)
	}
	return &path1, nil
}

func fetchPath2Triples(ctx context.Context, client *sidecar.Client, req retrieval.RetrieveRequest, threshold float64) (*retrieval.Path2Triples, error) {
	var path2 retrieval.Path2Triples
	err := client.Path2Triples(ctx, sidecar.Path2TriplesRequest{
		Dataset:           req.Dataset,
		Question:          req.Question,
		TopK:              req.TopK,
		Threshold:         threshold,
		IncludeCandidates: false,
		IncludeIndexHits:  false,
	}, &path2)
	if err != nil {
		return nil, fmt.Errorf("fetch path2 triples: %w", err)
	}
	if path2.SchemaVersion != "path2-triples/v1" {
		return nil, fmt.Errorf("unsupported path2 triples schema: %q", path2.SchemaVersion)
	}
	return &path2, nil
}

func fetchRerankTriples(ctx context.Context, client *sidecar.Client, req retrieval.RetrieveRequest) (*retrieval.RerankTriples, error) {
	if req.Path1Triples == nil || len(req.Path1Triples.RawOneHopTriples) == 0 {
		return nil, fmt.Errorf("rerank triples requires path1 raw one-hop triples")
	}
	rawTriples, err := json.Marshal(req.Path1Triples.RawOneHopTriples)
	if err != nil {
		return nil, fmt.Errorf("marshal rerank triples input: %w", err)
	}
	var rerank retrieval.RerankTriples
	err = client.RerankTriples(ctx, sidecar.RerankTriplesRequest{
		Dataset:  req.Dataset,
		Question: req.Question,
		TopK:     req.TopK,
		Triples:  rawTriples,
	}, &rerank)
	if err != nil {
		return nil, fmt.Errorf("fetch reranked triples: %w", err)
	}
	if rerank.SchemaVersion != "rerank-triples/v1" {
		return nil, fmt.Errorf("unsupported rerank triples schema: %q", rerank.SchemaVersion)
	}
	return &rerank, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
