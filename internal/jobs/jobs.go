package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound means a job id is unknown to the in-memory manager.
var ErrNotFound = errors.New("job not found")

// Status is the lifecycle state for one async job.
type Status string

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
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Status     Status     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	Result     any        `json:"result,omitempty"`
}

// Runner is the unit of work executed by the manager.
type Runner func(context.Context, *Recorder) (any, error)

// Manager keeps async jobs in memory. It is intentionally process-local; a
// durable queue can replace it later without changing the HTTP contract.
type Manager struct {
	mu      sync.Mutex
	seq     atomic.Uint64
	entries map[string]*entry
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

// NewManager constructs an empty in-memory manager.
func NewManager() *Manager {
	return &Manager{entries: map[string]*entry{}}
}

// Submit registers and starts one job. Jobs use a background context so they
// outlive the HTTP request that created them; callers can cancel via Cancel.
func (m *Manager) Submit(jobType string, runner Runner) Job {
	id := m.nextID()
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	ent := &entry{
		job: Job{
			ID:        id,
			Type:      jobType,
			Status:    StatusQueued,
			CreatedAt: now,
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
	m.mu.Unlock()

	cancel()
	return job, true, nil
}

// Event appends a custom lifecycle event.
func (r *Recorder) Event(eventType, message string) {
	r.manager.addEvent(r.jobID, eventType, message, "")
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
