package workflows

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

	"github.com/fuzzy-searcher-go/internal/jobs"
)

// ErrNotFound means a workflow id is unknown to the manager.
var ErrNotFound = errors.New("workflow not found")

// Status is the lifecycle state for one workflow.
type Status string

const (
	TypeBuildAndAnswer = "build_and_answer"
)

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// Event records a user-visible lifecycle event for a workflow.
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message,omitempty"`
	Status  Status    `json:"status,omitempty"`
}

// Step is one child-job step inside a workflow.
type Step struct {
	Name            string          `json:"name"`
	JobID           string          `json:"job_id,omitempty"`
	Type            string          `json:"type"`
	Status          jobs.Status     `json:"status"`
	InputArtifacts  []jobs.Artifact `json:"input_artifacts,omitempty"`
	OutputArtifacts []jobs.Artifact `json:"output_artifacts,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	Error           string          `json:"error,omitempty"`
}

// Workflow is a snapshot of a multi-job workflow.
type Workflow struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Status        Status          `json:"status"`
	Spec          any             `json:"spec,omitempty"`
	Steps         []Step          `json:"steps,omitempty"`
	Artifacts     []jobs.Artifact `json:"artifacts,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	Error         string          `json:"error,omitempty"`
	Result        any             `json:"result,omitempty"`
}

// BuildAndAnswerSpec is the typed workflow spec for graph construction plus
// final answer generation.
type BuildAndAnswerSpec struct {
	Dataset          string `json:"dataset"`
	Question         string `json:"question"`
	CorpusPath       string `json:"corpus_path,omitempty"`
	SchemaPath       string `json:"schema_path,omitempty"`
	GraphOutputPath  string `json:"graph_output_path,omitempty"`
	ChunksOutputPath string `json:"chunks_output_path,omitempty"`
	CacheDir         string `json:"cache_dir,omitempty"`
	AnswerOutputPath string `json:"answer_output_path,omitempty"`
	BuildMode        string `json:"build_mode,omitempty"`
	AnswerMode       string `json:"answer_mode,omitempty"`
	TopK             int    `json:"top_k,omitempty"`
	ConfigPath       string `json:"config_path,omitempty"`
}

// Runner is the unit of work executed by the workflow manager.
type Runner func(context.Context, *Recorder) (any, error)

// Manager keeps workflow state in memory with optional file persistence.
type Manager struct {
	mu      sync.Mutex
	seq     atomic.Uint64
	entries map[string]*entry
	store   store
}

type entry struct {
	workflow Workflow
	events   []Event
	cancel   context.CancelFunc
}

// Recorder lets a running workflow append events and update steps/artifacts.
type Recorder struct {
	manager    *Manager
	workflowID string
}

// Option customizes a Manager.
type Option func(*Manager)

// NewManager constructs an empty workflow manager.
func NewManager(opts ...Option) *Manager {
	manager := &Manager{entries: map[string]*entry{}}
	for _, opt := range opts {
		opt(manager)
	}
	return manager
}

// WithFileStore persists workflow snapshots as JSON files under dir.
func WithFileStore(dir string) Option {
	return func(manager *Manager) {
		if dir == "" {
			return
		}
		manager.store = fileStore{dir: dir}
		manager.loadStoredWorkflows()
	}
}

// SubmitSpec registers and starts one typed workflow.
func (m *Manager) SubmitSpec(workflowType string, spec any, artifacts []jobs.Artifact, runner Runner) Workflow {
	id := m.nextID()
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	ent := &entry{
		workflow: Workflow{
			SchemaVersion: "workflow/v1",
			ID:            id,
			Type:          workflowType,
			Status:        StatusQueued,
			Spec:          spec,
			Artifacts:     append([]jobs.Artifact(nil), artifacts...),
			CreatedAt:     now,
		},
		events: []Event{{
			Time:    now,
			Type:    "queued",
			Message: "workflow accepted",
			Status:  StatusQueued,
		}},
		cancel: cancel,
	}

	m.mu.Lock()
	m.entries[id] = ent
	m.persistLocked(ent)
	m.mu.Unlock()

	go m.run(ctx, id, runner)
	workflow, _ := m.Get(id)
	return workflow
}

// Get returns a stable snapshot for one workflow.
func (m *Manager) Get(id string) (Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, ok := m.entries[id]
	if !ok {
		return Workflow{}, ErrNotFound
	}
	return cloneWorkflow(ent.workflow), nil
}

// Events returns a copy of the event stream for one workflow.
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

// Cancel requests cancellation for a queued or running workflow.
func (m *Manager) Cancel(id string) (Workflow, bool, error) {
	m.mu.Lock()
	ent, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return Workflow{}, false, ErrNotFound
	}
	if isTerminal(ent.workflow.Status) {
		workflow := cloneWorkflow(ent.workflow)
		m.mu.Unlock()
		return workflow, false, nil
	}
	cancel := ent.cancel
	now := time.Now().UTC()
	ent.events = append(ent.events, Event{
		Time:    now,
		Type:    "cancel_requested",
		Message: "workflow cancellation requested",
		Status:  ent.workflow.Status,
	})
	workflow := cloneWorkflow(ent.workflow)
	m.persistLocked(ent)
	m.mu.Unlock()

	cancel()
	return workflow, true, nil
}

// Event appends a custom lifecycle event.
func (r *Recorder) Event(eventType, message string) {
	r.manager.addEvent(r.workflowID, eventType, message, "")
}

// Artifact updates the status/path for one named workflow artifact.
func (r *Recorder) Artifact(name, status, path string) {
	r.manager.updateArtifact(r.workflowID, name, status, path)
}

// StepStarted records a child job step.
func (r *Recorder) StepStarted(name string, job jobs.Job) {
	r.manager.upsertStep(r.workflowID, Step{
		Name:            name,
		JobID:           job.ID,
		Type:            job.Type,
		Status:          job.Status,
		InputArtifacts:  filterArtifacts(job.Artifacts, "input"),
		OutputArtifacts: filterArtifacts(job.Artifacts, "output"),
		StartedAt:       job.StartedAt,
	}, "step_started", fmt.Sprintf("%s child job started", name))
}

// StepFinished updates a child step from its terminal job state.
func (r *Recorder) StepFinished(name string, job jobs.Job) {
	step := Step{
		Name:            name,
		JobID:           job.ID,
		Type:            job.Type,
		Status:          job.Status,
		InputArtifacts:  filterArtifacts(job.Artifacts, "input"),
		OutputArtifacts: filterArtifacts(job.Artifacts, "output"),
		StartedAt:       job.StartedAt,
		FinishedAt:      job.FinishedAt,
		Error:           job.Error,
	}
	eventType := "step_succeeded"
	message := fmt.Sprintf("%s child job succeeded", name)
	if job.Status != jobs.StatusSucceeded {
		eventType = "step_failed"
		message = fmt.Sprintf("%s child job failed", name)
	}
	r.manager.upsertStep(r.workflowID, step, eventType, message)
}

func (m *Manager) run(ctx context.Context, id string, runner Runner) {
	started := time.Now().UTC()
	m.mu.Lock()
	ent := m.entries[id]
	ent.workflow.Status = StatusRunning
	ent.workflow.StartedAt = timePtr(started)
	ent.events = append(ent.events, Event{
		Time:    started,
		Type:    "running",
		Message: "workflow started",
		Status:  StatusRunning,
	})
	m.persistLocked(ent)
	m.mu.Unlock()

	result, err := runner(ctx, &Recorder{manager: m, workflowID: id})

	finished := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	ent = m.entries[id]
	ent.workflow.FinishedAt = timePtr(finished)
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		ent.workflow.Status = StatusCanceled
		ent.events = append(ent.events, Event{
			Time:    finished,
			Type:    "canceled",
			Message: "workflow canceled",
			Status:  StatusCanceled,
		})
	case err != nil:
		ent.workflow.Status = StatusFailed
		ent.workflow.Error = err.Error()
		ent.events = append(ent.events, Event{
			Time:    finished,
			Type:    "failed",
			Message: err.Error(),
			Status:  StatusFailed,
		})
	default:
		ent.workflow.Status = StatusSucceeded
		ent.workflow.Result = result
		ent.events = append(ent.events, Event{
			Time:    finished,
			Type:    "succeeded",
			Message: "workflow succeeded",
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
		status = ent.workflow.Status
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
	for idx := range ent.workflow.Artifacts {
		if ent.workflow.Artifacts[idx].Name == name {
			if status != "" {
				ent.workflow.Artifacts[idx].Status = status
			}
			if path != "" {
				ent.workflow.Artifacts[idx].Path = path
			}
			m.persistLocked(ent)
			return
		}
	}
}

func (m *Manager) upsertStep(id string, step Step, eventType string, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, ok := m.entries[id]
	if !ok {
		return
	}
	for idx := range ent.workflow.Steps {
		if ent.workflow.Steps[idx].Name == step.Name {
			ent.workflow.Steps[idx] = step
			ent.events = append(ent.events, Event{
				Time:    time.Now().UTC(),
				Type:    eventType,
				Message: message,
				Status:  ent.workflow.Status,
			})
			m.persistLocked(ent)
			return
		}
	}
	ent.workflow.Steps = append(ent.workflow.Steps, step)
	ent.events = append(ent.events, Event{
		Time:    time.Now().UTC(),
		Type:    eventType,
		Message: message,
		Status:  ent.workflow.Status,
	})
	m.persistLocked(ent)
}

func (m *Manager) loadStoredWorkflows() {
	if m.store == nil {
		return
	}
	records, err := m.store.Load()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, record := range records {
		if record.Workflow.ID == "" {
			continue
		}
		workflow := record.Workflow
		if workflow.SchemaVersion == "" {
			workflow.SchemaVersion = "workflow/v1"
		}
		events := append([]Event(nil), record.Events...)
		if !isTerminal(workflow.Status) {
			workflow.Status = StatusFailed
			workflow.Error = "workflow interrupted by service restart"
			workflow.FinishedAt = timePtr(now)
			events = append(events, Event{
				Time:    now,
				Type:    "interrupted",
				Message: workflow.Error,
				Status:  StatusFailed,
			})
		}
		m.entries[workflow.ID] = &entry{
			workflow: workflow,
			events:   events,
			cancel:   func() {},
		}
		m.persistLocked(m.entries[workflow.ID])
	}
}

func (m *Manager) persistLocked(ent *entry) {
	if m.store == nil || ent == nil {
		return
	}
	events := make([]Event, len(ent.events))
	copy(events, ent.events)
	_ = m.store.Save(record{
		SchemaVersion: "workflow-record/v1",
		Workflow:      cloneWorkflow(ent.workflow),
		Events:        events,
	})
}

func (m *Manager) nextID() string {
	seq := m.seq.Add(1)
	return fmt.Sprintf("wf_%d_%06d", time.Now().UTC().UnixNano(), seq)
}

func filterArtifacts(artifacts []jobs.Artifact, role string) []jobs.Artifact {
	filtered := []jobs.Artifact{}
	for _, artifact := range artifacts {
		if artifact.Role == role {
			filtered = append(filtered, artifact)
		}
	}
	return filtered
}

func isTerminal(status Status) bool {
	return status == StatusSucceeded || status == StatusFailed || status == StatusCanceled
}

func cloneWorkflow(workflow Workflow) Workflow {
	workflow.StartedAt = cloneTime(workflow.StartedAt)
	workflow.FinishedAt = cloneTime(workflow.FinishedAt)
	workflow.Artifacts = append([]jobs.Artifact(nil), workflow.Artifacts...)
	workflow.Steps = append([]Step(nil), workflow.Steps...)
	for idx := range workflow.Steps {
		workflow.Steps[idx].StartedAt = cloneTime(workflow.Steps[idx].StartedAt)
		workflow.Steps[idx].FinishedAt = cloneTime(workflow.Steps[idx].FinishedAt)
		workflow.Steps[idx].InputArtifacts = append([]jobs.Artifact(nil), workflow.Steps[idx].InputArtifacts...)
		workflow.Steps[idx].OutputArtifacts = append([]jobs.Artifact(nil), workflow.Steps[idx].OutputArtifacts...)
	}
	return workflow
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
	SchemaVersion string   `json:"schema_version"`
	Workflow      Workflow `json:"workflow"`
	Events        []Event  `json:"events"`
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
	if rec.Workflow.ID == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.dir, rec.Workflow.ID+".json")
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
