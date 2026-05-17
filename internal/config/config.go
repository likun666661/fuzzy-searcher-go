package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config contains the long-running service settings. It intentionally stays
// small for the first service milestone; dataset/job persistence can grow from
// this boundary later.
type Config struct {
	AppName          string
	Env              string
	ServerVersion    string
	HTTPAddr         string
	DefaultDataset   string
	DefaultGraph     string
	DefaultChunks    string
	DefaultSidecar   string
	DefaultMode      string
	ArtifactRoot     string
	CorpusRoot       string
	SchemaRoot       string
	GraphRoot        string
	ChunksRoot       string
	CacheRoot        string
	GoldenRoot       string
	TraceRoot        string
	DatasetMetaRoot  string
	DatasetOpsRoot   string
	JobRoot          string
	WorkflowRoot     string
	PythonBin        string
	GoldenScript     string
	ParseDocsScript  string
	BuildGraphScript string
	AnswerScript     string
	WorkerCWD        string
	DatasetNames     []string
	Path2Threshold   float64
	ShutdownGrace    time.Duration
}

// Load reads service configuration from environment variables.
func Load() Config {
	artifactRoot := getenv("YOUTU_RAG_ARTIFACT_ROOT", "../youtu-graphrag")
	defaultDataset := getenv("YOUTU_RAG_DATASET", "demo")
	return Config{
		AppName:          getenv("YOUTU_RAG_APP_NAME", "youtu-rag-service"),
		Env:              getenv("YOUTU_RAG_ENV", "development"),
		ServerVersion:    getenv("YOUTU_RAG_VERSION", "dev"),
		HTTPAddr:         getenv("YOUTU_RAG_HTTP_ADDR", ":8080"),
		DefaultDataset:   defaultDataset,
		DefaultGraph:     getenv("YOUTU_RAG_GRAPH", ""),
		DefaultChunks:    getenv("YOUTU_RAG_CHUNKS", ""),
		DefaultSidecar:   getenv("YOUTU_RAG_SIDECAR_URL", ""),
		DefaultMode:      getenv("YOUTU_RAG_MODE", "native"),
		ArtifactRoot:     artifactRoot,
		CorpusRoot:       getenv("YOUTU_RAG_CORPUS_ROOT", filepath.Join(artifactRoot, "data")),
		SchemaRoot:       getenv("YOUTU_RAG_SCHEMA_ROOT", filepath.Join(artifactRoot, "schemas")),
		GraphRoot:        getenv("YOUTU_RAG_GRAPH_ROOT", filepath.Join(artifactRoot, "output", "graphs")),
		ChunksRoot:       getenv("YOUTU_RAG_CHUNKS_ROOT", filepath.Join(artifactRoot, "output", "chunks")),
		CacheRoot:        getenv("YOUTU_RAG_CACHE_ROOT", filepath.Join(artifactRoot, "retriever", "faiss_cache_new")),
		GoldenRoot:       getenv("YOUTU_RAG_GOLDEN_ROOT", filepath.Join(artifactRoot, "output", "retrieval_golden")),
		TraceRoot:        getenv("YOUTU_RAG_TRACE_ROOT", filepath.Join(artifactRoot, "output", "retrieval_traces")),
		DatasetMetaRoot:  getenv("YOUTU_RAG_DATASET_META_ROOT", filepath.Join(artifactRoot, "output", "datasets")),
		DatasetOpsRoot:   getenv("YOUTU_RAG_DATASET_OPS_ROOT", filepath.Join(artifactRoot, "output", "dataset_operations")),
		JobRoot:          getenv("YOUTU_RAG_JOB_ROOT", filepath.Join(artifactRoot, "output", "jobs")),
		WorkflowRoot:     getenv("YOUTU_RAG_WORKFLOW_ROOT", filepath.Join(artifactRoot, "output", "workflows")),
		PythonBin:        getenv("YOUTU_RAG_PYTHON", filepath.Join(artifactRoot, ".venv", "bin", "python")),
		GoldenScript:     getenv("YOUTU_RAG_GOLDEN_SCRIPT", filepath.Join(artifactRoot, "scripts", "generate_retriever_golden.py")),
		ParseDocsScript:  getenv("YOUTU_RAG_PARSE_DOCUMENTS_SCRIPT", filepath.Join(artifactRoot, "scripts", "parse_documents_worker.py")),
		BuildGraphScript: getenv("YOUTU_RAG_BUILD_GRAPH_SCRIPT", filepath.Join(artifactRoot, "scripts", "build_graph_worker.py")),
		AnswerScript:     getenv("YOUTU_RAG_ANSWER_SCRIPT", filepath.Join(artifactRoot, "scripts", "answer_worker.py")),
		WorkerCWD:        getenv("YOUTU_RAG_WORKER_CWD", artifactRoot),
		DatasetNames:     getenvList("YOUTU_RAG_DATASETS", []string{defaultDataset}),
		Path2Threshold:   getenvFloat("YOUTU_RAG_PATH2_THRESHOLD", 0.1),
		ShutdownGrace:    time.Duration(getenvInt("YOUTU_RAG_SHUTDOWN_SECONDS", 10)) * time.Second,
	}
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvFloat(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvList(key string, fallback []string) []string {
	value := os.Getenv(key)
	if value == "" {
		return append([]string(nil), fallback...)
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), fallback...)
	}
	return out
}
