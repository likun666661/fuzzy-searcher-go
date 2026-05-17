package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/fuzzy-searcher-go/internal/artifacts"
	"github.com/fuzzy-searcher-go/internal/config"
	"github.com/fuzzy-searcher-go/internal/jobs"
	"github.com/fuzzy-searcher-go/internal/orchestrator"
	"github.com/fuzzy-searcher-go/internal/sidecarstatus"
)

// Service owns HTTP routing and response mapping. Domain orchestration lives in
// internal/orchestrator and dependency health checks live in sidecarstatus.
type Service struct {
	config    config.Config
	retriever *orchestrator.Retriever
	jobs      *jobs.Manager
}

// NewService constructs the service layer.
func NewService(config config.Config) *Service {
	return &Service{
		config:    config,
		retriever: orchestrator.NewRetriever(config),
		jobs:      jobs.NewManager(),
	}
}

// Routes returns the HTTP handler tree for the long-running service.
func (s *Service) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	mux.HandleFunc("GET /v1/datasets", s.handleDatasets)
	mux.HandleFunc("GET /v1/datasets/{dataset}", s.handleDataset)
	mux.HandleFunc("GET /v1/datasets/{dataset}/artifacts", s.handleDatasetArtifacts)
	mux.HandleFunc("GET /v1/sidecars", s.handleSidecars)
	mux.HandleFunc("GET /v1/sidecars/vector/health", s.handleVectorSidecarHealth)
	mux.HandleFunc("POST /v1/retrieve", s.handleRetrieve)
	mux.HandleFunc("POST /v1/jobs", s.handleCreateJob)
	mux.HandleFunc("GET /v1/jobs/{job_id}", s.handleJob)
	mux.HandleFunc("GET /v1/jobs/{job_id}/events", s.handleJobEvents)
	mux.HandleFunc("POST /v1/jobs/{job_id}/cancel", s.handleCancelJob)
	return mux
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"app":     s.config.AppName,
		"env":     s.config.Env,
		"version": s.config.ServerVersion,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Service) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app":     s.config.AppName,
		"version": s.config.ServerVersion,
	})
}

func (s *Service) handleReady(w http.ResponseWriter, r *http.Request) {
	registry := artifacts.NewRegistry(s.config)
	defaultDataset := registry.Get(s.config.DefaultDataset)
	checks := map[string]any{
		"graph_configured":   s.config.DefaultGraph != "",
		"chunks_configured":  s.config.DefaultChunks != "",
		"sidecar_configured": s.config.DefaultSidecar != "",
		"default_dataset":    defaultDataset.Name,
		"dataset_status":     defaultDataset.Status,
	}
	ready := defaultDataset.RetrievalReady
	if s.config.DefaultGraph != "" {
		if _, err := os.Stat(s.config.DefaultGraph); err != nil {
			ready = false
			checks["graph_error"] = err.Error()
		}
	}
	if s.config.DefaultChunks != "" {
		if _, err := os.Stat(s.config.DefaultChunks); err != nil {
			ready = false
			checks["chunks_error"] = err.Error()
		}
	}
	if len(defaultDataset.MissingRetrievalArtifacts) > 0 {
		checks["missing_retrieval_artifacts"] = defaultDataset.MissingRetrievalArtifacts
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ready":  ready,
		"checks": checks,
	})
}

func (s *Service) handleDatasets(w http.ResponseWriter, r *http.Request) {
	registry := artifacts.NewRegistry(s.config)
	writeJSON(w, http.StatusOK, map[string]any{
		"datasets": registry.List(),
	})
}

func (s *Service) handleDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_dataset", fmt.Errorf("dataset is required"))
		return
	}
	registry := artifacts.NewRegistry(s.config)
	writeJSON(w, http.StatusOK, registry.Get(name))
}

func (s *Service) handleDatasetArtifacts(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_dataset", fmt.Errorf("dataset is required"))
		return
	}
	registry := artifacts.NewRegistry(s.config)
	writeJSON(w, http.StatusOK, map[string]any{
		"dataset":   name,
		"artifacts": registry.Artifacts(name),
	})
}

func (s *Service) handleSidecars(w http.ResponseWriter, r *http.Request) {
	status := sidecarstatus.Vector(r.Context(), s.config, s.config.DefaultDataset)
	writeJSON(w, http.StatusOK, map[string]any{
		"sidecars": []sidecarstatus.Status{status},
	})
}

func (s *Service) handleVectorSidecarHealth(w http.ResponseWriter, r *http.Request) {
	dataset := r.URL.Query().Get("dataset")
	if dataset == "" {
		dataset = s.config.DefaultDataset
	}
	status := sidecarstatus.Vector(r.Context(), s.config, dataset)
	httpStatus := http.StatusOK
	if status.Configured && !status.Reachable {
		httpStatus = http.StatusServiceUnavailable
	}
	writeJSON(w, httpStatus, status)
}

func (s *Service) handleRetrieve(w http.ResponseWriter, r *http.Request) {
	var input orchestrator.RetrieveInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	result, err := s.retriever.Retrieve(r.Context(), input)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, orchestrator.ErrInvalidRequest) {
			status = http.StatusBadRequest
		}
		writeError(w, status, "retrieve_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type createJobRequest struct {
	Type     string                     `json:"type"`
	Retrieve orchestrator.RetrieveInput `json:"retrieve,omitempty"`
}

func (s *Service) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var input createJobRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	switch input.Type {
	case "retrieve":
		job := s.jobs.Submit(input.Type, func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
			recorder.Event("retrieve_started", "retrieve request started")
			return s.retriever.Retrieve(ctx, input.Retrieve)
		})
		writeJSON(w, http.StatusAccepted, job)
	case "":
		writeError(w, http.StatusBadRequest, "invalid_job_type", fmt.Errorf("type is required"))
	default:
		writeError(w, http.StatusBadRequest, "invalid_job_type", fmt.Errorf("unsupported job type %q", input.Type))
	}
}

func (s *Service) handleJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Get(r.PathValue("job_id"))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Service) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.jobs.Events(r.PathValue("job_id"))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": r.PathValue("job_id"),
		"events": events,
	})
}

func (s *Service) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	job, canceled, err := s.jobs.Cancel(r.PathValue("job_id"))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"canceled": canceled,
		"job":      job,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": err.Error(),
		},
	})
}

func writeJobError(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job_not_found", err)
		return
	}
	writeError(w, http.StatusInternalServerError, "job_failed", err)
}
