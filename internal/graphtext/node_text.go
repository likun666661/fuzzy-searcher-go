package graphtext

import (
	"fmt"
	"strings"

	"github.com/fuzzy-searcher-go/internal/dataset"
)

// NodeText produces the text representation of a graph node,
// replicating DualFAISSRetriever._get_node_text from faiss_filter.py.
//
// Logic (intentionally mirrors the Python original, including its quirks):
//  1. Get name and description from properties (fallback to "none")
//  2. str() then strip — this converts list to "['a','b']" BEFORE
//     the isinstance(list) check, so list handling is effectively dead code.
//     This is a known Python-side bug that we replicate for parity.
//  3. Format as "{name},{description}"
func NodeText(node *dataset.Node) string {
	name := "none"
	desc := "none"

	if node.Properties != nil {
		if v, ok := node.Properties["name"]; ok && v != nil {
			name = strings.TrimSpace(pythonStr(v))
		}
		if v, ok := node.Properties["description"]; ok && v != nil {
			desc = strings.TrimSpace(pythonStr(v))
		}
	}

	if name == "" {
		name = "none"
	}
	if desc == "" {
		desc = "none"
	}

	return strings.TrimSpace(fmt.Sprintf("%s,%s", name, desc))
}

// pythonStr mimics Python's str() for JSON-deserialized values.
// For lists, Python produces "['a', 'b']" — we replicate that exactly.
func pythonStr(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []any:
		// Python: str(["a", "b"]) → "['a', 'b']"
		parts := make([]string, len(val))
		for i, item := range val {
			switch s := item.(type) {
			case string:
				parts[i] = fmt.Sprintf("'%s'", s)
			default:
				parts[i] = fmt.Sprintf("%v", s)
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case float64:
		// JSON numbers come as float64; Python int str() has no decimal
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

// AllNodeTexts generates NodeText for every node in the graph.
func AllNodeTexts(g *dataset.Graph) map[string]string {
	result := make(map[string]string, len(g.Nodes))
	for id, node := range g.Nodes {
		result[id] = NodeText(node)
	}
	return result
}
