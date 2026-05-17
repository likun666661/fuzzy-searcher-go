package datasetops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fuzzy-searcher-go/internal/jobs"
)

// ErrNotFound means an operation id is unknown.
var ErrNotFound = errors.New("dataset operation not found")

const (
	TypeImport        = "import"
	TypeDelete        = "delete"
	TypeRebuild       = "rebuild"
	TypeCreateDataset = "create_dataset"
)

// Operation is the stable, user-visible record for one dataset lifecycle
// operation.
type Operation struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Dataset       string          `json:"dataset"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	Request       any             `json:"request,omitempty"`
	JobRefs       []JobRef        `json:"job_refs,omitempty"`
	WorkflowRefs  []WorkflowRef   `json:"workflow_refs,omitempty"`
	Artifacts     []jobs.Artifact `json:"artifacts,omitempty"`
	Result        any             `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
}

// JobRef points to a child job associated with an operation.
type JobRef struct {
	Name  string `json:"name"`
	JobID string `json:"job_id"`
	Type  string `json:"type"`
}

// WorkflowRef points to a child workflow associated with an operation.
type WorkflowRef struct {
	Name       string `json:"name"`
	WorkflowID string `json:"workflow_id"`
	Type       string `json:"type"`
}

// Store persists dataset operations as JSON records.
type Store struct {
	mu      sync.Mutex
	seq     atomic.Uint64
	dir     string
	records map[string]Operation
}

// NewStore creates a file-backed dataset operation store.
func NewStore(dir string) *Store {
	store := &Store{
		dir:     dir,
		records: map[string]Operation{},
	}
	store.load()
	return store
}

// Append stores a new operation record.
func (s *Store) Append(op Operation) Operation {
	now := time.Now().UTC()
	if op.ID == "" {
		op.ID = s.nextID()
	}
	if op.SchemaVersion == "" {
		op.SchemaVersion = "dataset-operation/v1"
	}
	if op.Status == "" {
		op.Status = "succeeded"
	}
	if op.CreatedAt.IsZero() {
		op.CreatedAt = now
	}
	if isTerminal(op.Status) && op.FinishedAt == nil {
		finished := now
		op.FinishedAt = &finished
	}
	op.Artifacts = append([]jobs.Artifact(nil), op.Artifacts...)
	op.JobRefs = append([]JobRef(nil), op.JobRefs...)
	op.WorkflowRefs = append([]WorkflowRef(nil), op.WorkflowRefs...)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[op.ID] = op
	s.persistLocked(op)
	return op
}

// Get returns one operation by id.
func (s *Store) Get(id string) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.records[id]
	if !ok {
		return Operation{}, ErrNotFound
	}
	return clone(op), nil
}

// List returns operations sorted newest first. Empty dataset means all datasets.
func (s *Store) List(dataset string) []Operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Operation, 0, len(s.records))
	for _, op := range s.records {
		if dataset == "" || op.Dataset == dataset {
			out = append(out, clone(op))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *Store) load() {
	if s.dir == "" {
		return
	}
	matches, err := filepath.Glob(filepath.Join(s.dir, "*.json"))
	if err != nil {
		return
	}
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var op Operation
		var rec record
		if err := json.Unmarshal(body, &rec); err == nil && rec.Operation.ID != "" {
			op = rec.Operation
		} else if err := json.Unmarshal(body, &op); err != nil {
			continue
		}
		if op.ID == "" {
			continue
		}
		if op.SchemaVersion == "" {
			op.SchemaVersion = "dataset-operation/v1"
		}
		if !isTerminal(op.Status) {
			now := time.Now().UTC()
			op.Status = "failed"
			op.Error = "operation interrupted by service restart"
			op.FinishedAt = &now
		}
		s.records[op.ID] = op
	}
}

func (s *Store) persistLocked(op Operation) {
	if s.dir == "" || op.ID == "" {
		return
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(s.dir, op.ID+".json")
	tmp := path + ".tmp"
	body, err := json.MarshalIndent(record{
		SchemaVersion: "dataset-operation-record/v1",
		Operation:     op,
	}, "", "  ")
	if err != nil {
		return
	}
	body = append(body, '\n')
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func (s *Store) nextID() string {
	seq := s.seq.Add(1)
	return fmt.Sprintf("op_%d_%06d", time.Now().UTC().UnixNano(), seq)
}

func clone(op Operation) Operation {
	op.Artifacts = append([]jobs.Artifact(nil), op.Artifacts...)
	op.JobRefs = append([]JobRef(nil), op.JobRefs...)
	op.WorkflowRefs = append([]WorkflowRef(nil), op.WorkflowRefs...)
	op.FinishedAt = cloneTime(op.FinishedAt)
	return op
}

func isTerminal(status string) bool {
	return status == "planned" || status == "succeeded" || status == "failed" || status == "canceled"
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

type record struct {
	SchemaVersion string    `json:"schema_version"`
	Operation     Operation `json:"operation"`
}
