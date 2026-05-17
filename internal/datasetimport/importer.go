package datasetimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
