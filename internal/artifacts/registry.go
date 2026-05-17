package artifacts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fuzzy-searcher-go/internal/config"
)

// Registry maps Youtu-RAG datasets to their filesystem artifacts.
type Registry struct {
	config config.Config
}

// NewRegistry constructs a dataset/artifact registry from service config.
func NewRegistry(config config.Config) *Registry {
	return &Registry{config: config}
}

// Dataset describes one dataset and its coarse readiness status.
type Dataset struct {
	Name                      string     `json:"name"`
	Status                    string     `json:"status"`
	RetrievalReady            bool       `json:"retrieval_ready"`
	MissingRetrievalArtifacts []string   `json:"missing_retrieval_artifacts,omitempty"`
	Artifacts                 []Artifact `json:"artifacts"`
}

// Artifact describes one expected dataset artifact.
type Artifact struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
}

// List returns discovered datasets.
func (r *Registry) List() []Dataset {
	names := r.discoverDatasetNames()
	out := make([]Dataset, 0, len(names))
	for _, name := range names {
		out = append(out, r.Get(name))
	}
	return out
}

// Get returns one dataset status. The dataset can be absent; in that case the
// artifact paths still explain what the service expects to find.
func (r *Registry) Get(name string) Dataset {
	artifacts := r.Artifacts(name)
	missing := missingRetrievalArtifacts(artifacts)
	return Dataset{
		Name:                      name,
		Status:                    statusFor(artifacts),
		RetrievalReady:            len(missing) == 0,
		MissingRetrievalArtifacts: missing,
		Artifacts:                 artifacts,
	}
}

// Artifacts returns the expected artifacts for one dataset.
func (r *Registry) Artifacts(name string) []Artifact {
	return []Artifact{
		r.artifact("corpus", r.corpusPath(name), "file", false),
		r.artifact("schema", filepath.Join(r.config.SchemaRoot, name+".json"), "file", false),
		r.artifact("graph", r.graphPath(name), "file", true),
		r.artifact("chunks", r.chunksPath(name), "file", true),
		r.artifact("cache", filepath.Join(r.config.CacheRoot, name), "dir", true),
		r.artifact("golden", filepath.Join(r.config.GoldenRoot, name+".json"), "file", false),
		r.artifact("triple_trace", filepath.Join(r.config.TraceRoot, name+"_triple_trace.json"), "file", false),
	}
}

func (r *Registry) artifact(name string, path string, kind string, required bool) Artifact {
	return Artifact{
		Name:     name,
		Path:     path,
		Kind:     kind,
		Required: required,
		Exists:   exists(path, kind),
	}
}

func (r *Registry) corpusPath(name string) string {
	demoPath := filepath.Join(r.config.CorpusRoot, name, name+"_corpus.json")
	if exists(demoPath, "file") {
		return demoPath
	}
	uploadedPath := filepath.Join(r.config.CorpusRoot, "uploaded", name, "corpus.json")
	if exists(uploadedPath, "file") {
		return uploadedPath
	}
	return demoPath
}

func (r *Registry) graphPath(name string) string {
	if name == r.config.DefaultDataset && r.config.DefaultGraph != "" {
		return r.config.DefaultGraph
	}
	return filepath.Join(r.config.GraphRoot, name+"_new.json")
}

func (r *Registry) chunksPath(name string) string {
	if name == r.config.DefaultDataset && r.config.DefaultChunks != "" {
		return r.config.DefaultChunks
	}
	return filepath.Join(r.config.ChunksRoot, name+".txt")
}

func (r *Registry) discoverDatasetNames() []string {
	names := map[string]struct{}{}
	for _, name := range r.config.DatasetNames {
		if name != "" {
			names[name] = struct{}{}
		}
	}
	addBaseNames(names, r.config.SchemaRoot, ".json", "")
	addBaseNames(names, r.config.GraphRoot, "_new.json", "")
	addBaseNames(names, r.config.ChunksRoot, ".txt", "")
	addCorpusNames(names, r.config.CorpusRoot)

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func addBaseNames(names map[string]struct{}, root string, suffix string, prefix string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		name = strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		if name != "" {
			names[name] = struct{}{}
		}
	}
}

func addCorpusNames(names map[string]struct{}, root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "uploaded" {
			names[entry.Name()] = struct{}{}
		}
	}
	uploadedEntries, err := os.ReadDir(filepath.Join(root, "uploaded"))
	if err != nil {
		return
	}
	for _, entry := range uploadedEntries {
		if entry.IsDir() {
			names[entry.Name()] = struct{}{}
		}
	}
}

func missingRetrievalArtifacts(artifacts []Artifact) []string {
	missing := []string{}
	for _, artifact := range artifacts {
		if artifact.Required && !artifact.Exists {
			missing = append(missing, artifact.Name)
		}
	}
	return missing
}

func statusFor(artifacts []Artifact) string {
	hasCorpus := hasArtifact(artifacts, "corpus")
	hasSchema := hasArtifact(artifacts, "schema")
	hasGraph := hasArtifact(artifacts, "graph")
	hasChunks := hasArtifact(artifacts, "chunks")
	hasCache := hasArtifact(artifacts, "cache")
	switch {
	case hasGraph && hasChunks && hasCache:
		return "retrieval_ready"
	case hasGraph && hasChunks:
		return "graph_ready"
	case hasCorpus && hasSchema:
		return "schema_ready"
	case hasCorpus:
		return "corpus_ready"
	case hasSchema:
		return "schema_ready"
	default:
		return "empty"
	}
}

func hasArtifact(artifacts []Artifact, name string) bool {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return artifact.Exists
		}
	}
	return false
}

func exists(path string, kind string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	switch kind {
	case "dir":
		return info.IsDir()
	default:
		return !info.IsDir()
	}
}
