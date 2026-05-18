package buildgraph

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
// expected graph/chunks artifacts.
var ErrMissingOutput = errors.New("build graph output missing")

// Config describes the Python worker command used by build_graph jobs.
type Config struct {
	PythonBin  string
	ScriptPath string
	WorkingDir string
}

// Result is the inline job result for a completed build_graph job.
type Result struct {
	SchemaVersion    string `json:"schema_version"`
	Dataset          string `json:"dataset"`
	GraphOutputPath  string `json:"graph_output_path"`
	ChunksOutputPath string `json:"chunks_output_path"`
	CacheDir         string `json:"cache_dir,omitempty"`
	Stdout           string `json:"stdout,omitempty"`
	Stderr           string `json:"stderr,omitempty"`
}

// Run executes the configured Python graph-construction worker.
func Run(ctx context.Context, cfg Config, spec jobs.BuildGraphSpec) (*Result, error) {
	if cfg.PythonBin == "" {
		return nil, fmt.Errorf("python binary is required")
	}
	if cfg.ScriptPath == "" {
		return nil, fmt.Errorf("build graph script path is required")
	}
	if spec.Dataset == "" {
		return nil, fmt.Errorf("dataset is required")
	}
	if spec.CorpusPath == "" {
		return nil, fmt.Errorf("corpus_path is required")
	}
	if spec.GraphOutputPath == "" {
		return nil, fmt.Errorf("graph_output_path is required")
	}
	if spec.ChunksOutputPath == "" {
		return nil, fmt.Errorf("chunks_output_path is required")
	}
	for _, path := range []string{spec.GraphOutputPath, spec.ChunksOutputPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create output directory: %w", err)
		}
	}
	if spec.CacheDir != "" {
		if err := os.MkdirAll(spec.CacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("create cache directory: %w", err)
		}
	}

	args := []string{
		cfg.ScriptPath,
		"--dataset", spec.Dataset,
		"--corpus", spec.CorpusPath,
		"--graph-output", spec.GraphOutputPath,
		"--chunks-output", spec.ChunksOutputPath,
	}
	if strings.TrimSpace(spec.SchemaPath) != "" {
		args = append(args, "--schema", spec.SchemaPath)
	}
	if strings.TrimSpace(spec.CacheDir) != "" {
		args = append(args, "--cache-dir", spec.CacheDir)
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
		SchemaVersion:    "build-graph-result/v1",
		Dataset:          spec.Dataset,
		GraphOutputPath:  spec.GraphOutputPath,
		ChunksOutputPath: spec.ChunksOutputPath,
		CacheDir:         spec.CacheDir,
	}
	err := cmd.Run()
	result.Stdout = strings.TrimSpace(stdout.String())
	result.Stderr = strings.TrimSpace(stderr.String())
	if err != nil {
		if result.Stderr != "" {
			return result, fmt.Errorf("build graph worker failed: %w: %s", err, result.Stderr)
		}
		return result, fmt.Errorf("build graph worker failed: %w", err)
	}
	if err := validateGraphOutput(spec.GraphOutputPath); err != nil {
		return result, err
	}
	if err := validateChunksOutput(spec.ChunksOutputPath); err != nil {
		return result, err
	}
	return result, nil
}

func validateGraphOutput(path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: graph %s", ErrMissingOutput, path)
		}
		return fmt.Errorf("read graph output: %w", err)
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parse graph output: %w", err)
	}
	return nil
}

func validateChunksOutput(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: chunks %s", ErrMissingOutput, path)
		}
		return fmt.Errorf("stat chunks output: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("chunks output is a directory: %s", path)
	}
	return nil
}
