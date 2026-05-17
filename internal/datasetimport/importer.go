package datasetimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fuzzy-searcher-go/internal/config"
	"github.com/fuzzy-searcher-go/internal/jobs"
)

const SchemaVersion = "dataset-import/v1"

var datasetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ErrAlreadyExists means one or more managed import artifacts already exist.
var ErrAlreadyExists = errors.New("dataset import already exists")

// ErrInvalidDataset means the dataset name is unsafe or unsupported.
var ErrInvalidDataset = errors.New("invalid dataset")

// ErrNotManaged means dataset metadata is missing and force was not requested.
var ErrNotManaged = errors.New("dataset is not managed")

// ErrDeleteFailed means one or more managed artifacts could not be deleted.
var ErrDeleteFailed = errors.New("dataset delete failed")

// ErrMissingArtifact means a required managed artifact is absent.
var ErrMissingArtifact = errors.New("missing managed artifact")

// ErrRebuildConflict means outputs exist but overwrite was disabled.
var ErrRebuildConflict = errors.New("dataset rebuild conflict")

// Request is the stable dataset import request.
type Request struct {
	Dataset    string `json:"dataset"`
	CorpusPath string `json:"corpus_path"`
	SchemaPath string `json:"schema_path"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}

// Metadata is persisted for each imported dataset.
type Metadata struct {
	SchemaVersion string          `json:"schema_version"`
	Dataset       string          `json:"dataset"`
	Status        string          `json:"status"`
	ImportedAt    time.Time       `json:"imported_at"`
	Source        Source          `json:"source"`
	Artifacts     []jobs.Artifact `json:"artifacts"`
}

// Source records the external source paths used by the import.
type Source struct {
	CorpusPath string `json:"corpus_path"`
	SchemaPath string `json:"schema_path"`
}

// DeleteResult is the stable response for deleting service-managed dataset
// artifacts.
type DeleteResult struct {
	SchemaVersion string          `json:"schema_version"`
	Dataset       string          `json:"dataset"`
	Status        string          `json:"status"`
	DryRun        bool            `json:"dry_run"`
	IncludeOutput bool            `json:"include_outputs"`
	DeletedAt     time.Time       `json:"deleted_at"`
	Artifacts     []jobs.Artifact `json:"artifacts"`
	Errors        []string        `json:"errors"`
}

// DeleteRequest configures safe managed dataset deletion.
type DeleteRequest struct {
	Dataset       string
	IncludeOutput bool
	DryRun        bool
	Force         bool
}

// RebuildRequest configures a rebuild operation for an existing managed
// dataset.
type RebuildRequest struct {
	Dataset          string
	OverwriteOutput  bool
	DryRun           bool
	ConfigPath       string
	Mode             string
	GraphOutputPath  string
	ChunksOutputPath string
	CacheDir         string
	PythonBin        string
	ScriptPath       string
	WorkingDir       string
}

// RebuildPlan is the stable response shape for dataset rebuild requests.
type RebuildPlan struct {
	SchemaVersion   string          `json:"schema_version"`
	Dataset         string          `json:"dataset"`
	Status          string          `json:"status"`
	DryRun          bool            `json:"dry_run"`
	OverwriteOutput bool            `json:"overwrite_outputs"`
	JobID           string          `json:"job_id,omitempty"`
	JobType         string          `json:"job_type,omitempty"`
	Artifacts       []jobs.Artifact `json:"artifacts"`
	Errors          []string        `json:"errors"`
}

// DeleteError carries a failed delete result with per-artifact statuses.
type DeleteError struct {
	Result DeleteResult
	Err    error
}

func (e DeleteError) Error() string {
	return e.Err.Error()
}

func (e DeleteError) Unwrap() error {
	return e.Err
}

// RebuildError carries a rebuild plan with per-artifact conflict status.
type RebuildError struct {
	Result RebuildPlan
	Err    error
}

func (e RebuildError) Error() string {
	return e.Err.Error()
}

func (e RebuildError) Unwrap() error {
	return e.Err
}

// Import copies a corpus/schema pair into the service-managed artifact roots
// and writes a durable metadata record.
func Import(cfg config.Config, req Request) (Metadata, error) {
	if err := validateRequest(req); err != nil {
		return Metadata{}, err
	}
	if err := validateJSONFile(req.CorpusPath, "corpus"); err != nil {
		return Metadata{}, err
	}
	if err := validateJSONFile(req.SchemaPath, "schema"); err != nil {
		return Metadata{}, err
	}

	paths := managedPaths(cfg, req.Dataset)
	if !req.Overwrite {
		if existing := firstExisting(paths.corpus, paths.schema, paths.metadata); existing != "" {
			return Metadata{}, fmt.Errorf("%w: %s", ErrAlreadyExists, existing)
		}
	}
	if err := copyFile(req.CorpusPath, paths.corpus); err != nil {
		return Metadata{}, err
	}
	if err := copyFile(req.SchemaPath, paths.schema); err != nil {
		return Metadata{}, err
	}
	now := time.Now().UTC()
	metadata := Metadata{
		SchemaVersion: SchemaVersion,
		Dataset:       req.Dataset,
		Status:        "imported",
		ImportedAt:    now,
		Source: Source{
			CorpusPath: req.CorpusPath,
			SchemaPath: req.SchemaPath,
		},
		Artifacts: []jobs.Artifact{
			{
				Name:        "corpus",
				Role:        "input",
				Kind:        "corpus_json",
				Dataset:     req.Dataset,
				Path:        paths.corpus,
				Status:      "written",
				Description: "Service-managed corpus imported for this dataset.",
			},
			{
				Name:        "schema",
				Role:        "input",
				Kind:        "schema_json",
				Dataset:     req.Dataset,
				Path:        paths.schema,
				Status:      "written",
				Description: "Service-managed schema imported for this dataset.",
			},
			{
				Name:          "metadata",
				Role:          "output",
				Kind:          "dataset_metadata_json",
				SchemaVersion: SchemaVersion,
				Dataset:       req.Dataset,
				Path:          paths.metadata,
				Status:        "written",
				Description:   "Dataset import metadata persisted by the Go service.",
			},
		},
	}
	if err := writeMetadata(paths.metadata, metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

// Delete removes only service-managed artifacts for a dataset. It intentionally
// avoids external source paths and legacy corpus layouts.
func Delete(cfg config.Config, req DeleteRequest) (DeleteResult, error) {
	if !datasetNamePattern.MatchString(req.Dataset) {
		return DeleteResult{}, fmt.Errorf("%w: dataset must match %s", ErrInvalidDataset, datasetNamePattern.String())
	}
	paths := managedPaths(cfg, req.Dataset)
	if _, err := os.Stat(paths.metadata); err != nil && !req.Force {
		if errors.Is(err, os.ErrNotExist) {
			return DeleteResult{}, fmt.Errorf("%w: %s", ErrNotManaged, req.Dataset)
		}
		return DeleteResult{}, fmt.Errorf("stat metadata %s: %w", paths.metadata, err)
	}
	candidates := managedDeleteArtifacts(cfg, req.Dataset)
	artifacts := make([]jobs.Artifact, 0, len(candidates))
	deleteErrors := []string{}
	for _, candidate := range candidates {
		if candidate.Role == "output" && !isCoreDatasetArtifact(candidate.Name) && !req.IncludeOutput {
			artifact := candidate
			artifact.Status = "skipped"
			artifacts = append(artifacts, artifact)
			continue
		}
		status := "missing"
		if _, err := os.Stat(candidate.Path); err == nil {
			if req.DryRun {
				status = "skipped"
			} else if err := os.RemoveAll(candidate.Path); err != nil {
				status = "failed"
				deleteErrors = append(deleteErrors, fmt.Sprintf("delete %s artifact %s: %v", candidate.Name, candidate.Path, err))
			} else {
				status = "deleted"
				if candidate.Name == "corpus" {
					removeEmptyParent(filepath.Dir(candidate.Path))
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			status = "failed"
			deleteErrors = append(deleteErrors, fmt.Sprintf("stat %s artifact %s: %v", candidate.Name, candidate.Path, err))
		}
		artifact := candidate
		artifact.Status = status
		artifacts = append(artifacts, artifact)
	}
	status := "deleted"
	if req.DryRun {
		status = "skipped"
	}
	if len(deleteErrors) > 0 {
		status = "failed"
	}
	result := DeleteResult{
		SchemaVersion: "dataset-delete/v1",
		Dataset:       req.Dataset,
		Status:        status,
		DryRun:        req.DryRun,
		IncludeOutput: req.IncludeOutput,
		DeletedAt:     time.Now().UTC(),
		Artifacts:     artifacts,
		Errors:        deleteErrors,
	}
	if len(deleteErrors) > 0 {
		return result, DeleteError{
			Result: result,
			Err:    fmt.Errorf("%w: %s", ErrDeleteFailed, req.Dataset),
		}
	}
	return result, nil
}

// PlanRebuild validates a managed dataset and returns the build_graph spec plus
// cleanup artifact plan. The caller owns job submission.
func PlanRebuild(cfg config.Config, req RebuildRequest) (RebuildPlan, jobs.BuildGraphSpec, error) {
	if !datasetNamePattern.MatchString(req.Dataset) {
		return RebuildPlan{}, jobs.BuildGraphSpec{}, fmt.Errorf("%w: dataset must match %s", ErrInvalidDataset, datasetNamePattern.String())
	}
	paths := managedPaths(cfg, req.Dataset)
	if _, err := os.Stat(paths.metadata); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RebuildPlan{}, jobs.BuildGraphSpec{}, fmt.Errorf("%w: %s", ErrNotManaged, req.Dataset)
		}
		return RebuildPlan{}, jobs.BuildGraphSpec{}, fmt.Errorf("stat metadata %s: %w", paths.metadata, err)
	}
	missing := []string{}
	if _, err := os.Stat(paths.corpus); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, "corpus")
		} else {
			return RebuildPlan{}, jobs.BuildGraphSpec{}, fmt.Errorf("stat corpus %s: %w", paths.corpus, err)
		}
	}
	if _, err := os.Stat(paths.schema); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, "schema")
		} else {
			return RebuildPlan{}, jobs.BuildGraphSpec{}, fmt.Errorf("stat schema %s: %w", paths.schema, err)
		}
	}
	if len(missing) > 0 {
		return RebuildPlan{}, jobs.BuildGraphSpec{}, fmt.Errorf("%w: %s", ErrMissingArtifact, strings.Join(missing, ","))
	}
	spec := jobs.BuildGraphSpec{
		Dataset:          req.Dataset,
		CorpusPath:       paths.corpus,
		SchemaPath:       paths.schema,
		GraphOutputPath:  choosePath(req.GraphOutputPath, filepath.Join(cfg.GraphRoot, req.Dataset+"_new.json")),
		ChunksOutputPath: choosePath(req.ChunksOutputPath, filepath.Join(cfg.ChunksRoot, req.Dataset+".txt")),
		CacheDir:         choosePath(req.CacheDir, filepath.Join(cfg.CacheRoot, req.Dataset)),
		ConfigPath:       req.ConfigPath,
		Mode:             req.Mode,
		PythonBin:        req.PythonBin,
		ScriptPath:       req.ScriptPath,
		WorkingDir:       req.WorkingDir,
	}
	artifacts := rebuildArtifacts(req, spec)
	conflicts := []string{}
	for _, artifact := range artifacts {
		if artifact.Status == "conflict" {
			conflicts = append(conflicts, fmt.Sprintf("%s output exists: %s", artifact.Name, artifact.Path))
		}
	}
	status := "queued"
	if req.DryRun {
		status = "planned"
	}
	plan := RebuildPlan{
		SchemaVersion:   "dataset-rebuild/v1",
		Dataset:         req.Dataset,
		Status:          status,
		DryRun:          req.DryRun,
		OverwriteOutput: req.OverwriteOutput,
		JobType:         jobs.TypeBuildGraph,
		Artifacts:       artifacts,
		Errors:          conflicts,
	}
	if len(conflicts) > 0 {
		plan.Status = "conflict"
		return plan, jobs.BuildGraphSpec{}, RebuildError{
			Result: plan,
			Err:    fmt.Errorf("%w: %s", ErrRebuildConflict, req.Dataset),
		}
	}
	return plan, spec, nil
}

func validateRequest(req Request) error {
	if !datasetNamePattern.MatchString(req.Dataset) {
		return fmt.Errorf("%w: dataset must match %s", ErrInvalidDataset, datasetNamePattern.String())
	}
	if req.CorpusPath == "" {
		return errors.New("corpus_path is required")
	}
	if req.SchemaPath == "" {
		return errors.New("schema_path is required")
	}
	return nil
}

func validateJSONFile(path string, label string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s json: %w", label, err)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return fmt.Errorf("parse %s json: %w", label, err)
	}
	return nil
}

type paths struct {
	corpus   string
	schema   string
	metadata string
}

func managedPaths(cfg config.Config, dataset string) paths {
	return paths{
		corpus:   filepath.Join(cfg.CorpusRoot, "uploaded", dataset, "corpus.json"),
		schema:   filepath.Join(cfg.SchemaRoot, dataset+".json"),
		metadata: filepath.Join(datasetMetaRoot(cfg), dataset+".json"),
	}
}

func datasetMetaRoot(cfg config.Config) string {
	if cfg.DatasetMetaRoot != "" {
		return cfg.DatasetMetaRoot
	}
	return filepath.Join(cfg.ArtifactRoot, "output", "datasets")
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination dir %s: %w", filepath.Dir(dst), err)
	}
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create destination %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination %s: %w", tmp, err)
	}
	return os.Rename(tmp, dst)
}

func managedDeleteArtifacts(cfg config.Config, dataset string) []jobs.Artifact {
	paths := managedPaths(cfg, dataset)
	return []jobs.Artifact{
		{
			Name:        "corpus",
			Role:        "input",
			Kind:        "corpus_json",
			Dataset:     dataset,
			Path:        paths.corpus,
			Description: "Service-managed uploaded corpus for this dataset.",
		},
		{
			Name:        "schema",
			Role:        "input",
			Kind:        "schema_json",
			Dataset:     dataset,
			Path:        paths.schema,
			Description: "Service-managed schema for this dataset.",
		},
		{
			Name:          "metadata",
			Role:          "output",
			Kind:          "dataset_metadata_json",
			SchemaVersion: SchemaVersion,
			Dataset:       dataset,
			Path:          paths.metadata,
			Description:   "Dataset import metadata persisted by the Go service.",
		},
		{
			Name:          "graph",
			Role:          "output",
			Kind:          "graph_json",
			SchemaVersion: "youtu-graph/v1",
			Dataset:       dataset,
			Path:          filepath.Join(cfg.GraphRoot, dataset+"_new.json"),
			Description:   "Knowledge graph produced for this dataset.",
		},
		{
			Name:        "chunks",
			Role:        "output",
			Kind:        "chunks_txt",
			Dataset:     dataset,
			Path:        filepath.Join(cfg.ChunksRoot, dataset+".txt"),
			Description: "Chunk text file produced for this dataset.",
		},
		{
			Name:        "cache",
			Role:        "output",
			Kind:        "faiss_cache_dir",
			Dataset:     dataset,
			Path:        filepath.Join(cfg.CacheRoot, dataset),
			Description: "Vector cache directory for this dataset.",
		},
		{
			Name:          "golden",
			Role:          "output",
			Kind:          "retriever_golden_json",
			SchemaVersion: "retriever-golden/v1",
			Dataset:       dataset,
			Path:          filepath.Join(cfg.GoldenRoot, dataset+".json"),
			Description:   "Retriever golden fixture for this dataset.",
		},
		{
			Name:          "triple_trace",
			Role:          "output",
			Kind:          "triple_trace_json",
			SchemaVersion: "triple-trace/v1",
			Dataset:       dataset,
			Path:          filepath.Join(cfg.TraceRoot, dataset+"_triple_trace.json"),
			Description:   "Python-authoritative triple trace for this dataset.",
		},
		{
			Name:          "answer",
			Role:          "output",
			Kind:          "answer_json",
			SchemaVersion: "answer-output/v1",
			Dataset:       dataset,
			Path:          filepath.Join(cfg.ArtifactRoot, "output", "answers", dataset+".json"),
			Description:   "Answer output for this dataset.",
		},
	}
}

func rebuildArtifacts(req RebuildRequest, spec jobs.BuildGraphSpec) []jobs.Artifact {
	artifacts := []jobs.Artifact{
		{
			Name:        "corpus",
			Role:        "input",
			Kind:        "corpus_json",
			Dataset:     spec.Dataset,
			Path:        spec.CorpusPath,
			Status:      "configured",
			Description: "Service-managed corpus consumed by the rebuild build_graph job.",
		},
		{
			Name:        "schema",
			Role:        "input",
			Kind:        "schema_json",
			Dataset:     spec.Dataset,
			Path:        spec.SchemaPath,
			Status:      "configured",
			Description: "Service-managed schema consumed by the rebuild build_graph job.",
		},
		{
			Name:          "graph",
			Role:          "output",
			Kind:          "graph_json",
			SchemaVersion: "youtu-graph/v1",
			Dataset:       spec.Dataset,
			Path:          spec.GraphOutputPath,
			Description:   "Graph output rebuilt by build_graph.",
		},
		{
			Name:        "chunks",
			Role:        "output",
			Kind:        "chunks_txt",
			Dataset:     spec.Dataset,
			Path:        spec.ChunksOutputPath,
			Description: "Chunks output rebuilt by build_graph.",
		},
		{
			Name:        "cache",
			Role:        "output",
			Kind:        "faiss_cache_dir",
			Dataset:     spec.Dataset,
			Path:        spec.CacheDir,
			Description: "Vector cache output rebuilt by build_graph.",
		},
	}
	for idx := range artifacts {
		if artifacts[idx].Role == "output" {
			artifacts[idx].Status = rebuildOutputStatus(req, artifacts[idx].Path)
		}
	}
	return artifacts
}

func rebuildOutputStatus(req RebuildRequest, path string) string {
	if path == "" {
		return "missing"
	}
	_, err := os.Stat(path)
	exists := err == nil
	if req.DryRun {
		if exists {
			return "skipped"
		}
		return "missing"
	}
	if !req.OverwriteOutput && exists {
		return "conflict"
	}
	return "pending"
}

func choosePath(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func isCoreDatasetArtifact(name string) bool {
	return name == "corpus" || name == "schema" || name == "metadata"
}

func removeEmptyParent(path string) {
	_ = os.Remove(path)
}

func writeMetadata(path string, metadata Metadata) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
