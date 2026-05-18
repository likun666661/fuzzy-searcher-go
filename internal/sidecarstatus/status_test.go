package sidecarstatus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/likun666661/youtu-rag-service/internal/config"
	"github.com/likun666661/youtu-rag-service/internal/sidecarstatus"
)

func TestVectorUnconfigured(t *testing.T) {
	status := sidecarstatus.Vector(context.Background(), config.Config{}, "demo")
	if status.Configured {
		t.Fatalf("status = %#v", status)
	}
	if status.Reachable {
		t.Fatalf("status = %#v", status)
	}
}

func TestVectorReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/datasets/demo/cache" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dataset": "demo",
			"indices": map[string]any{
				"chunk": map[string]any{"dimension": 384, "ntotal": 3},
			},
		})
	}))
	defer server.Close()

	status := sidecarstatus.Vector(context.Background(), config.Config{
		DefaultSidecar: server.URL,
	}, "demo")
	if !status.Configured || !status.Reachable {
		t.Fatalf("status = %#v", status)
	}
	if status.Cache == nil {
		t.Fatalf("status cache = %#v", status)
	}
}
