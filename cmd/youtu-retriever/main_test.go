package main

import "testing"

func TestApplyMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		trace      bool
		path1      bool
		path2      bool
		rerank     bool
		shouldFail bool
	}{
		{name: "empty"},
		{name: "runtime trace", mode: "runtime-trace", trace: true},
		{name: "path2", mode: "path2-detrace", trace: true, path2: true},
		{name: "primitive", mode: "primitive-merge", path1: true, path2: true},
		{name: "rerank", mode: "rerank-merge", path1: true, path2: true, rerank: true},
		{name: "bad", mode: "unknown", shouldFail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var trace, path1, path2, rerank bool
			err := applyMode(tt.mode, &trace, &path1, &path2, &rerank)
			if tt.shouldFail {
				if err == nil {
					t.Fatalf("applyMode(%q) succeeded, want error", tt.mode)
				}
				return
			}
			if err != nil {
				t.Fatalf("applyMode(%q): %v", tt.mode, err)
			}
			if trace != tt.trace || path1 != tt.path1 || path2 != tt.path2 || rerank != tt.rerank {
				t.Fatalf("flags trace=%v path1=%v path2=%v rerank=%v", trace, path1, path2, rerank)
			}
		})
	}
}
