package golden

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/likun666661/youtu-rag-service/internal/jobs"
)

// ErrMissingOutput means the worker exited successfully but did not write the
// expected artifact.
var ErrMissingOutput = errors.New("golden output missing")

// Config describes the Python worker command used by generate_golden jobs.
type Config struct {
	PythonBin  string
	ScriptPath string
	WorkingDir string
}

// Result is the inline job result for a completed generate_golden job.
type Result struct {
	SchemaVersion string `json:"schema_version"`
	Dataset       string `json:"dataset"`
	OutputPath    string `json:"output_path"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
}

// Run executes the configured Python golden-generation script.
func Run(ctx context.Context, cfg Config, spec jobs.GenerateGoldenSpec) (*Result, error) {
	if cfg.PythonBin == "" {
		return nil, fmt.Errorf("python binary is required")
	}
	if cfg.ScriptPath == "" {
		return nil, fmt.Errorf("golden script path is required")
	}
	if spec.Dataset == "" {
		return nil, fmt.Errorf("dataset is required")
	}
	if spec.OutputPath == "" {
		return nil, fmt.Errorf("output_path is required")
	}
	if err := os.MkdirAll(filepath.Dir(spec.OutputPath), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	args := []string{cfg.ScriptPath, "--dataset", spec.Dataset, "--output", spec.OutputPath}
	if spec.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(spec.Limit))
	}
	for _, question := range spec.Questions {
		if strings.TrimSpace(question) != "" {
			args = append(args, "--question", question)
		}
	}
	if spec.TopK > 0 {
		args = append(args, "--top-k", strconv.Itoa(spec.TopK))
	}
	if strings.TrimSpace(spec.InvolvedTypes) != "" {
		args = append(args, "--involved-types", spec.InvolvedTypes)
	}
	if strings.TrimSpace(spec.ConfigPath) != "" {
		args = append(args, "--config", spec.ConfigPath)
	}
	if spec.SkipBuildIndices {
		args = append(args, "--skip-build-indices")
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
		SchemaVersion: "generate-golden-result/v1",
		Dataset:       spec.Dataset,
		OutputPath:    spec.OutputPath,
	}
	err := cmd.Run()
	result.Stdout = strings.TrimSpace(stdout.String())
	result.Stderr = strings.TrimSpace(stderr.String())
	if err != nil {
		if result.Stderr != "" {
			return result, fmt.Errorf("generate golden worker failed: %w: %s", err, result.Stderr)
		}
		return result, fmt.Errorf("generate golden worker failed: %w", err)
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
		return fmt.Errorf("read golden output: %w", err)
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parse golden output: %w", err)
	}
	if payload.SchemaVersion != "retriever-golden/v1" {
		return fmt.Errorf("unexpected golden schema_version %q", payload.SchemaVersion)
	}
	return nil
}
