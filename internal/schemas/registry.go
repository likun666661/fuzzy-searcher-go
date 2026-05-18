package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fuzzy-searcher-go/internal/config"
	"github.com/fuzzy-searcher-go/internal/jobs"
)

const (
	SchemaVersion         = "dataset-schema/v1"
	UpdateSchemaVersion   = "dataset-schema-update/v1"
	MetadataSchemaVersion = "dataset-schema-metadata/v1"
	ValidationVersion     = "schema-validation/v1"
)

var datasetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var (
	ErrInvalidDataset = errors.New("invalid dataset")
	ErrNotFound       = errors.New("schema not found")
	ErrInvalidSchema  = errors.New("invalid schema")
	ErrDuplicateItem  = errors.New("duplicate schema item")
	ErrAlreadyExists  = errors.New("schema already exists")
	ErrWriteFailed    = errors.New("schema write failed")
)

type PutRequest struct {
	Dataset    string
	Schema     json.RawMessage
	Overwrite  bool
	SourcePath string
}

type ValidationResult struct {
	SchemaVersion string   `json:"schema_version"`
	Valid         bool     `json:"valid"`
	Summary       Summary  `json:"summary"`
	Errors        []string `json:"errors"`
}

type Record struct {
	SchemaVersion string          `json:"schema_version"`
	Dataset       string          `json:"dataset"`
	Status        string          `json:"status"`
	Path          string          `json:"path,omitempty"`
	Fallback      bool            `json:"fallback"`
	Version       int             `json:"version,omitempty"`
	Hash          string          `json:"hash,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at,omitempty"`
	Summary       Summary         `json:"summary,omitempty"`
	Schema        json.RawMessage `json:"schema,omitempty"`
	Artifact      jobs.Artifact   `json:"artifact,omitempty"`
}

type UpdateResult struct {
	SchemaVersion string          `json:"schema_version"`
	Dataset       string          `json:"dataset"`
	Status        string          `json:"status"`
	Path          string          `json:"path"`
	Version       int             `json:"version"`
	Hash          string          `json:"hash"`
	Summary       Summary         `json:"summary"`
	Artifacts     []jobs.Artifact `json:"artifacts"`
}

type Metadata struct {
	SchemaVersion string    `json:"schema_version"`
	Dataset       string    `json:"dataset"`
	Version       int       `json:"version"`
	Hash          string    `json:"hash"`
	Path          string    `json:"path"`
	Fallback      bool      `json:"fallback"`
	UpdatedAt     time.Time `json:"updated_at"`
	Source        Source    `json:"source"`
	Summary       Summary   `json:"summary"`
}

type Source struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}

type Summary struct {
	Nodes      int `json:"nodes"`
	Relations  int `json:"relations"`
	Attributes int `json:"attributes"`
}

// Validate checks schema shape without writing artifacts.
func Validate(schema json.RawMessage) ValidationResult {
	_, summary, err := canonicalSchema(schema)
	if err != nil {
		return ValidationResult{
			SchemaVersion: ValidationVersion,
			Valid:         false,
			Errors:        []string{err.Error()},
		}
	}
	return ValidationResult{
		SchemaVersion: ValidationVersion,
		Valid:         true,
		Summary:       summary,
		Errors:        []string{},
	}
}

func List(cfg config.Config) ([]Record, error) {
	entries, err := os.ReadDir(cfg.SchemaRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Record{}, nil
		}
		return nil, err
	}
	out := []Record{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		dataset := strings.TrimSuffix(entry.Name(), ".json")
		if !datasetNamePattern.MatchString(dataset) {
			continue
		}
		record, err := Get(cfg, dataset, GetOptions{AllowFallback: false, IncludeBody: false})
		if err == nil {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Dataset < out[j].Dataset
	})
	return out, nil
}

type GetOptions struct {
	AllowFallback bool
	IncludeBody   bool
}

func Get(cfg config.Config, dataset string, opts GetOptions) (Record, error) {
	if err := validateDataset(dataset); err != nil {
		return Record{}, err
	}
	path := schemaPath(cfg, dataset)
	if exists(path) {
		return recordFromFile(cfg, dataset, path, false, opts.IncludeBody)
	}
	if opts.AllowFallback {
		fallbackPath := defaultSchemaPath(cfg)
		if exists(fallbackPath) {
			return recordFromFile(cfg, dataset, fallbackPath, true, opts.IncludeBody)
		}
	}
	return Record{}, fmt.Errorf("%w: %s", ErrNotFound, dataset)
}

func Put(cfg config.Config, req PutRequest) (UpdateResult, error) {
	if err := validateDataset(req.Dataset); err != nil {
		return UpdateResult{}, err
	}
	canonical, summary, err := canonicalSchema(req.Schema)
	if err != nil {
		return UpdateResult{}, err
	}
	path := schemaPath(cfg, req.Dataset)
	if !req.Overwrite && exists(path) {
		return UpdateResult{}, fmt.Errorf("%w: %s", ErrAlreadyExists, path)
	}
	previousVersion := previousMetadataVersion(cfg, req.Dataset)
	now := time.Now().UTC()
	hash := schemaHash(canonical)
	metadataPath := metadataPath(cfg, req.Dataset)
	metadata := Metadata{
		SchemaVersion: MetadataSchemaVersion,
		Dataset:       req.Dataset,
		Version:       previousVersion + 1,
		Hash:          hash,
		Path:          path,
		Fallback:      false,
		UpdatedAt:     now,
		Source: Source{
			Kind: "upload",
			Path: req.SourcePath,
		},
		Summary: summary,
	}
	if err := writeSchemaAndMetadata(path, canonical, metadataPath, metadata); err != nil {
		return UpdateResult{}, fmt.Errorf("%w: %v", ErrWriteFailed, err)
	}
	return UpdateResult{
		SchemaVersion: UpdateSchemaVersion,
		Dataset:       req.Dataset,
		Status:        "written",
		Path:          path,
		Version:       metadata.Version,
		Hash:          hash,
		Summary:       summary,
		Artifacts: []jobs.Artifact{
			{
				Name:          "schema",
				Role:          "input",
				Kind:          "schema_json",
				SchemaVersion: SchemaVersion,
				Dataset:       req.Dataset,
				Path:          path,
				Status:        "written",
				Description:   "Managed dataset schema JSON.",
			},
			{
				Name:          "schema_metadata",
				Role:          "metadata",
				Kind:          "dataset_schema_metadata_json",
				SchemaVersion: MetadataSchemaVersion,
				Dataset:       req.Dataset,
				Path:          metadataPath,
				Status:        "written",
				Description:   "Managed dataset schema metadata.",
			},
		},
	}, nil
}

func validateDataset(dataset string) error {
	if !datasetNamePattern.MatchString(dataset) {
		return fmt.Errorf("%w: dataset must match %s", ErrInvalidDataset, datasetNamePattern.String())
	}
	return nil
}

func canonicalSchema(body json.RawMessage) ([]byte, Summary, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, Summary{}, fmt.Errorf("%w: schema is required", ErrInvalidSchema)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, Summary{}, fmt.Errorf("%w: parse schema json: %v", ErrInvalidSchema, err)
	}
	counts := map[string]int{}
	for _, key := range []string{"Nodes", "Relations", "Attributes"} {
		raw, ok := value[key]
		if !ok {
			return nil, Summary{}, fmt.Errorf("%w: %s is required", ErrInvalidSchema, key)
		}
		items, ok := raw.([]any)
		if !ok || len(items) == 0 {
			return nil, Summary{}, fmt.Errorf("%w: %s must be a non-empty array of strings", ErrInvalidSchema, key)
		}
		seen := map[string]struct{}{}
		normalized := make([]string, 0, len(items))
		for idx, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, Summary{}, fmt.Errorf("%w: %s[%d] must be a string", ErrInvalidSchema, key, idx)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				return nil, Summary{}, fmt.Errorf("%w: %s[%d] must be non-empty", ErrInvalidSchema, key, idx)
			}
			if _, ok := seen[text]; ok {
				return nil, Summary{}, fmt.Errorf("%w: %s contains duplicate %q", ErrDuplicateItem, key, text)
			}
			seen[text] = struct{}{}
			normalized = append(normalized, text)
		}
		counts[key] = len(normalized)
		value[key] = normalized
	}
	canonical, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, Summary{}, err
	}
	return append(canonical, '\n'), Summary{
		Nodes:      counts["Nodes"],
		Relations:  counts["Relations"],
		Attributes: counts["Attributes"],
	}, nil
}

func recordFromFile(cfg config.Config, dataset string, path string, fallback bool, includeBody bool) (Record, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	canonical, summary, err := canonicalSchema(body)
	if err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Record{}, err
	}
	hash := schemaHash(canonical)
	version := 0
	if !fallback {
		version = previousMetadataVersion(cfg, dataset)
		if version == 0 {
			version = 1
		}
	}
	status := "ready"
	if fallback {
		status = "fallback"
	}
	record := Record{
		SchemaVersion: SchemaVersion,
		Dataset:       dataset,
		Status:        status,
		Path:          path,
		Fallback:      fallback,
		Version:       version,
		Hash:          hash,
		UpdatedAt:     info.ModTime().UTC(),
		Summary:       summary,
		Artifact: jobs.Artifact{
			Name:          "schema",
			Role:          "input",
			Kind:          "schema_json",
			SchemaVersion: SchemaVersion,
			Dataset:       dataset,
			Path:          path,
			Status:        status,
			Description:   "Managed dataset schema JSON.",
		},
	}
	if includeBody {
		record.Schema = canonical
	}
	return record, nil
}

func writeSchemaAndMetadata(schemaPath string, body []byte, metaPath string, metadata Metadata) error {
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return err
	}
	schemaTmp := schemaPath + ".tmp"
	metaTmp := metaPath + ".tmp"
	metadataBody, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	metadataBody = append(metadataBody, '\n')
	if err := os.WriteFile(schemaTmp, body, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(metaTmp, metadataBody, 0o600); err != nil {
		_ = os.Remove(schemaTmp)
		return err
	}
	if err := os.Rename(schemaTmp, schemaPath); err != nil {
		_ = os.Remove(schemaTmp)
		_ = os.Remove(metaTmp)
		return err
	}
	if err := os.Rename(metaTmp, metaPath); err != nil {
		_ = os.Remove(metaTmp)
		return err
	}
	return nil
}

func previousMetadataVersion(cfg config.Config, dataset string) int {
	body, err := os.ReadFile(metadataPath(cfg, dataset))
	if err != nil {
		return 0
	}
	var metadata Metadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return 0
	}
	return metadata.Version
}

func schemaHash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func schemaPath(cfg config.Config, dataset string) string {
	return filepath.Join(cfg.SchemaRoot, dataset+".json")
}

func defaultSchemaPath(cfg config.Config) string {
	return filepath.Join(cfg.SchemaRoot, "default.json")
}

func metadataPath(cfg config.Config, dataset string) string {
	root := cfg.DatasetMetaRoot
	if root == "" {
		root = filepath.Join(cfg.ArtifactRoot, "output", "datasets")
	}
	return filepath.Join(root, dataset+".schema.json")
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
