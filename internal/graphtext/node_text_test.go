package graphtext_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/fuzzy-searcher-go/internal/dataset"
	"github.com/fuzzy-searcher-go/internal/graphtext"
)

const (
	graphPath    = "../../testdata/demo_graph_snapshot.json"
	nodeTextPath = "../../testdata/node_text_cases.json"
)

func TestNodeTextParity(t *testing.T) {
	// Load graph
	graph, err := dataset.LoadGraph(graphPath)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}

	// Load oracle
	oracleData, err := os.ReadFile(nodeTextPath)
	if err != nil {
		t.Fatalf("read oracle: %v", err)
	}
	var oracle map[string]string
	if err := json.Unmarshal(oracleData, &oracle); err != nil {
		t.Fatalf("parse oracle: %v", err)
	}

	// Check all oracle entries
	mismatches := 0
	for nodeID, expected := range oracle {
		node, ok := graph.Nodes[nodeID]
		if !ok {
			t.Errorf("node %s: exists in oracle but not in graph", nodeID)
			mismatches++
			continue
		}

		got := graphtext.NodeText(node)
		if got != expected {
			t.Errorf("node %s:\n  got:      %q\n  expected: %q", nodeID, got, expected)
			mismatches++
		}
	}

	// Also check we didn't miss any graph nodes
	goTexts := graphtext.AllNodeTexts(graph)
	for nodeID := range goTexts {
		if _, ok := oracle[nodeID]; !ok {
			t.Errorf("node %s: exists in graph but not in oracle", nodeID)
			mismatches++
		}
	}

	if mismatches == 0 {
		t.Logf("✅ All %d node texts match oracle", len(oracle))
	} else {
		t.Errorf("❌ %d/%d mismatches", mismatches, len(oracle))
	}
}
