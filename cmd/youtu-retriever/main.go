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
	involvedTypesPath := flag.String("involved-types", "", "Optional involved_types JSON file")
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
	}
	if *involvedTypesPath != "" {
		req.InvolvedTypes, err = loadInvolvedTypes(*involvedTypesPath)
		if err != nil {
			fatal(err)
		}
	}

	result, err := retrieval.NewService(graph, chunkStore).Retrieve(context.Background(), req)
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
