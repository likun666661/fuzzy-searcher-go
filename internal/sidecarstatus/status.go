package sidecarstatus

import (
	"context"
	"time"

	"github.com/likun666661/youtu-rag-service/internal/config"
	"github.com/likun666661/youtu-rag-service/internal/sidecar"
)

// Status reports the service's view of one sidecar dependency.
type Status struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	URL        string `json:"url,omitempty"`
	Dataset    string `json:"dataset,omitempty"`
	Reachable  bool   `json:"reachable"`
	Error      string `json:"error,omitempty"`
	Cache      any    `json:"cache,omitempty"`
}

// Vector reports Python vector sidecar readiness for a dataset.
func Vector(ctx context.Context, cfg config.Config, dataset string) Status {
	status := Status{
		Name:       "vector",
		Configured: cfg.DefaultSidecar != "",
		URL:        cfg.DefaultSidecar,
		Dataset:    dataset,
	}
	if !status.Configured {
		status.Error = "YOUTU_RAG_SIDECAR_URL is not configured"
		return status
	}
	if dataset == "" {
		dataset = "demo"
		status.Dataset = dataset
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var cache map[string]any
	client := sidecar.NewClient(cfg.DefaultSidecar)
	if err := client.CacheHealth(checkCtx, dataset, &cache); err != nil {
		status.Error = err.Error()
		return status
	}
	status.Reachable = true
	status.Cache = cache
	return status
}
