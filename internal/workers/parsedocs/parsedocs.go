package parsedocs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/likun666661/youtu-rag-service/internal/jobs"
)

// ErrMissingOutput means the worker exited successfully but did not write the
// expected corpus artifact.
var ErrMissingOutput = errors.New("parse documents output missing")

// Config describes the Python worker command used by parse_documents jobs.
type Config struct {
	PythonBin  string
	ScriptPath string
	WorkingDir string
}

// Result is the inline job result for a completed parse_documents job.
type Result struct {
	SchemaVersion string   `json:"schema_version"`
	Dataset       string   `json:"dataset"`
	OutputPath    string   `json:"output_path"`
	DocumentPaths []string `json:"document_paths,omitempty"`
	Stdout        string   `json:"stdout,omitempty"`
	Stderr        string   `json:"stderr,omitempty"`
}

// Run executes the configured Python document parsing worker.
func Run(ctx context.Context, cfg Config, spec jobs.ParseDocumentsSpec) (*Result, error) {
	if cfg.PythonBin == "" {
		return nil, fmt.Errorf("python binary is required")
	}
	if cfg.ScriptPath == "" {
		return nil, fmt.Errorf("parse documents script path is required")
	}
	if spec.Dataset == "" {
		return nil, fmt.Errorf("dataset is required")
	}
	if len(spec.DocumentPaths) == 0 {
		return nil, fmt.Errorf("document_paths is required")
	}
	if spec.OutputPath == "" {
		return nil, fmt.Errorf("output_path is required")
	}
	if err := os.MkdirAll(filepath.Dir(spec.OutputPath), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	args := []string{
		cfg.ScriptPath,
		"--dataset", spec.Dataset,
		"--output", spec.OutputPath,
	}
	for _, path := range spec.DocumentPaths {
		if strings.TrimSpace(path) != "" {
			args = append(args, "--document", path)
		}
	}
	if strings.TrimSpace(spec.ConfigPath) != "" {
		args = append(args, "--config", spec.ConfigPath)
	}
	if strings.TrimSpace(spec.Mode) != "" {
		args = append(args, "--mode", spec.Mode)
	}

	cmd := exec.CommandContext(ctx, cfg.PythonBin, args...)
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}
	cmd.Env = append(os.Environ(),
		"TOKENIZERS_PARALLELISM=false",
		"OMP_NUM_THREADS=1",
		"MKL_NUM_THREADS=1",
		"VECLIB_MAXIMUM_THREADS=1",
		"NUMEXPR_NUM_THREADS=1",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := &Result{
		SchemaVersion: "parse-documents-result/v1",
		Dataset:       spec.Dataset,
		OutputPath:    spec.OutputPath,
		DocumentPaths: append([]string(nil), spec.DocumentPaths...),
	}
	err := cmd.Run()
	result.Stdout = strings.TrimSpace(stdout.String())
	result.Stderr = strings.TrimSpace(stderr.String())
	if err != nil {
		if result.Stderr != "" {
			return result, fmt.Errorf("parse documents worker failed: %w: %s", err, result.Stderr)
		}
		return result, fmt.Errorf("parse documents worker failed: %w", err)
	}
	if err := validateOutput(spec.OutputPath); err != nil {
		return result, err
	}
	return result, nil
}

func validateOutput(path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrMissingOutput, path)
		}
		return fmt.Errorf("read corpus output: %w", err)
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parse corpus output: %w", err)
	}
	return nil
}
