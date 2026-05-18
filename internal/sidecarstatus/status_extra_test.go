package sidecarstatus_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/likun666661/youtu-rag-service/internal/config"
	"github.com/likun666661/youtu-rag-service/internal/sidecarstatus"
)

func TestVectorDefaultsDatasetForConfiguredSidecar(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dataset":"demo","indices":{}}`))
	}))
	defer server.Close()

	status := sidecarstatus.Vector(context.Background(), config.Config{
		DefaultSidecar: server.URL,
	}, "")
	if !status.Reachable || status.Dataset != "demo" {
		t.Fatalf("status = %#v", status)
	}
	if sawPath != "/v1/datasets/demo/cache" {
		t.Fatalf("sidecar path = %q", sawPath)
	}
}

func TestVectorReportsUnreachableSidecar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()

	status := sidecarstatus.Vector(context.Background(), config.Config{
		DefaultSidecar: server.URL,
	}, "demo")
	if !status.Configured {
		t.Fatalf("status = %#v", status)
	}
	if status.Reachable {
		t.Fatalf("status = %#v", status)
	}
	if status.Error == "" || !strings.Contains(status.Error, "HTTP 500") {
		t.Fatalf("status error = %q", status.Error)
	}
}
