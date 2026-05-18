package benchmark

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
// expected benchmark result artifact.
var ErrMissingOutput = errors.New("benchmark output missing")

// Config describes the Python worker command used by benchmark jobs.
type Config struct {
	PythonBin  string
	ScriptPath string
	WorkingDir string
}

// Result is the compact inline job result for a completed benchmark job.
type Result struct {
	SchemaVersion string  `json:"schema_version"`
	Dataset       string  `json:"dataset"`
	OutputPath    string  `json:"output_path"`
	ProgressPath  string  `json:"progress_path,omitempty"`
	QuestionCount int     `json:"question_count"`
	CorrectCount  int     `json:"correct_count"`
	Accuracy      float64 `json:"accuracy"`
	Stdout        string  `json:"stdout,omitempty"`
	Stderr        string  `json:"stderr,omitempty"`
}

// Run executes the configured Python dataset benchmark worker.
func Run(ctx context.Context, cfg Config, spec jobs.BenchmarkSpec) (*Result, error) {
	if cfg.PythonBin == "" {
		return nil, fmt.Errorf("python binary is required")
	}
	if cfg.ScriptPath == "" {
		return nil, fmt.Errorf("benchmark script path is required")
	}
	if spec.Dataset == "" {
		return nil, fmt.Errorf("dataset is required")
	}
	if spec.QAPath == "" {
		return nil, fmt.Errorf("qa_path is required")
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
		"--qa", spec.QAPath,
		"--output", spec.OutputPath,
	}
	appendInt := func(flag string, value int) {
		if value > 0 {
			args = append(args, flag, strconv.Itoa(value))
		}
	}
	appendString := func(flag string, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, flag, value)
		}
	}
	appendInt("--limit", spec.Limit)
	appendInt("--offset", spec.Offset)
	appendString("--mode", spec.Mode)
	appendInt("--top-k", spec.TopK)
	appendString("--answer-model", spec.AnswerModel)
	appendString("--judge-model", spec.JudgeModel)
	appendString("--llm-base-url", spec.LLMBaseURL)
	appendString("--graph", spec.GraphPath)
	appendString("--chunks", spec.ChunksPath)
	appendString("--corpus", spec.CorpusPath)
	appendString("--progress", spec.ProgressPath)
	appendString("--checkpoint", spec.CheckpointPath)
	appendInt("--concurrency", spec.Concurrency)
	appendInt("--rate-limit-rpm", spec.RateLimitRPM)
	appendInt("--checkpoint-every", spec.CheckpointEvery)
	appendInt("--max-failures", spec.MaxFailures)
	appendInt("--question-timeout", spec.QuestionTimeoutSeconds)
	if spec.Resume {
		args = append(args, "--resume")
	}
	appendString("--cache-dir", spec.CacheDir)
	appendString("--schema", spec.SchemaPath)
	appendString("--config", spec.ConfigPath)

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
		SchemaVersion: "benchmark-job-result/v1",
		Dataset:       spec.Dataset,
		OutputPath:    spec.OutputPath,
		ProgressPath:  spec.ProgressPath,
	}
	err := cmd.Run()
	result.Stdout = strings.TrimSpace(stdout.String())
	result.Stderr = strings.TrimSpace(stderr.String())
	if err != nil {
		if result.Stderr != "" {
			return result, fmt.Errorf("benchmark worker failed: %w: %s", err, result.Stderr)
		}
		return result, fmt.Errorf("benchmark worker failed: %w", err)
	}
	payload, err := validateOutput(spec.OutputPath)
	if err != nil {
		return result, err
	}
	result.QuestionCount = payload.QuestionCount
	result.CorrectCount = payload.CorrectCount
	result.Accuracy = payload.Accuracy
	return result, nil
}

type benchmarkPayload struct {
	SchemaVersion string           `json:"schema_version"`
	Dataset       string           `json:"dataset"`
	QuestionCount int              `json:"question_count"`
	CorrectCount  int              `json:"correct_count"`
	Accuracy      float64          `json:"accuracy"`
	Items         []map[string]any `json:"items"`
}

func validateOutput(path string) (*benchmarkPayload, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrMissingOutput, path)
		}
		return nil, fmt.Errorf("read benchmark output: %w", err)
	}
	var payload benchmarkPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse benchmark output: %w", err)
	}
	if payload.SchemaVersion != "benchmark-result/v1" {
		return nil, fmt.Errorf("unexpected benchmark schema_version %q", payload.SchemaVersion)
	}
	if payload.Dataset == "" {
		return nil, fmt.Errorf("benchmark output missing dataset")
	}
	if payload.QuestionCount < 0 || payload.CorrectCount < 0 {
		return nil, fmt.Errorf("benchmark output has invalid counts")
	}
	if payload.Items == nil {
		return nil, fmt.Errorf("benchmark output missing items")
	}
	return &payload, nil
}
