package config

import (
	"os"
	"strconv"
	"time"
)

// Config contains the long-running service settings. It intentionally stays
// small for the first service milestone; dataset/job persistence can grow from
// this boundary later.
type Config struct {
	AppName        string
	Env            string
	ServerVersion  string
	HTTPAddr       string
	DefaultDataset string
	DefaultGraph   string
	DefaultChunks  string
	DefaultSidecar string
	DefaultMode    string
	Path2Threshold float64
	ShutdownGrace  time.Duration
}

// Load reads service configuration from environment variables.
func Load() Config {
	return Config{
		AppName:        getenv("YOUTU_RAG_APP_NAME", "youtu-rag-service"),
		Env:            getenv("YOUTU_RAG_ENV", "development"),
		ServerVersion:  getenv("YOUTU_RAG_VERSION", "dev"),
		HTTPAddr:       getenv("YOUTU_RAG_HTTP_ADDR", ":8080"),
		DefaultDataset: getenv("YOUTU_RAG_DATASET", "demo"),
		DefaultGraph:   getenv("YOUTU_RAG_GRAPH", ""),
		DefaultChunks:  getenv("YOUTU_RAG_CHUNKS", ""),
		DefaultSidecar: getenv("YOUTU_RAG_SIDECAR_URL", ""),
		DefaultMode:    getenv("YOUTU_RAG_MODE", "native"),
		Path2Threshold: getenvFloat("YOUTU_RAG_PATH2_THRESHOLD", 0.1),
		ShutdownGrace:  time.Duration(getenvInt("YOUTU_RAG_SHUTDOWN_SECONDS", 10)) * time.Second,
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
