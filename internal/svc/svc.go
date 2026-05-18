package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/likun666661/youtu-rag-service/internal/artifacts"
	"github.com/likun666661/youtu-rag-service/internal/config"
	"github.com/likun666661/youtu-rag-service/internal/datasetimport"
	"github.com/likun666661/youtu-rag-service/internal/datasetops"
	"github.com/likun666661/youtu-rag-service/internal/jobs"
	"github.com/likun666661/youtu-rag-service/internal/orchestrator"
	"github.com/likun666661/youtu-rag-service/internal/schemas"
	"github.com/likun666661/youtu-rag-service/internal/sidecarstatus"
	"github.com/likun666661/youtu-rag-service/internal/workers/answer"
	"github.com/likun666661/youtu-rag-service/internal/workers/buildgraph"
	"github.com/likun666661/youtu-rag-service/internal/workers/golden"
	"github.com/likun666661/youtu-rag-service/internal/workers/parsedocs"
	"github.com/likun666661/youtu-rag-service/internal/workflows"
)

// Service owns HTTP routing and response mapping. Domain orchestration lives in
// internal/orchestrator and dependency health checks live in sidecarstatus.
type Service struct {
	config    config.Config
	retriever *orchestrator.Retriever
	jobs      *jobs.Manager
	workflows *workflows.Manager
	ops       *datasetops.Store
}

// NewService constructs the service layer.
func NewService(config config.Config) *Service {
	return &Service{
		config:    config,
		retriever: orchestrator.NewRetriever(config),
		jobs:      jobs.NewManager(jobs.WithFileStore(config.JobRoot)),
		workflows: workflows.NewManager(
			workflows.WithFileStore(config.WorkflowRoot),
		),
		ops: datasetops.NewStore(config.DatasetOpsRoot),
	}
}

// Routes returns the HTTP handler tree for the long-running service.
func (s *Service) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	mux.HandleFunc("GET /v1/datasets", s.handleDatasets)
	mux.HandleFunc("GET /v1/schemas", s.handleSchemas)
	mux.HandleFunc("POST /v1/schemas/validate", s.handleValidateSchema)
	mux.HandleFunc("POST /v1/datasets/import", s.handleImportDataset)
	mux.HandleFunc("GET /v1/dataset-operations", s.handleDatasetOperations)
	mux.HandleFunc("GET /v1/dataset-operations/{operation_id}", s.handleDatasetOperation)
	mux.HandleFunc("GET /v1/datasets/{dataset}", s.handleDataset)
	mux.HandleFunc("GET /v1/datasets/{dataset}/schema", s.handleDatasetSchema)
	mux.HandleFunc("PUT /v1/datasets/{dataset}/schema", s.handlePutDatasetSchema)
	mux.HandleFunc("DELETE /v1/datasets/{dataset}", s.handleDeleteDataset)
	mux.HandleFunc("POST /v1/datasets/{dataset}/rebuild", s.handleRebuildDataset)
	mux.HandleFunc("GET /v1/datasets/{dataset}/operations", s.handleDatasetOperationsForDataset)
	mux.HandleFunc("GET /v1/datasets/{dataset}/artifacts", s.handleDatasetArtifacts)
	mux.HandleFunc("GET /v1/sidecars", s.handleSidecars)
	mux.HandleFunc("GET /v1/sidecars/vector/health", s.handleVectorSidecarHealth)
	mux.HandleFunc("POST /v1/retrieve", s.handleRetrieve)
	mux.HandleFunc("POST /v1/jobs", s.handleCreateJob)
	mux.HandleFunc("GET /v1/jobs/{job_id}", s.handleJob)
	mux.HandleFunc("GET /v1/jobs/{job_id}/events", s.handleJobEvents)
	mux.HandleFunc("POST /v1/jobs/{job_id}/cancel", s.handleCancelJob)
	mux.HandleFunc("POST /v1/workflows", s.handleCreateWorkflow)
	mux.HandleFunc("GET /v1/workflows", s.handleWorkflows)
	mux.HandleFunc("GET /v1/workflows/{workflow_id}", s.handleWorkflow)
	mux.HandleFunc("GET /v1/workflows/{workflow_id}/steps", s.handleWorkflowSteps)
	mux.HandleFunc("GET /v1/workflows/{workflow_id}/steps/{step_name}", s.handleWorkflowStep)
	mux.HandleFunc("GET /v1/workflows/{workflow_id}/events", s.handleWorkflowEvents)
	mux.HandleFunc("POST /v1/workflows/{workflow_id}/cancel", s.handleCancelWorkflow)
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

func (s *Service) handleSchemas(w http.ResponseWriter, r *http.Request) {
	records, err := schemas.List(s.config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "schema_list_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "schema-management-list/v1",
		"count":          len(records),
		"schemas":        records,
	})
}

type validateSchemaRequest struct {
	Schema json.RawMessage `json:"schema"`
}

func (s *Service) handleValidateSchema(w http.ResponseWriter, r *http.Request) {
	var input validateSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	result := schemas.Validate(input.Schema)
	if !result.Valid {
		status := http.StatusBadRequest
		code := "invalid_schema"
		for _, message := range result.Errors {
			if strings.Contains(message, schemas.ErrDuplicateItem.Error()) {
				code = "duplicate_schema_item"
				break
			}
		}
		writeJSON(w, status, map[string]any{
			"error": map[string]any{
				"code":    code,
				"message": strings.Join(result.Errors, "; "),
			},
			"result": result,
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleDatasetOperations(w http.ResponseWriter, r *http.Request) {
	operations := s.ops.List("")
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "dataset-operation-list/v1",
		"count":          len(operations),
		"operations":     operations,
	})
}

func (s *Service) handleDatasetOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("operation_id")
	op, err := s.ops.Get(id)
	if err != nil {
		writeDatasetOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (s *Service) handleImportDataset(w http.ResponseWriter, r *http.Request) {
	var input datasetimport.Request
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	metadata, err := datasetimport.Import(s.config, input)
	if err != nil {
		s.recordDatasetOperation(datasetops.Operation{
			Dataset: input.Dataset,
			Type:    datasetops.TypeImport,
			Status:  "failed",
			Request: input,
			Error:   err.Error(),
		})
		writeDatasetImportError(w, err)
		return
	}
	s.recordDatasetOperation(datasetops.Operation{
		Dataset:   metadata.Dataset,
		Type:      datasetops.TypeImport,
		Status:    "succeeded",
		Request:   input,
		Artifacts: metadata.Artifacts,
		Result:    metadata,
	})
	writeJSON(w, http.StatusCreated, metadata)
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

func (s *Service) handleDatasetSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_dataset", fmt.Errorf("dataset is required"))
		return
	}
	record, err := schemas.Get(s.config, name, schemas.GetOptions{
		AllowFallback: queryBoolDefault(r, "allow_fallback", true),
		IncludeBody:   queryBoolDefault(r, "include_body", true),
	})
	if err != nil {
		writeSchemaError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

type putDatasetSchemaRequest struct {
	Schema     json.RawMessage `json:"schema,omitempty"`
	Overwrite  *bool           `json:"overwrite,omitempty"`
	SourcePath string          `json:"source_path,omitempty"`
}

func (s *Service) handlePutDatasetSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_dataset", fmt.Errorf("dataset is required"))
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	var envelope putDatasetSchemaRequest
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	schemaBody := envelope.Schema
	if len(schemaBody) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_schema", fmt.Errorf("schema is required"))
		return
	}
	overwrite := false
	if envelope.Overwrite != nil {
		overwrite = *envelope.Overwrite
	}
	record, err := schemas.Put(s.config, schemas.PutRequest{
		Dataset:    name,
		Schema:     schemaBody,
		Overwrite:  overwrite,
		SourcePath: envelope.SourcePath,
	})
	if err != nil {
		s.recordDatasetOperation(datasetops.Operation{
			Dataset: name,
			Type:    datasetops.TypeSchemaUpdate,
			Status:  "failed",
			Request: map[string]any{
				"overwrite":   overwrite,
				"source_path": envelope.SourcePath,
			},
			Error: err.Error(),
		})
		writeSchemaError(w, err)
		return
	}
	s.recordDatasetOperation(datasetops.Operation{
		Dataset:   name,
		Type:      datasetops.TypeSchemaUpdate,
		Status:    "succeeded",
		Request:   map[string]any{"overwrite": overwrite, "source_path": envelope.SourcePath},
		Artifacts: record.Artifacts,
		Result:    record,
	})
	writeJSON(w, http.StatusOK, record)
}

func (s *Service) handleDeleteDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_dataset", fmt.Errorf("dataset is required"))
		return
	}
	result, err := datasetimport.Delete(s.config, datasetimport.DeleteRequest{
		Dataset:       name,
		IncludeOutput: queryBoolDefault(r, "include_outputs", true),
		DryRun:        queryBoolDefault(r, "dry_run", false),
		Force:         queryBoolDefault(r, "force", false),
	})
	if err != nil {
		dataset := result.Dataset
		if dataset == "" {
			dataset = name
		}
		s.recordDatasetOperation(datasetops.Operation{
			Dataset:   dataset,
			Type:      datasetops.TypeDelete,
			Status:    "failed",
			Artifacts: result.Artifacts,
			Result:    result,
			Error:     err.Error(),
		})
		writeDatasetDeleteError(w, result, err)
		return
	}
	s.recordDatasetOperation(datasetops.Operation{
		Dataset:   result.Dataset,
		Type:      datasetops.TypeDelete,
		Status:    operationStatusFromDatasetDelete(result),
		Artifacts: result.Artifacts,
		Result:    result,
	})
	writeJSON(w, http.StatusOK, result)
}

type rebuildDatasetRequest struct {
	OverwriteOutputs *bool `json:"overwrite_outputs,omitempty"`
	DryRun           bool  `json:"dry_run,omitempty"`
	BuildGraph       struct {
		ConfigPath       string `json:"config_path,omitempty"`
		Mode             string `json:"mode,omitempty"`
		GraphOutputPath  string `json:"graph_output_path,omitempty"`
		ChunksOutputPath string `json:"chunks_output_path,omitempty"`
		CacheDir         string `json:"cache_dir,omitempty"`
		PythonBin        string `json:"python_bin,omitempty"`
		ScriptPath       string `json:"script_path,omitempty"`
		WorkingDir       string `json:"working_dir,omitempty"`
	} `json:"build_graph,omitempty"`
}

func (s *Service) handleRebuildDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_dataset", fmt.Errorf("dataset is required"))
		return
	}
	var input rebuildDatasetRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
	}
	overwriteOutputs := true
	if input.OverwriteOutputs != nil {
		overwriteOutputs = *input.OverwriteOutputs
	}
	plan, spec, err := datasetimport.PlanRebuild(s.config, datasetimport.RebuildRequest{
		Dataset:          name,
		OverwriteOutput:  overwriteOutputs,
		DryRun:           input.DryRun,
		ConfigPath:       input.BuildGraph.ConfigPath,
		Mode:             input.BuildGraph.Mode,
		GraphOutputPath:  input.BuildGraph.GraphOutputPath,
		ChunksOutputPath: input.BuildGraph.ChunksOutputPath,
		CacheDir:         input.BuildGraph.CacheDir,
		PythonBin:        input.BuildGraph.PythonBin,
		ScriptPath:       input.BuildGraph.ScriptPath,
		WorkingDir:       input.BuildGraph.WorkingDir,
	})
	if err != nil {
		var rebuildErr datasetimport.RebuildError
		if errors.As(err, &rebuildErr) {
			s.recordDatasetOperation(datasetops.Operation{
				Dataset:   rebuildErr.Result.Dataset,
				Type:      datasetops.TypeRebuild,
				Status:    "failed",
				Request:   input,
				Artifacts: rebuildErr.Result.Artifacts,
				Result:    rebuildErr.Result,
				Error:     err.Error(),
			})
		} else {
			s.recordDatasetOperation(datasetops.Operation{
				Dataset: name,
				Type:    datasetops.TypeRebuild,
				Status:  "failed",
				Request: input,
				Error:   err.Error(),
			})
		}
		writeDatasetRebuildError(w, err)
		return
	}
	if input.DryRun {
		s.recordDatasetOperation(datasetops.Operation{
			Dataset:   plan.Dataset,
			Type:      datasetops.TypeRebuild,
			Status:    plan.Status,
			Request:   input,
			Artifacts: plan.Artifacts,
			Result:    plan,
		})
		writeJSON(w, http.StatusOK, plan)
		return
	}
	if overwriteOutputs {
		if err := cleanupRebuildOutputs(plan.Artifacts); err != nil {
			writeError(w, http.StatusInternalServerError, "dataset_rebuild_failed", err)
			return
		}
	}
	job := s.submitBuildGraphJob(s.buildGraphSpec(spec))
	plan.Status = "submitted"
	plan.JobID = job.ID
	s.recordDatasetOperation(datasetops.Operation{
		Dataset: plan.Dataset,
		Type:    datasetops.TypeRebuild,
		Status:  "running",
		Request: input,
		JobRefs: []datasetops.JobRef{{
			Name:  "build_graph",
			JobID: job.ID,
			Type:  job.Type,
		}},
		Artifacts: plan.Artifacts,
		Result:    plan,
	})
	writeJSON(w, http.StatusAccepted, plan)
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

func (s *Service) handleDatasetOperationsForDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_dataset", fmt.Errorf("dataset is required"))
		return
	}
	operations := s.ops.List(name)
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "dataset-operation-list/v1",
		"count":          len(operations),
		"dataset":        name,
		"operations":     operations,
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
	Type           string                     `json:"type"`
	Retrieve       orchestrator.RetrieveInput `json:"retrieve,omitempty"`
	GenerateGolden jobs.GenerateGoldenSpec    `json:"generate_golden,omitempty"`
	ParseDocuments jobs.ParseDocumentsSpec    `json:"parse_documents,omitempty"`
	BuildGraph     jobs.BuildGraphSpec        `json:"build_graph,omitempty"`
	Answer         jobs.AnswerSpec            `json:"answer,omitempty"`
}

type createWorkflowRequest struct {
	Type           string                       `json:"type"`
	BuildAndAnswer workflows.BuildAndAnswerSpec `json:"build_and_answer,omitempty"`
	CreateDataset  workflows.CreateDatasetSpec  `json:"create_dataset,omitempty"`
}

func (s *Service) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var input createJobRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	switch input.Type {
	case jobs.TypeRetrieve:
		spec := retrieveJobSpec(input.Retrieve)
		artifacts := retrieveJobArtifacts(input.Retrieve)
		job := s.jobs.SubmitSpec(input.Type, spec, artifacts, func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
			recorder.Event("retrieve_started", "retrieve request started")
			result, err := s.retriever.Retrieve(ctx, input.Retrieve)
			if err == nil {
				recorder.Event("artifact_result_inline", "retrieve result stored in job result")
			}
			return result, err
		})
		writeJSON(w, http.StatusAccepted, job)
	case jobs.TypeGenerateGolden:
		spec := s.generateGoldenSpec(input.GenerateGolden)
		job := s.submitGenerateGoldenJob(spec)
		writeJSON(w, http.StatusAccepted, job)
	case jobs.TypeParseDocuments:
		spec := s.parseDocumentsSpec(input.ParseDocuments)
		job := s.submitParseDocumentsJob(spec)
		writeJSON(w, http.StatusAccepted, job)
	case jobs.TypeBuildGraph:
		spec := s.buildGraphSpec(input.BuildGraph)
		job := s.submitBuildGraphJob(spec)
		writeJSON(w, http.StatusAccepted, job)
	case jobs.TypeAnswer:
		spec := s.answerSpec(input.Answer)
		job := s.submitAnswerJob(spec)
		writeJSON(w, http.StatusAccepted, job)
	case "":
		writeError(w, http.StatusBadRequest, "invalid_job_type", fmt.Errorf("type is required"))
	default:
		writeError(w, http.StatusBadRequest, "invalid_job_type", fmt.Errorf("unsupported job type %q", input.Type))
	}
}

func retrieveJobSpec(input orchestrator.RetrieveInput) jobs.RetrieveSpec {
	return jobs.RetrieveSpec{
		Dataset:        input.Dataset,
		Question:       input.Question,
		TopK:           input.TopK,
		Mode:           input.Mode,
		GraphPath:      input.GraphPath,
		ChunksPath:     input.ChunksPath,
		SidecarURL:     input.SidecarURL,
		Path2Threshold: input.Path2Threshold,
	}
}

func retrieveJobArtifacts(input orchestrator.RetrieveInput) []jobs.Artifact {
	artifacts := []jobs.Artifact{}
	if input.GraphPath != "" {
		artifacts = append(artifacts, jobs.Artifact{
			Name:        "graph",
			Role:        "input",
			Kind:        "graph_json",
			Dataset:     input.Dataset,
			Path:        input.GraphPath,
			Status:      "configured",
			Description: "Graph JSON used by the retrieve job.",
		})
	}
	if input.ChunksPath != "" {
		artifacts = append(artifacts, jobs.Artifact{
			Name:        "chunks",
			Role:        "input",
			Kind:        "chunks_txt",
			Dataset:     input.Dataset,
			Path:        input.ChunksPath,
			Status:      "configured",
			Description: "Chunk text file used by the retrieve job.",
		})
	}
	artifacts = append(artifacts, jobs.Artifact{
		Name:          "retrieve_result",
		Role:          "output",
		Kind:          "retrieve_result_json",
		SchemaVersion: "retrieve-result/v1",
		Dataset:       input.Dataset,
		Status:        "inline",
		Description:   "RetrieveResult is returned inline in the job result field.",
	})
	return artifacts
}

func (s *Service) generateGoldenSpec(input jobs.GenerateGoldenSpec) jobs.GenerateGoldenSpec {
	spec := input
	if spec.Dataset == "" {
		spec.Dataset = s.config.DefaultDataset
	}
	if spec.Dataset == "" {
		spec.Dataset = "demo"
	}
	if spec.OutputPath == "" {
		spec.OutputPath = filepath.Join(s.config.GoldenRoot, spec.Dataset+".json")
	}
	if spec.Limit <= 0 {
		spec.Limit = 1
	}
	if spec.PythonBin == "" {
		spec.PythonBin = s.config.PythonBin
	}
	if spec.ScriptPath == "" {
		spec.ScriptPath = s.config.GoldenScript
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = s.config.WorkerCWD
	}
	return spec
}

func (s *Service) submitGenerateGoldenJob(spec jobs.GenerateGoldenSpec) jobs.Job {
	artifacts := generateGoldenArtifacts(spec)
	return s.jobs.SubmitSpec(jobs.TypeGenerateGolden, spec, artifacts, func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		recorder.Event("worker_started", "generate_golden Python worker started")
		recorder.Artifact("golden_fixture", "running", "")
		result, err := golden.Run(ctx, golden.Config{
			PythonBin:  spec.PythonBin,
			ScriptPath: spec.ScriptPath,
			WorkingDir: spec.WorkingDir,
		}, spec)
		if err == nil {
			recorder.Artifact("golden_fixture", "written", result.OutputPath)
			recorder.Event("artifact_golden_written", "golden fixture artifact written")
		} else if errors.Is(err, golden.ErrMissingOutput) {
			recorder.Artifact("golden_fixture", "missing", "")
		} else {
			recorder.Artifact("golden_fixture", "failed", "")
		}
		return result, err
	})
}

func generateGoldenArtifacts(spec jobs.GenerateGoldenSpec) []jobs.Artifact {
	return []jobs.Artifact{
		{
			Name:          "golden_fixture",
			Role:          "output",
			Kind:          "retriever_golden_json",
			SchemaVersion: "retriever-golden/v1",
			Dataset:       spec.Dataset,
			Path:          spec.OutputPath,
			Status:        "pending",
			Description:   "Python retriever golden fixture written by generate_golden worker.",
		},
	}
}

func (s *Service) parseDocumentsSpec(input jobs.ParseDocumentsSpec) jobs.ParseDocumentsSpec {
	spec := input
	if spec.Dataset == "" {
		spec.Dataset = s.config.DefaultDataset
	}
	if spec.Dataset == "" {
		spec.Dataset = "demo"
	}
	if spec.OutputPath == "" {
		spec.OutputPath = filepath.Join(s.config.CorpusRoot, "uploaded", spec.Dataset, "corpus.json")
	}
	if spec.PythonBin == "" {
		spec.PythonBin = s.config.PythonBin
	}
	if spec.ScriptPath == "" {
		spec.ScriptPath = s.config.ParseDocsScript
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = s.config.WorkerCWD
	}
	return spec
}

func (s *Service) submitParseDocumentsJob(spec jobs.ParseDocumentsSpec) jobs.Job {
	artifacts := parseDocumentsArtifacts(spec)
	return s.jobs.SubmitSpec(jobs.TypeParseDocuments, spec, artifacts, func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		recorder.Event("worker_started", "parse_documents Python worker started")
		recorder.Artifact("corpus", "running", "")
		result, err := parsedocs.Run(ctx, parsedocs.Config{
			PythonBin:  spec.PythonBin,
			ScriptPath: spec.ScriptPath,
			WorkingDir: spec.WorkingDir,
		}, spec)
		if err == nil {
			recorder.Artifact("corpus", "written", result.OutputPath)
			recorder.Event("artifact_corpus_written", "corpus artifact written")
		} else if errors.Is(err, parsedocs.ErrMissingOutput) {
			recorder.Artifact("corpus", "missing", "")
		} else {
			recorder.Artifact("corpus", "failed", "")
		}
		return result, err
	})
}

func parseDocumentsArtifacts(spec jobs.ParseDocumentsSpec) []jobs.Artifact {
	artifacts := make([]jobs.Artifact, 0, len(spec.DocumentPaths)+1)
	for idx, path := range spec.DocumentPaths {
		artifacts = append(artifacts, jobs.Artifact{
			Name:        fmt.Sprintf("document_%d", idx+1),
			Role:        "input",
			Kind:        "source_document",
			Dataset:     spec.Dataset,
			Path:        path,
			Status:      "configured",
			Description: "Raw document consumed by the parse_documents worker.",
		})
	}
	artifacts = append(artifacts, jobs.Artifact{
		Name:          "corpus",
		Role:          "output",
		Kind:          "corpus_json",
		SchemaVersion: "corpus-json/v1",
		Dataset:       spec.Dataset,
		Path:          spec.OutputPath,
		Status:        "pending",
		Description:   "Corpus JSON written by the parse_documents worker.",
	})
	return artifacts
}

func (s *Service) buildGraphSpec(input jobs.BuildGraphSpec) jobs.BuildGraphSpec {
	spec := input
	if spec.Dataset == "" {
		spec.Dataset = s.config.DefaultDataset
	}
	if spec.Dataset == "" {
		spec.Dataset = "demo"
	}
	artifactPaths := map[string]string{}
	for _, artifact := range artifacts.NewRegistry(s.config).Artifacts(spec.Dataset) {
		artifactPaths[artifact.Name] = artifact.Path
	}
	if spec.CorpusPath == "" {
		spec.CorpusPath = artifactPaths["corpus"]
	}
	if spec.SchemaPath == "" {
		spec.SchemaPath = artifactPaths["schema"]
	}
	if spec.GraphOutputPath == "" {
		spec.GraphOutputPath = artifactPaths["graph"]
	}
	if spec.ChunksOutputPath == "" {
		spec.ChunksOutputPath = artifactPaths["chunks"]
	}
	if spec.CacheDir == "" {
		spec.CacheDir = artifactPaths["cache"]
	}
	if spec.PythonBin == "" {
		spec.PythonBin = s.config.PythonBin
	}
	if spec.ScriptPath == "" {
		spec.ScriptPath = s.config.BuildGraphScript
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = s.config.WorkerCWD
	}
	return spec
}

func (s *Service) submitBuildGraphJob(spec jobs.BuildGraphSpec) jobs.Job {
	artifacts := buildGraphArtifacts(spec)
	return s.jobs.SubmitSpec(jobs.TypeBuildGraph, spec, artifacts, func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		recorder.Event("worker_started", "build_graph Python worker started")
		recorder.Artifact("graph", "running", "")
		recorder.Artifact("chunks", "running", "")
		result, err := buildgraph.Run(ctx, buildgraph.Config{
			PythonBin:  spec.PythonBin,
			ScriptPath: spec.ScriptPath,
			WorkingDir: spec.WorkingDir,
		}, spec)
		if err == nil {
			recorder.Artifact("graph", "written", result.GraphOutputPath)
			recorder.Artifact("chunks", "written", result.ChunksOutputPath)
			if result.CacheDir != "" {
				recorder.Artifact("cache", "written", result.CacheDir)
			}
			recorder.Event("artifact_graph_written", "graph and chunks artifacts written")
		} else if errors.Is(err, buildgraph.ErrMissingOutput) {
			recorder.Artifact("graph", "missing", "")
			recorder.Artifact("chunks", "missing", "")
		} else {
			recorder.Artifact("graph", "failed", "")
			recorder.Artifact("chunks", "failed", "")
		}
		return result, err
	})
}

func buildGraphArtifacts(spec jobs.BuildGraphSpec) []jobs.Artifact {
	return []jobs.Artifact{
		{
			Name:        "corpus",
			Role:        "input",
			Kind:        "corpus_json",
			Dataset:     spec.Dataset,
			Path:        spec.CorpusPath,
			Status:      "configured",
			Description: "Corpus JSON consumed by the build_graph worker.",
		},
		{
			Name:        "schema",
			Role:        "input",
			Kind:        "schema_json",
			Dataset:     spec.Dataset,
			Path:        spec.SchemaPath,
			Status:      "configured",
			Description: "Schema JSON consumed by the build_graph worker.",
		},
		{
			Name:          "graph",
			Role:          "output",
			Kind:          "graph_json",
			SchemaVersion: "youtu-graph/v1",
			Dataset:       spec.Dataset,
			Path:          spec.GraphOutputPath,
			Status:        "pending",
			Description:   "Knowledge graph JSON written by the build_graph worker.",
		},
		{
			Name:        "chunks",
			Role:        "output",
			Kind:        "chunks_txt",
			Dataset:     spec.Dataset,
			Path:        spec.ChunksOutputPath,
			Status:      "pending",
			Description: "Chunk text file written by the build_graph worker.",
		},
		{
			Name:        "cache",
			Role:        "output",
			Kind:        "faiss_cache_dir",
			Dataset:     spec.Dataset,
			Path:        spec.CacheDir,
			Status:      "pending",
			Description: "Vector cache directory prepared for retrieval indexing.",
		},
	}
}

func (s *Service) answerSpec(input jobs.AnswerSpec) jobs.AnswerSpec {
	spec := input
	if spec.Dataset == "" {
		spec.Dataset = s.config.DefaultDataset
	}
	if spec.Dataset == "" {
		spec.Dataset = "demo"
	}
	artifactPaths := map[string]string{}
	for _, artifact := range artifacts.NewRegistry(s.config).Artifacts(spec.Dataset) {
		artifactPaths[artifact.Name] = artifact.Path
	}
	if spec.OutputPath == "" {
		spec.OutputPath = filepath.Join(s.config.ArtifactRoot, "output", "answers", spec.Dataset+".json")
	}
	if spec.Mode == "" {
		spec.Mode = s.config.DefaultMode
	}
	if spec.TopK <= 0 {
		spec.TopK = 20
	}
	if spec.GraphPath == "" {
		spec.GraphPath = artifactPaths["graph"]
	}
	if spec.ChunksPath == "" {
		spec.ChunksPath = artifactPaths["chunks"]
	}
	if spec.PythonBin == "" {
		spec.PythonBin = s.config.PythonBin
	}
	if spec.ScriptPath == "" {
		spec.ScriptPath = s.config.AnswerScript
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = s.config.WorkerCWD
	}
	return spec
}

func (s *Service) submitAnswerJob(spec jobs.AnswerSpec) jobs.Job {
	artifacts := answerArtifacts(spec)
	return s.jobs.SubmitSpec(jobs.TypeAnswer, spec, artifacts, func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		recorder.Event("worker_started", "answer Python worker started")
		recorder.Artifact("answer", "running", "")
		result, err := answer.Run(ctx, answer.Config{
			PythonBin:  spec.PythonBin,
			ScriptPath: spec.ScriptPath,
			WorkingDir: spec.WorkingDir,
		}, spec)
		if err == nil {
			recorder.Artifact("answer", "written", result.OutputPath)
			recorder.Event("artifact_answer_written", "answer artifact written")
		} else if errors.Is(err, answer.ErrMissingOutput) {
			recorder.Artifact("answer", "missing", "")
		} else {
			recorder.Artifact("answer", "failed", "")
		}
		return result, err
	})
}

func answerArtifacts(spec jobs.AnswerSpec) []jobs.Artifact {
	artifacts := []jobs.Artifact{}
	if spec.GraphPath != "" {
		artifacts = append(artifacts, jobs.Artifact{
			Name:        "graph",
			Role:        "input",
			Kind:        "graph_json",
			Dataset:     spec.Dataset,
			Path:        spec.GraphPath,
			Status:      "configured",
			Description: "Graph JSON consumed by the answer worker.",
		})
	}
	if spec.ChunksPath != "" {
		artifacts = append(artifacts, jobs.Artifact{
			Name:        "chunks",
			Role:        "input",
			Kind:        "chunks_txt",
			Dataset:     spec.Dataset,
			Path:        spec.ChunksPath,
			Status:      "configured",
			Description: "Chunk text file consumed by the answer worker.",
		})
	}
	artifacts = append(artifacts, jobs.Artifact{
		Name:          "answer",
		Role:          "output",
		Kind:          "answer_json",
		SchemaVersion: "answer-output/v1",
		Dataset:       spec.Dataset,
		Path:          spec.OutputPath,
		Status:        "pending",
		Description:   "Final answer JSON written by the answer worker.",
	})
	return artifacts
}

func (s *Service) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var input createWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	switch input.Type {
	case workflows.TypeBuildAndAnswer:
		spec := s.buildAndAnswerSpec(input.BuildAndAnswer)
		artifacts := buildAndAnswerArtifacts(spec)
		workflow := s.workflows.SubmitSpec(input.Type, spec, artifacts, func(ctx context.Context, recorder *workflows.Recorder) (any, error) {
			return s.runBuildAndAnswerWorkflow(ctx, recorder, spec)
		})
		writeJSON(w, http.StatusAccepted, workflow)
	case workflows.TypeCreateDataset:
		spec := s.createDatasetSpec(input.CreateDataset)
		artifacts := createDatasetArtifacts(spec)
		workflow := s.workflows.SubmitSpec(input.Type, spec, artifacts, func(ctx context.Context, recorder *workflows.Recorder) (any, error) {
			return s.runCreateDatasetWorkflow(ctx, recorder, spec)
		})
		s.recordDatasetOperation(datasetops.Operation{
			Dataset: spec.Dataset,
			Type:    datasetops.TypeCreateDataset,
			Status:  "running",
			Request: input.CreateDataset,
			WorkflowRefs: []datasetops.WorkflowRef{{
				Name:       "create_dataset",
				WorkflowID: workflow.ID,
				Type:       workflow.Type,
			}},
			Artifacts: artifacts,
			Result: map[string]any{
				"workflow_id": workflow.ID,
			},
		})
		writeJSON(w, http.StatusAccepted, workflow)
	case "":
		writeError(w, http.StatusBadRequest, "invalid_workflow_type", fmt.Errorf("type is required"))
	default:
		writeError(w, http.StatusBadRequest, "invalid_workflow_type", fmt.Errorf("unsupported workflow type %q", input.Type))
	}
}

func (s *Service) buildAndAnswerSpec(input workflows.BuildAndAnswerSpec) workflows.BuildAndAnswerSpec {
	spec := input
	if spec.Dataset == "" {
		spec.Dataset = s.config.DefaultDataset
	}
	if spec.Dataset == "" {
		spec.Dataset = "demo"
	}
	artifactPaths := map[string]string{}
	for _, artifact := range artifacts.NewRegistry(s.config).Artifacts(spec.Dataset) {
		artifactPaths[artifact.Name] = artifact.Path
	}
	if spec.CorpusPath == "" {
		spec.CorpusPath = artifactPaths["corpus"]
	}
	if spec.SchemaPath == "" {
		spec.SchemaPath = artifactPaths["schema"]
	}
	if spec.GraphOutputPath == "" {
		spec.GraphOutputPath = artifactPaths["graph"]
	}
	if spec.ChunksOutputPath == "" {
		spec.ChunksOutputPath = artifactPaths["chunks"]
	}
	if spec.CacheDir == "" {
		spec.CacheDir = artifactPaths["cache"]
	}
	if spec.AnswerOutputPath == "" {
		spec.AnswerOutputPath = filepath.Join(s.config.ArtifactRoot, "output", "answers", spec.Dataset+".json")
	}
	if spec.AnswerMode == "" {
		spec.AnswerMode = s.config.DefaultMode
	}
	if spec.TopK <= 0 {
		spec.TopK = 20
	}
	return spec
}

func buildAndAnswerArtifacts(spec workflows.BuildAndAnswerSpec) []jobs.Artifact {
	return []jobs.Artifact{
		{
			Name:        "corpus",
			Role:        "input",
			Kind:        "corpus_json",
			Dataset:     spec.Dataset,
			Path:        spec.CorpusPath,
			Status:      "configured",
			Description: "Corpus JSON consumed by the build_graph step.",
		},
		{
			Name:        "schema",
			Role:        "input",
			Kind:        "schema_json",
			Dataset:     spec.Dataset,
			Path:        spec.SchemaPath,
			Status:      "configured",
			Description: "Schema JSON consumed by the build_graph step.",
		},
		{
			Name:          "graph",
			Role:          "output",
			Kind:          "graph_json",
			SchemaVersion: "youtu-graph/v1",
			Dataset:       spec.Dataset,
			Path:          spec.GraphOutputPath,
			Status:        "pending",
			Description:   "Graph artifact handed from build_graph to answer.",
		},
		{
			Name:        "chunks",
			Role:        "output",
			Kind:        "chunks_txt",
			Dataset:     spec.Dataset,
			Path:        spec.ChunksOutputPath,
			Status:      "pending",
			Description: "Chunks artifact handed from build_graph to answer.",
		},
		{
			Name:        "cache",
			Role:        "output",
			Kind:        "faiss_cache_dir",
			Dataset:     spec.Dataset,
			Path:        spec.CacheDir,
			Status:      "pending",
			Description: "Vector cache prepared by build_graph for later retrieval.",
		},
		{
			Name:          "answer",
			Role:          "output",
			Kind:          "answer_json",
			SchemaVersion: "answer-output/v1",
			Dataset:       spec.Dataset,
			Path:          spec.AnswerOutputPath,
			Status:        "pending",
			Description:   "Final answer JSON written by the answer step.",
		},
	}
}

func (s *Service) createDatasetSpec(input workflows.CreateDatasetSpec) workflows.CreateDatasetSpec {
	spec := input
	if spec.Dataset == "" {
		spec.Dataset = s.config.DefaultDataset
	}
	if spec.Dataset == "" {
		spec.Dataset = "demo"
	}
	artifactPaths := map[string]string{}
	for _, artifact := range artifacts.NewRegistry(s.config).Artifacts(spec.Dataset) {
		artifactPaths[artifact.Name] = artifact.Path
	}
	if spec.CorpusOutputPath == "" {
		spec.CorpusOutputPath = artifactPaths["corpus"]
	}
	if spec.ParseOutputPath == "" {
		spec.ParseOutputPath = filepath.Join(s.config.ArtifactRoot, "output", "workflow_staging", spec.Dataset, "parsed_corpus.json")
	}
	if spec.GraphOutputPath == "" {
		spec.GraphOutputPath = artifactPaths["graph"]
	}
	if spec.ChunksOutputPath == "" {
		spec.ChunksOutputPath = artifactPaths["chunks"]
	}
	if spec.CacheDir == "" {
		spec.CacheDir = artifactPaths["cache"]
	}
	return spec
}

func createDatasetArtifacts(spec workflows.CreateDatasetSpec) []jobs.Artifact {
	artifacts := make([]jobs.Artifact, 0, len(spec.DocumentPaths)+6)
	for idx, path := range spec.DocumentPaths {
		artifacts = append(artifacts, jobs.Artifact{
			Name:        fmt.Sprintf("document_%d", idx+1),
			Role:        "input",
			Kind:        "source_document",
			Dataset:     spec.Dataset,
			Path:        path,
			Status:      "configured",
			Description: "Raw source document parsed by the parse_documents step.",
		})
	}
	if spec.SchemaPath != "" {
		artifacts = append(artifacts, jobs.Artifact{
			Name:        "schema_source",
			Role:        "input",
			Kind:        "schema_json",
			Dataset:     spec.Dataset,
			Path:        spec.SchemaPath,
			Status:      "configured",
			Description: "Source schema imported for this dataset.",
		})
	}
	artifacts = append(artifacts,
		jobs.Artifact{
			Name:          "parsed_corpus",
			Role:          "output",
			Kind:          "corpus_json",
			SchemaVersion: "corpus-json/v1",
			Dataset:       spec.Dataset,
			Path:          spec.ParseOutputPath,
			Status:        "pending",
			Description:   "Corpus JSON produced by parse_documents and imported into the dataset.",
		},
		jobs.Artifact{
			Name:          "corpus",
			Role:          "output",
			Kind:          "corpus_json",
			SchemaVersion: "corpus-json/v1",
			Dataset:       spec.Dataset,
			Path:          spec.CorpusOutputPath,
			Status:        "pending",
			Description:   "Managed corpus JSON imported for this dataset.",
		},
		jobs.Artifact{
			Name:          "schema",
			Role:          "output",
			Kind:          "schema_json",
			SchemaVersion: "schema-json/v1",
			Dataset:       spec.Dataset,
			Status:        "pending",
			Description:   "Managed schema JSON imported for this dataset.",
		},
		jobs.Artifact{
			Name:          "metadata",
			Role:          "output",
			Kind:          "dataset_metadata_json",
			SchemaVersion: datasetimport.SchemaVersion,
			Dataset:       spec.Dataset,
			Status:        "pending",
			Description:   "Dataset import metadata written by the import step.",
		},
		jobs.Artifact{
			Name:          "graph",
			Role:          "output",
			Kind:          "graph_json",
			SchemaVersion: "youtu-graph/v1",
			Dataset:       spec.Dataset,
			Path:          spec.GraphOutputPath,
			Status:        "pending",
			Description:   "Knowledge graph written by the build_graph step.",
		},
		jobs.Artifact{
			Name:        "chunks",
			Role:        "output",
			Kind:        "chunks_txt",
			Dataset:     spec.Dataset,
			Path:        spec.ChunksOutputPath,
			Status:      "pending",
			Description: "Chunk text file written by the build_graph step.",
		},
		jobs.Artifact{
			Name:        "cache",
			Role:        "output",
			Kind:        "faiss_cache_dir",
			Dataset:     spec.Dataset,
			Path:        spec.CacheDir,
			Status:      "pending",
			Description: "Vector cache directory prepared by the build_graph step.",
		},
	)
	return artifacts
}

func (s *Service) runBuildAndAnswerWorkflow(ctx context.Context, recorder *workflows.Recorder, spec workflows.BuildAndAnswerSpec) (any, error) {
	buildSpec := s.buildGraphSpec(jobs.BuildGraphSpec{
		Dataset:          spec.Dataset,
		CorpusPath:       spec.CorpusPath,
		SchemaPath:       spec.SchemaPath,
		GraphOutputPath:  spec.GraphOutputPath,
		ChunksOutputPath: spec.ChunksOutputPath,
		CacheDir:         spec.CacheDir,
		ConfigPath:       spec.ConfigPath,
		Mode:             spec.BuildMode,
	})
	buildJob := s.submitBuildGraphJob(buildSpec)
	recorder.StepStarted("build_graph", buildJob)
	buildJob, err := s.waitForJob(ctx, buildJob.ID)
	recorder.StepFinished("build_graph", buildJob)
	if err != nil {
		return nil, err
	}
	if buildJob.Status != jobs.StatusSucceeded {
		return map[string]any{
			"schema_version":     "build-and-answer-result/v1",
			"build_graph_job_id": buildJob.ID,
		}, fmt.Errorf("build_graph step failed: %s", buildJob.Error)
	}
	recorder.Artifact("graph", "written", buildSpec.GraphOutputPath)
	recorder.Artifact("chunks", "written", buildSpec.ChunksOutputPath)
	if buildSpec.CacheDir != "" {
		recorder.Artifact("cache", "written", buildSpec.CacheDir)
	}
	recorder.Event("artifact_handoff", "build_graph graph/chunks artifacts handed to answer")

	answerSpec := s.answerSpec(jobs.AnswerSpec{
		Dataset:    spec.Dataset,
		Question:   spec.Question,
		OutputPath: spec.AnswerOutputPath,
		Mode:       spec.AnswerMode,
		TopK:       spec.TopK,
		GraphPath:  buildSpec.GraphOutputPath,
		ChunksPath: buildSpec.ChunksOutputPath,
		ConfigPath: spec.ConfigPath,
	})
	answerJob := s.submitAnswerJob(answerSpec)
	recorder.StepStarted("answer", answerJob)
	answerJob, err = s.waitForJob(ctx, answerJob.ID)
	recorder.StepFinished("answer", answerJob)
	if err != nil {
		return nil, err
	}
	if answerJob.Status != jobs.StatusSucceeded {
		return map[string]any{
			"schema_version":     "build-and-answer-result/v1",
			"build_graph_job_id": buildJob.ID,
			"answer_job_id":      answerJob.ID,
		}, fmt.Errorf("answer step failed: %s", answerJob.Error)
	}
	recorder.Artifact("answer", "written", answerSpec.OutputPath)

	return map[string]any{
		"schema_version":     "build-and-answer-result/v1",
		"dataset":            spec.Dataset,
		"question":           spec.Question,
		"build_graph_job_id": buildJob.ID,
		"answer_job_id":      answerJob.ID,
		"graph_output_path":  buildSpec.GraphOutputPath,
		"chunks_output_path": buildSpec.ChunksOutputPath,
		"cache_dir":          buildSpec.CacheDir,
		"answer_output_path": answerSpec.OutputPath,
	}, nil
}

func (s *Service) runCreateDatasetWorkflow(ctx context.Context, recorder *workflows.Recorder, spec workflows.CreateDatasetSpec) (any, error) {
	parseSpec := s.parseDocumentsSpec(jobs.ParseDocumentsSpec{
		Dataset:       spec.Dataset,
		DocumentPaths: spec.DocumentPaths,
		OutputPath:    spec.ParseOutputPath,
		ConfigPath:    spec.ConfigPath,
		Mode:          spec.ParseMode,
	})
	parseJob := s.submitParseDocumentsJob(parseSpec)
	recorder.StepStarted("parse_documents", parseJob)
	parseJob, err := s.waitForJob(ctx, parseJob.ID)
	recorder.StepFinished("parse_documents", parseJob)
	if err != nil {
		return nil, err
	}
	if parseJob.Status != jobs.StatusSucceeded {
		return map[string]any{
			"schema_version":         "create-dataset-result/v1",
			"parse_documents_job_id": parseJob.ID,
		}, fmt.Errorf("parse_documents step failed: %s", parseJob.Error)
	}
	recorder.Artifact("parsed_corpus", "written", parseSpec.OutputPath)

	importStarted := time.Now().UTC()
	importJob := jobs.Job{
		Type:      "dataset_import",
		Status:    jobs.StatusRunning,
		StartedAt: &importStarted,
		Artifacts: []jobs.Artifact{
			{
				Name:        "corpus",
				Role:        "input",
				Kind:        "corpus_json",
				Dataset:     spec.Dataset,
				Path:        parseSpec.OutputPath,
				Status:      "configured",
				Description: "Parsed corpus imported into the dataset registry.",
			},
			{
				Name:        "schema_source",
				Role:        "input",
				Kind:        "schema_json",
				Dataset:     spec.Dataset,
				Path:        spec.SchemaPath,
				Status:      "configured",
				Description: "Source schema imported into the dataset registry.",
			},
		},
	}
	recorder.StepStarted("dataset_import", importJob)
	metadata, err := datasetimport.Import(s.config, datasetimport.Request{
		Dataset:    spec.Dataset,
		CorpusPath: parseSpec.OutputPath,
		SchemaPath: spec.SchemaPath,
		Overwrite:  spec.OverwriteImport,
	})
	if err != nil {
		finished := time.Now().UTC()
		importJob.Status = jobs.StatusFailed
		importJob.FinishedAt = &finished
		importJob.Error = err.Error()
		recorder.StepFinished("dataset_import", importJob)
		recorder.Artifact("metadata", "failed", "")
		recorder.Event("dataset_import_failed", err.Error())
		return map[string]any{
			"schema_version":         "create-dataset-result/v1",
			"parse_documents_job_id": parseJob.ID,
		}, fmt.Errorf("dataset import step failed: %w", err)
	}
	metadataPath := ""
	importedCorpusPath := parseSpec.OutputPath
	importedSchemaPath := spec.SchemaPath
	importJob.Artifacts = append(importJob.Artifacts, metadata.Artifacts...)
	for _, artifact := range metadata.Artifacts {
		switch artifact.Name {
		case "corpus":
			importedCorpusPath = artifact.Path
			recorder.Artifact("corpus", "written", artifact.Path)
		case "schema":
			importedSchemaPath = artifact.Path
			recorder.Artifact("schema", "written", artifact.Path)
		case "metadata":
			metadataPath = artifact.Path
			recorder.Artifact("metadata", "written", artifact.Path)
		}
	}
	finished := time.Now().UTC()
	importJob.Status = jobs.StatusSucceeded
	importJob.FinishedAt = &finished
	recorder.StepFinished("dataset_import", importJob)
	recorder.Event("dataset_imported", "dataset corpus/schema metadata imported")
	recorder.Event("artifact_handoff", "parse_documents corpus imported for build_graph")

	buildSpec := s.buildGraphSpec(jobs.BuildGraphSpec{
		Dataset:          spec.Dataset,
		CorpusPath:       importedCorpusPath,
		SchemaPath:       importedSchemaPath,
		GraphOutputPath:  spec.GraphOutputPath,
		ChunksOutputPath: spec.ChunksOutputPath,
		CacheDir:         spec.CacheDir,
		ConfigPath:       spec.ConfigPath,
		Mode:             spec.BuildMode,
	})
	buildJob := s.submitBuildGraphJob(buildSpec)
	recorder.StepStarted("build_graph", buildJob)
	buildJob, err = s.waitForJob(ctx, buildJob.ID)
	recorder.StepFinished("build_graph", buildJob)
	if err != nil {
		return nil, err
	}
	if buildJob.Status != jobs.StatusSucceeded {
		return map[string]any{
			"schema_version":         "create-dataset-result/v1",
			"parse_documents_job_id": parseJob.ID,
			"build_graph_job_id":     buildJob.ID,
			"metadata_path":          metadataPath,
		}, fmt.Errorf("build_graph step failed: %s", buildJob.Error)
	}
	recorder.Artifact("graph", "written", buildSpec.GraphOutputPath)
	recorder.Artifact("chunks", "written", buildSpec.ChunksOutputPath)
	if buildSpec.CacheDir != "" {
		recorder.Artifact("cache", "written", buildSpec.CacheDir)
	}

	return map[string]any{
		"schema_version":         "create-dataset-result/v1",
		"dataset":                spec.Dataset,
		"parse_documents_job_id": parseJob.ID,
		"build_graph_job_id":     buildJob.ID,
		"corpus_path":            buildSpec.CorpusPath,
		"schema_path":            buildSpec.SchemaPath,
		"metadata_path":          metadataPath,
		"graph_output_path":      buildSpec.GraphOutputPath,
		"chunks_output_path":     buildSpec.ChunksOutputPath,
		"cache_dir":              buildSpec.CacheDir,
	}, nil
}

func (s *Service) waitForJob(ctx context.Context, id string) (jobs.Job, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := s.jobs.Get(id)
		if err != nil {
			return jobs.Job{}, err
		}
		if isJobTerminal(job.Status) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			_, _, _ = s.jobs.Cancel(id)
			for attempt := 0; attempt < 200; attempt++ {
				job, err := s.jobs.Get(id)
				if err != nil {
					return jobs.Job{}, err
				}
				if isJobTerminal(job.Status) {
					return job, ctx.Err()
				}
				time.Sleep(10 * time.Millisecond)
			}
			job, _ := s.jobs.Get(id)
			return job, ctx.Err()
		case <-ticker.C:
		}
	}
}

func isJobTerminal(status jobs.Status) bool {
	return status == jobs.StatusSucceeded || status == jobs.StatusFailed || status == jobs.StatusCanceled
}

func (s *Service) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	workflows := s.workflows.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "workflow-list/v1",
		"count":          len(workflows),
		"workflows":      workflows,
	})
}

func (s *Service) handleWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow, err := s.workflows.Get(r.PathValue("workflow_id"))
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workflow)
}

func (s *Service) handleWorkflowSteps(w http.ResponseWriter, r *http.Request) {
	steps, err := s.workflows.Steps(r.PathValue("workflow_id"))
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "workflow-steps/v1",
		"workflow_id":    r.PathValue("workflow_id"),
		"count":          len(steps),
		"steps":          steps,
	})
}

func (s *Service) handleWorkflowStep(w http.ResponseWriter, r *http.Request) {
	step, err := s.workflows.Step(r.PathValue("workflow_id"), r.PathValue("step_name"))
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "workflow-step/v1",
		"workflow_id":    r.PathValue("workflow_id"),
		"step":           step,
	})
}

func (s *Service) handleWorkflowEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.workflows.Events(r.PathValue("workflow_id"))
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workflow_id": r.PathValue("workflow_id"),
		"events":      events,
	})
}

func (s *Service) handleCancelWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow, canceled, err := s.workflows.Cancel(r.PathValue("workflow_id"))
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"canceled": canceled,
		"workflow": workflow,
	})
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

func writeWorkflowError(w http.ResponseWriter, err error) {
	if errors.Is(err, workflows.ErrNotFound) {
		writeError(w, http.StatusNotFound, "workflow_not_found", err)
		return
	}
	if errors.Is(err, workflows.ErrStepNotFound) {
		writeError(w, http.StatusNotFound, "workflow_step_not_found", err)
		return
	}
	writeError(w, http.StatusInternalServerError, "workflow_failed", err)
}

func writeDatasetImportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, datasetimport.ErrInvalidDataset):
		writeError(w, http.StatusBadRequest, "invalid_dataset", err)
	case errors.Is(err, datasetimport.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "dataset_exists", err)
	default:
		writeError(w, http.StatusBadRequest, "dataset_import_failed", err)
	}
}

func writeDatasetDeleteError(w http.ResponseWriter, result datasetimport.DeleteResult, err error) {
	var deleteErr datasetimport.DeleteError
	switch {
	case errors.As(err, &deleteErr):
		writeJSON(w, http.StatusInternalServerError, deleteErr.Result)
	case errors.Is(err, datasetimport.ErrInvalidDataset):
		writeError(w, http.StatusBadRequest, "invalid_dataset", err)
	case errors.Is(err, datasetimport.ErrNotManaged):
		writeError(w, http.StatusNotFound, "dataset_not_managed", err)
	case errors.Is(err, datasetimport.ErrDeleteFailed):
		if result.SchemaVersion != "" {
			writeJSON(w, http.StatusInternalServerError, result)
			return
		}
		writeError(w, http.StatusInternalServerError, "dataset_delete_failed", err)
	default:
		writeError(w, http.StatusInternalServerError, "dataset_delete_failed", err)
	}
}

func writeDatasetRebuildError(w http.ResponseWriter, err error) {
	var rebuildErr datasetimport.RebuildError
	switch {
	case errors.As(err, &rebuildErr):
		writeJSON(w, http.StatusConflict, rebuildErr.Result)
	case errors.Is(err, datasetimport.ErrInvalidDataset):
		writeError(w, http.StatusBadRequest, "invalid_dataset", err)
	case errors.Is(err, datasetimport.ErrNotManaged):
		writeError(w, http.StatusNotFound, "dataset_not_managed", err)
	case errors.Is(err, datasetimport.ErrMissingArtifact):
		writeError(w, http.StatusConflict, "dataset_not_ready", err)
	case errors.Is(err, datasetimport.ErrRebuildConflict):
		writeError(w, http.StatusConflict, "dataset_rebuild_conflict", err)
	default:
		writeError(w, http.StatusInternalServerError, "dataset_rebuild_failed", err)
	}
}

func writeSchemaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, schemas.ErrInvalidDataset):
		writeError(w, http.StatusBadRequest, "invalid_dataset", err)
	case errors.Is(err, schemas.ErrDuplicateItem):
		writeError(w, http.StatusBadRequest, "duplicate_schema_item", err)
	case errors.Is(err, schemas.ErrInvalidSchema):
		writeError(w, http.StatusBadRequest, "invalid_schema", err)
	case errors.Is(err, schemas.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "schema_exists", err)
	case errors.Is(err, schemas.ErrNotFound):
		writeError(w, http.StatusNotFound, "schema_not_found", err)
	case errors.Is(err, schemas.ErrWriteFailed):
		writeError(w, http.StatusInternalServerError, "schema_write_failed", err)
	default:
		writeError(w, http.StatusInternalServerError, "schema_management_failed", err)
	}
}

func writeDatasetOperationError(w http.ResponseWriter, err error) {
	if errors.Is(err, datasetops.ErrNotFound) {
		writeError(w, http.StatusNotFound, "dataset_operation_not_found", err)
		return
	}
	writeError(w, http.StatusInternalServerError, "dataset_operation_failed", err)
}

func (s *Service) recordDatasetOperation(op datasetops.Operation) {
	if s.ops == nil || op.Dataset == "" || op.Type == "" {
		return
	}
	s.ops.Append(op)
}

func operationStatusFromDatasetDelete(result datasetimport.DeleteResult) string {
	switch result.Status {
	case "skipped":
		return "planned"
	case "failed":
		return "failed"
	default:
		return "succeeded"
	}
}

func cleanupRebuildOutputs(artifacts []jobs.Artifact) error {
	for _, artifact := range artifacts {
		if artifact.Role != "output" || artifact.Path == "" {
			continue
		}
		if artifact.Name != "graph" && artifact.Name != "chunks" && artifact.Name != "cache" {
			continue
		}
		if err := os.RemoveAll(artifact.Path); err != nil {
			return fmt.Errorf("delete rebuild artifact %s at %s: %w", artifact.Name, artifact.Path, err)
		}
	}
	return nil
}

func queryBoolDefault(r *http.Request, key string, fallback bool) bool {
	value := r.URL.Query().Get(key)
	switch value {
	case "":
		return fallback
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	case "0", "false", "FALSE", "False", "no", "NO", "off", "OFF":
		return false
	default:
		return fallback
	}
}
