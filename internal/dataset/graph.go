package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Node represents a node in the knowledge graph.
type Node struct {
	ID         string
	Label      string
	Level      int
	Properties map[string]any
}

// Edge represents an edge in the knowledge graph.
type Edge struct {
	Source   string
	Target   string
	Key      int
	Relation string
}

// Graph holds the loaded knowledge graph.
type Graph struct {
	Nodes map[string]*Node
	Edges []Edge
}

// snapshot is the JSON structure of demo_graph_snapshot.json.
type snapshot struct {
	Format            string         `json:"format"`
	IncludesSynthetic bool           `json:"includes_synthetic_nodes"`
	Nodes             []snapshotNode `json:"nodes"`
	Edges             []snapshotEdge `json:"edges"`
}

type snapshotNode struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Level      int            `json:"level"`
	Properties map[string]any `json:"properties"`
}

type snapshotEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Key      int    `json:"key"`
	Relation string `json:"relation"`
}

type graphRAGRelationship struct {
	StartNode graphRAGNode `json:"start_node"`
	Relation  string       `json:"relation"`
	EndNode   graphRAGNode `json:"end_node"`
}

type graphRAGNode struct {
	Label      string         `json:"label"`
	Properties map[string]any `json:"properties"`
}

// LoadGraph reads either a Phase 1 graph snapshot JSON file or the
// Youtu-GraphRAG output/graphs/<dataset>_new.json relationship-list format.
func LoadGraph(path string) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read graph file: %w", err)
	}

	if g, ok, err := loadSnapshotGraph(data); ok || err != nil {
		return g, err
	}

	return loadGraphRAGRelationships(data)
}

func loadSnapshotGraph(data []byte) (*Graph, bool, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false, nil
	}
	if snap.Format == "" && len(snap.Nodes) == 0 && len(snap.Edges) == 0 {
		return nil, false, nil
	}

	g := &Graph{
		Nodes: make(map[string]*Node, len(snap.Nodes)),
		Edges: make([]Edge, 0, len(snap.Edges)),
	}

	for _, n := range snap.Nodes {
		g.Nodes[n.ID] = &Node{
			ID:         n.ID,
			Label:      n.Label,
			Level:      n.Level,
			Properties: n.Properties,
		}
	}

	for _, e := range snap.Edges {
		g.Edges = append(g.Edges, Edge{
			Source:   e.Source,
			Target:   e.Target,
			Key:      e.Key,
			Relation: e.Relation,
		})
	}

	return g, true, nil
}

func loadGraphRAGRelationships(data []byte) (*Graph, error) {
	var rels []graphRAGRelationship
	if err := json.Unmarshal(data, &rels); err != nil {
		return nil, fmt.Errorf("parse graph JSON: %w", err)
	}

	g := &Graph{
		Nodes: make(map[string]*Node),
		Edges: make([]Edge, 0, len(rels)),
	}
	nodeByKey := make(map[string]string)
	nextID := 0
	nextNodeID := func(node graphRAGNode) string {
		name := propertyString(node.Properties, "name")
		key := node.Label + "\x00" + name
		if id, ok := nodeByKey[key]; ok {
			return id
		}

		id := fmt.Sprintf("%s_%d", node.Label, nextID)
		nextID++
		nodeByKey[key] = id
		g.Nodes[id] = &Node{
			ID:         id,
			Label:      node.Label,
			Level:      levelForLabel(node.Label),
			Properties: node.Properties,
		}
		return id
	}

	edgeKeys := make(map[string]int)
	for _, rel := range rels {
		source := nextNodeID(rel.StartNode)
		target := nextNodeID(rel.EndNode)
		edgeKeyBase := source + "\x00" + target
		key := edgeKeys[edgeKeyBase]
		edgeKeys[edgeKeyBase] = key + 1
		g.Edges = append(g.Edges, Edge{
			Source:   source,
			Target:   target,
			Key:      key,
			Relation: rel.Relation,
		})
	}

	return g, nil
}

func levelForLabel(label string) int {
	switch label {
	case "attribute":
		return 1
	case "entity":
		return 2
	case "keyword":
		return 3
	case "community":
		return 4
	default:
		return 2
	}
}

// NodeName returns the "name" property as a string.
// Handles string, list ([]any), and numeric types.
func (n *Node) NodeName() string {
	return propertyString(n.Properties, "name")
}

// NodeDescription returns the "description" property as a string.
func (n *Node) NodeDescription() string {
	return propertyString(n.Properties, "description")
}

func propertyString(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(parts, ", ")
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", val))
	}
}
