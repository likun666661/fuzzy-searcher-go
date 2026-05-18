package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound means a job id is unknown to the in-memory manager.
var ErrNotFound = errors.New("job not found")

// Status is the lifecycle state for one async job.
type Status string

const (
	TypeRetrieve       = "retrieve"
	TypeParseDocuments = "parse_documents"
	TypeBuildGraph     = "build_graph"
	TypeGenerateGolden = "generate_golden"
	TypeAnswer         = "answer"
	TypeBenchmark      = "benchmark"
)

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// Event records a user-visible lifecycle event for a job.
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message,omitempty"`
	Status  Status    `json:"status,omitempty"`
}

// Job is a snapshot of async job state.
type Job struct {
	SchemaVersion string     `json:"schema_version"`
	ID            string     `json:"id"`
	Type          string     `json:"type"`
	Status        Status     `json:"status"`
	Spec          any        `json:"spec,omitempty"`
	Artifacts     []Artifact `json:"artifacts,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Error         string     `json:"error,omitempty"`
	Result        any        `json:"result,omitempty"`
}

// Artifact describes an input or output file/object associated with a job.
type Artifact struct {
	Name          string `json:"name"`
	Role          string `json:"role"`
	Kind          string `json:"kind"`
	SchemaVersion string `json:"schema_version,omitempty"`
	Dataset       string `json:"dataset,omitempty"`
	Path          string `json:"path,omitempty"`
	Status        string `json:"status,omitempty"`
	Description   string `json:"description,omitempty"`
}

// RetrieveSpec is the typed job spec for the current retrieve workflow.
type RetrieveSpec struct {
	Dataset        string  `json:"dataset,omitempty"`
	Question       string  `json:"question"`
	TopK           int     `json:"top_k,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	GraphPath      string  `json:"graph_path,omitempty"`
	ChunksPath     string  `json:"chunks_path,omitempty"`
	SidecarURL     string  `json:"sidecar_url,omitempty"`
	Path2Threshold float64 `json:"path2_threshold,omitempty"`
}

// GenerateGoldenSpec is the typed job spec for Python retriever golden
// generation. Go owns orchestration and artifact tracking; Python owns the
// retriever runtime.
type GenerateGoldenSpec struct {
	Dataset          string   `json:"dataset"`
	OutputPath       string   `json:"output_path"`
	Limit            int      `json:"limit,omitempty"`
	Questions        []string `json:"questions,omitempty"`
	TopK             int      `json:"top_k,omitempty"`
	InvolvedTypes    string   `json:"involved_types,omitempty"`
	ConfigPath       string   `json:"config_path,omitempty"`
	SkipBuildIndices bool     `json:"skip_build_indices,omitempty"`
	PythonBin        string   `json:"python_bin,omitempty"`
	ScriptPath       string   `json:"script_path,omitempty"`
	WorkingDir       string   `json:"working_dir,omitempty"`
}

// ParseDocumentsSpec is the typed job spec for Python document parsing workers.
type ParseDocumentsSpec struct {
	Dataset       string   `json:"dataset"`
	DocumentPaths []string `json:"document_paths"`
	OutputPath    string   `json:"output_path"`
	ConfigPath    string   `json:"config_path,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	PythonBin     string   `json:"python_bin,omitempty"`
	ScriptPath    string   `json:"script_path,omitempty"`
	WorkingDir    string   `json:"working_dir,omitempty"`
}

// BuildGraphSpec is the typed job spec for Python graph construction workers.
type BuildGraphSpec struct {
	Dataset          string `json:"dataset"`
	CorpusPath       string `json:"corpus_path"`
	SchemaPath       string `json:"schema_path,omitempty"`
	GraphOutputPath  string `json:"graph_output_path"`
	ChunksOutputPath string `json:"chunks_output_path"`
	CacheDir         string `json:"cache_dir,omitempty"`
	ConfigPath       string `json:"config_path,omitempty"`
	Mode             string `json:"mode,omitempty"`
	PythonBin        string `json:"python_bin,omitempty"`
	ScriptPath       string `json:"script_path,omitempty"`
	WorkingDir       string `json:"working_dir,omitempty"`
}

// AnswerSpec is the typed job spec for Python answer-generation workers.
type AnswerSpec struct {
	Dataset       string `json:"dataset"`
	Question      string `json:"question"`
	OutputPath    string `json:"output_path"`
	Mode          string `json:"mode,omitempty"`
	TopK          int    `json:"top_k,omitempty"`
	GraphPath     string `json:"graph_path,omitempty"`
	ChunksPath    string `json:"chunks_path,omitempty"`
	ConfigPath    string `json:"config_path,omitempty"`
	InvolvedTypes string `json:"involved_types,omitempty"`
	PythonBin     string `json:"python_bin,omitempty"`
	ScriptPath    string `json:"script_path,omitempty"`
	WorkingDir    string `json:"working_dir,omitempty"`
}

// BenchmarkSpec is the typed job spec for Python dataset benchmark workers.
type BenchmarkSpec struct {
	Dataset                string `json:"dataset"`
	QAPath                 string `json:"qa_path"`
	OutputPath             string `json:"output_path"`
	ProgressPath           string `json:"progress_path,omitempty"`
	CheckpointPath         string `json:"checkpoint_path,omitempty"`
	Limit                  int    `json:"limit,omitempty"`
	Offset                 int    `json:"offset,omitempty"`
	Concurrency            int    `json:"concurrency,omitempty"`
	RateLimitRPM           int    `json:"rate_limit_rpm,omitempty"`
	CheckpointEvery        int    `json:"checkpoint_every,omitempty"`
	MaxFailures            int    `json:"max_failures,omitempty"`
	QuestionTimeoutSeconds int    `json:"question_timeout_seconds,omitempty"`
	Resume                 bool   `json:"resume,omitempty"`
	Mode                   string `json:"mode,omitempty"`
	TopK                   int    `json:"top_k,omitempty"`
	AnswerModel            string `json:"answer_model,omitempty"`
	JudgeModel             string `json:"judge_model,omitempty"`
	LLMBaseURL             string `json:"llm_base_url,omitempty"`
	GraphPath              string `json:"graph_path,omitempty"`
	ChunksPath             string `json:"chunks_path,omitempty"`
	CacheDir               string `json:"cache_dir,omitempty"`
	SchemaPath             string `json:"schema_path,omitempty"`
	ConfigPath             string `json:"config_path,omitempty"`
	PythonBin              string `json:"python_bin,omitempty"`
	ScriptPath             string `json:"script_path,omitempty"`
	WorkingDir             string `json:"working_dir,omitempty"`
	BuildFirst             bool   `json:"build_first,omitempty"`
	BuildMode              string `json:"build_mode,omitempty"`
	CorpusPath             string `json:"corpus_path,omitempty"`
	BuildGraphID           string `json:"build_graph_job_id,omitempty"`
}

// Runner is the unit of work executed by the manager.
type Runner func(context.Context, *Recorder) (any, error)

// Manager keeps async jobs in memory. It is intentionally process-local; a
// durable queue can replace it later without changing the HTTP contract.
type Manager struct {
	mu      sync.Mutex
	seq     atomic.Uint64
	entries map[string]*entry
	store   store
}

type entry struct {
	job    Job
	events []Event
	cancel context.CancelFunc
}

// Recorder lets a running job append events without exposing manager internals.
type Recorder struct {
	manager *Manager
	jobID   string
}

// Option customizes a Manager.
type Option func(*Manager)

// NewManager constructs an empty in-memory manager.
func NewManager(opts ...Option) *Manager {
	manager := &Manager{entries: map[string]*entry{}}
	for _, opt := range opts {
		opt(manager)
	}
	return manager
}

// WithFileStore persists job snapshots as JSON files under dir.
func WithFileStore(dir string) Option {
	return func(manager *Manager) {
		if dir == "" {
			return
		}
		manager.store = fileStore{dir: dir}
		manager.loadStoredJobs()
	}
}

// Submit registers and starts one job. Jobs use a background context so they
// outlive the HTTP request that created them; callers can cancel via Cancel.
func (m *Manager) Submit(jobType string, runner Runner) Job {
	return m.SubmitSpec(jobType, nil, nil, runner)
}

// SubmitSpec registers and starts one typed job with stable spec/artifact
// metadata.
func (m *Manager) SubmitSpec(jobType string, spec any, artifacts []Artifact, runner Runner) Job {
	id := m.nextID()
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	ent := &entry{
		job: Job{
			SchemaVersion: "service-job/v1",
			ID:            id,
			Type:          jobType,
			Status:        StatusQueued,
			Spec:          spec,
			Artifacts:     append([]Artifact(nil), artifacts...),
			CreatedAt:     now,
		},
		events: []Event{{
			Time:    now,
			Type:    "queued",
			Message: "job accepted",
			Status:  StatusQueued,
		}},
		cancel: cancel,
	}

	m.mu.Lock()
	m.entries[id] = ent
	m.persistLocked(ent)
	m.mu.Unlock()

	go m.run(ctx, id, runner)
	job, _ := m.Get(id)
	return job
}

// Get returns a stable snapshot for one job.
func (m *Manager) Get(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, ok := m.entries[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return cloneJob(ent.job), nil
}

// Events returns a copy of the event stream for one job.
func (m *Manager) Events(id string) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, ok := m.entries[id]
	if !ok {
		return nil, ErrNotFound
	}
	events := make([]Event, len(ent.events))
	copy(events, ent.events)
	return events, nil
}

// Cancel requests cancellation for a queued or running job.
func (m *Manager) Cancel(id string) (Job, bool, error) {
	m.mu.Lock()
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return Job{}, false, ErrNotFound
	}
	if isTerminal(ent.job.Status) {
		job := cloneJob(ent.job)
		m.mu.Unlock()
		return job, false, nil
	}
	cancel := ent.cancel
	now := time.Now().UTC()
	ent.events = append(ent.events, Event{
		Time:    now,
		Type:    "cancel_requested",
		Message: "job cancellation requested",
		Status:  ent.job.Status,
	})
	job := cloneJob(ent.job)
	m.persistLocked(ent)
	m.mu.Unlock()

	cancel()
	return job, true, nil
}

// Event appends a custom lifecycle event.
func (r *Recorder) Event(eventType, message string) {
	r.manager.addEvent(r.jobID, eventType, message, "")
}

// Artifact updates the status/path for one named job artifact.
func (r *Recorder) Artifact(name, status, path string) {
	r.manager.updateArtifact(r.jobID, name, status, path)
}

// Progress records a benchmark_progress event and updates the checkpoint
// artifact. The progress payload is kept in the message as compact JSON so the
// existing event contract remains backwards-compatible.
func (r *Recorder) Progress(message string) {
	r.manager.addEvent(r.jobID, "benchmark_progress", message, "")
}

func (m *Manager) run(ctx context.Context, id string, runner Runner) {
	started := time.Now().UTC()
	m.mu.Lock()
	ent := m.entries[id]
	ent.job.Status = StatusRunning
	ent.job.StartedAt = timePtr(started)
	ent.events = append(ent.events, Event{
		Time:    started,
		Type:    "running",
		Message: "job started",
		Status:  StatusRunning,
	})
	m.persistLocked(ent)
	m.mu.Unlock()

	result, err := runner(ctx, &Recorder{manager: m, jobID: id})

	finished := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	ent = m.entries[id]
	ent.job.FinishedAt = timePtr(finished)
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		ent.job.Status = StatusCanceled
		ent.events = append(ent.events, Event{
			Time:    finished,
			Type:    "canceled",
			Message: "job canceled",
			Status:  StatusCanceled,
		})
	case err != nil:
		ent.job.Status = StatusFailed
		ent.job.Error = err.Error()
		ent.events = append(ent.events, Event{
			Time:    finished,
			Type:    "failed",
			Message: err.Error(),
			Status:  StatusFailed,
		})
	default:
		ent.job.Status = StatusSucceeded
		ent.job.Result = result
		ent.events = append(ent.events, Event{
			Time:    finished,
			Type:    "succeeded",
			Message: "job succeeded",
			Status:  StatusSucceeded,
		})
	}
	m.persistLocked(ent)
}

func (m *Manager) addEvent(id string, eventType, message string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, ok := m.entries[id]
	if !ok {
		return
	}
	if status == "" {
		status = ent.job.Status
	}
	ent.events = append(ent.events, Event{
		Time:    time.Now().UTC(),
		Type:    eventType,
		Message: message,
		Status:  status,
	})
	m.persistLocked(ent)
}

func (m *Manager) updateArtifact(id string, name string, status string, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, ok := m.entries[id]
	if !ok {
		return
	}
	for idx := range ent.job.Artifacts {
		if ent.job.Artifacts[idx].Name == name {
			if status != "" {
				ent.job.Artifacts[idx].Status = status
			}
			if path != "" {
				ent.job.Artifacts[idx].Path = path
			}
			m.persistLocked(ent)
			return
		}
	}
}

func (m *Manager) loadStoredJobs() {
	if m.store == nil {
		return
	}
	records, err := m.store.Load()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, record := range records {
		if record.Job.ID == "" {
			continue
		}
		job := record.Job
		if job.SchemaVersion == "" {
			job.SchemaVersion = "service-job/v1"
		}
		events := append([]Event(nil), record.Events...)
		if !isTerminal(job.Status) {
			job.Status = StatusFailed
			job.Error = "job interrupted by service restart"
			job.FinishedAt = timePtr(now)
			events = append(events, Event{
				Time:    now,
				Type:    "interrupted",
				Message: job.Error,
				Status:  StatusFailed,
			})
		}
		m.entries[job.ID] = &entry{
			job:    job,
			events: events,
			cancel: func() {},
		}
		m.persistLocked(m.entries[job.ID])
	}
}

func (m *Manager) persistLocked(ent *entry) {
	if m.store == nil || ent == nil {
		return
	}
	events := make([]Event, len(ent.events))
	copy(events, ent.events)
	_ = m.store.Save(record{
		SchemaVersion: "job-record/v1",
		Job:           cloneJob(ent.job),
		Events:        events,
	})
}

func (m *Manager) nextID() string {
	seq := m.seq.Add(1)
	return fmt.Sprintf("job_%d_%06d", time.Now().UTC().UnixNano(), seq)
}

func isTerminal(status Status) bool {
	return status == StatusSucceeded || status == StatusFailed || status == StatusCanceled
}

func cloneJob(job Job) Job {
	job.StartedAt = cloneTime(job.StartedAt)
	job.FinishedAt = cloneTime(job.FinishedAt)
	job.Artifacts = append([]Artifact(nil), job.Artifacts...)
	return job
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func timePtr(value time.Time) *time.Time {
	return &value
}

type record struct {
	SchemaVersion string  `json:"schema_version"`
	Job           Job     `json:"job"`
	Events        []Event `json:"events"`
}

type store interface {
	Load() ([]record, error)
	Save(record) error
}

type fileStore struct {
	dir string
}

func (s fileStore) Load() ([]record, error) {
	matches, err := filepath.Glob(filepath.Join(s.dir, "*.json"))
	if err != nil {
		return nil, err
	}
	records := make([]record, 0, len(matches))
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var rec record
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

func (s fileStore) Save(rec record) error {
	if rec.Job.ID == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.dir, rec.Job.ID+".json")
	tmp := path + ".tmp"
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
