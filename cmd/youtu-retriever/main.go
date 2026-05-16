package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/fuzzy-searcher-go/internal/chunks"
	"github.com/fuzzy-searcher-go/internal/dataset"
	"github.com/fuzzy-searcher-go/internal/retrieval"
	"github.com/fuzzy-searcher-go/internal/sidecar"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "retrieve" {
		args = args[1:]
	}

	graphPath := flag.String("graph", "", "Path to graph JSON")
	chunksPath := flag.String("chunks", "", "Path to chunks txt")
	question := flag.String("question", "", "Question to retrieve for")
	topK := flag.Int("top-k", 20, "Max triples to return")
	datasetName := flag.String("dataset", "demo", "Dataset name for sidecar requests")
	involvedTypesPath := flag.String("involved-types", "", "Optional involved_types JSON file")
	sidecarURL := flag.String("sidecar-url", "", "Optional Python sidecar base URL")
	tripleTracePath := flag.String("triple-trace", "", "Optional Python triple-trace/v1 JSON path")
	sidecarTripleTrace := flag.Bool("sidecar-triple-trace", false, "Fetch Python triple-trace/v1 from --sidecar-url")
	sidecarPath2Triples := flag.Bool("sidecar-path2-triples", false, "Fetch Python path2-triples/v1 from --sidecar-url and merge locally")
	path2Threshold := flag.Float64("path2-threshold", 0.1, "Threshold for --sidecar-path2-triples")
	pretty := flag.Bool("pretty", true, "Pretty-print JSON output")
	flag.CommandLine.Parse(args)

	if *graphPath == "" || *chunksPath == "" || *question == "" {
		fmt.Fprintln(os.Stderr, "required flags: --graph, --chunks, --question")
		os.Exit(2)
	}

	graph, err := dataset.LoadGraph(*graphPath)
	if err != nil {
		fatal(err)
	}
	chunkStore, err := chunks.Load(*chunksPath)
	if err != nil {
		fatal(err)
	}

	req := retrieval.RetrieveRequest{
		Question: *question,
		TopK:     *topK,
		Dataset:  *datasetName,
	}
	if *involvedTypesPath != "" {
		req.InvolvedTypes, err = loadInvolvedTypes(*involvedTypesPath)
		if err != nil {
			fatal(err)
		}
	}
	client := (*sidecar.Client)(nil)
	if *sidecarURL != "" {
		client = sidecar.NewClient(*sidecarURL)
	}
	if *tripleTracePath != "" {
		req.TripleTrace, err = loadTripleTrace(*tripleTracePath)
		if err != nil {
			fatal(err)
		}
	}
	if *sidecarTripleTrace {
		if client == nil {
			fatal(fmt.Errorf("--sidecar-triple-trace requires --sidecar-url"))
		}
		req.TripleTrace, err = fetchTripleTrace(context.Background(), client, req)
		if err != nil {
			fatal(err)
		}
	}
	if *sidecarPath2Triples {
		if client == nil {
			fatal(fmt.Errorf("--sidecar-path2-triples requires --sidecar-url"))
		}
		if req.TripleTrace == nil {
			fatal(fmt.Errorf("--sidecar-path2-triples requires --sidecar-triple-trace or --triple-trace for path1 authority"))
		}
		req.Path2Triples, err = fetchPath2Triples(context.Background(), client, req, *path2Threshold)
		if err != nil {
			fatal(err)
		}
	}

	opts := []retrieval.Option{}
	if client != nil {
		opts = append(opts, retrieval.WithSidecar(client))
	}

	result, err := retrieval.NewService(graph, chunkStore, opts...).Retrieve(context.Background(), req)
	if err != nil {
		fatal(err)
	}

	var out []byte
	if *pretty {
		out, err = json.MarshalIndent(result, "", "  ")
	} else {
		out, err = json.Marshal(result)
	}
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
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

func loadTripleTrace(path string) (*retrieval.TripleTrace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read triple trace: %w", err)
	}
	var trace retrieval.TripleTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		return nil, fmt.Errorf("parse triple trace: %w", err)
	}
	if trace.SchemaVersion != "triple-trace/v1" {
		return nil, fmt.Errorf("unsupported triple trace schema: %q", trace.SchemaVersion)
	}
	return &trace, nil
}

func loadInvolvedTypes(path string) (retrieval.InvolvedTypes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return retrieval.InvolvedTypes{}, fmt.Errorf("read involved types: %w", err)
	}
	var involved retrieval.InvolvedTypes
	if err := json.Unmarshal(data, &involved); err != nil {
		return retrieval.InvolvedTypes{}, fmt.Errorf("parse involved types: %w", err)
	}
	return involved, nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
