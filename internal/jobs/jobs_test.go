package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/likun666661/youtu-rag-service/internal/jobs"
)

func TestManagerSubmitSuccess(t *testing.T) {
	manager := jobs.NewManager()
	job := manager.Submit("test", func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		recorder.Event("step", "work happened")
		return map[string]string{"ok": "true"}, nil
	})

	job = waitForStatus(t, manager, job.ID, jobs.StatusSucceeded)
	if job.Result == nil {
		t.Fatalf("job result was nil: %#v", job)
	}
	events, err := manager.Events(job.ID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) < 4 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Type != "queued" || events[len(events)-1].Type != "succeeded" {
		t.Fatalf("events = %#v", events)
	}
}

func TestManagerSubmitFailure(t *testing.T) {
	manager := jobs.NewManager()
	job := manager.Submit("test", func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		return nil, errors.New("boom")
	})

	job = waitForStatus(t, manager, job.ID, jobs.StatusFailed)
	if job.Error != "boom" {
		t.Fatalf("job error = %q", job.Error)
	}
}

func TestManagerCancel(t *testing.T) {
	manager := jobs.NewManager()
	started := make(chan struct{})
	job := manager.Submit("test", func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	if _, canceled, err := manager.Cancel(job.ID); err != nil || !canceled {
		t.Fatalf("cancel canceled=%v err=%v", canceled, err)
	}
	job = waitForStatus(t, manager, job.ID, jobs.StatusCanceled)
	if job.FinishedAt == nil {
		t.Fatalf("job missing finished_at: %#v", job)
	}
}

func TestManagerFileStoreReloadsCompletedJobs(t *testing.T) {
	dir := t.TempDir()
	manager := jobs.NewManager(jobs.WithFileStore(dir))
	spec := jobs.RetrieveSpec{Dataset: "demo", Question: "hello", TopK: 3, Mode: "native"}
	artifacts := []jobs.Artifact{{
		Name:          "retrieve_result",
		Role:          "output",
		Kind:          "retrieve_result_json",
		SchemaVersion: "retrieve-result/v1",
		Status:        "inline",
	}}
	job := manager.SubmitSpec(jobs.TypeRetrieve, spec, artifacts, func(ctx context.Context, recorder *jobs.Recorder) (any, error) {
		recorder.Event("step", "persist me")
		return map[string]string{"answer": "ok"}, nil
	})
	job = waitForStatus(t, manager, job.ID, jobs.StatusSucceeded)

	reloaded := jobs.NewManager(jobs.WithFileStore(dir))
	loaded, err := reloaded.Get(job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if loaded.SchemaVersion != "service-job/v1" || loaded.Type != jobs.TypeRetrieve || loaded.Status != jobs.StatusSucceeded || loaded.Result == nil {
		t.Fatalf("loaded job = %#v", loaded)
	}
	if len(loaded.Artifacts) != 1 || loaded.Artifacts[0].SchemaVersion != "retrieve-result/v1" {
		t.Fatalf("loaded artifacts = %#v", loaded.Artifacts)
	}
	events, err := reloaded.Events(job.ID)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) < 4 || events[len(events)-1].Type != "succeeded" {
		t.Fatalf("loaded events = %#v", events)
	}
}

func TestManagerFileStoreMarksRunningJobsInterruptedOnLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := map[string]any{
		"schema_version": "job-record/v1",
		"job": map[string]any{
			"schema_version": "service-job/v1",
			"id":             "job_stale",
			"type":           jobs.TypeRetrieve,
			"status":         jobs.StatusRunning,
			"created_at":     now,
			"started_at":     now,
		},
		"events": []map[string]any{{
			"time":    now,
			"type":    "running",
			"message": "job started",
			"status":  jobs.StatusRunning,
		}},
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "job_stale.json"), body, 0o600); err != nil {
		t.Fatalf("write stale record: %v", err)
	}

	manager := jobs.NewManager(jobs.WithFileStore(dir))
	loaded, err := manager.Get("job_stale")
	if err != nil {
		t.Fatalf("load stale job: %v", err)
	}
	if loaded.Status != jobs.StatusFailed || loaded.Error != "job interrupted by service restart" || loaded.FinishedAt == nil {
		t.Fatalf("loaded stale job = %#v", loaded)
	}
	events, err := manager.Events("job_stale")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "interrupted" || events[len(events)-1].Status != jobs.StatusFailed {
		t.Fatalf("loaded stale events = %#v", events)
	}
}

func TestManagerUnknownJob(t *testing.T) {
	manager := jobs.NewManager()
	if _, err := manager.Get("missing"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("get missing err = %v", err)
	}
	if _, err := manager.Events("missing"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("events missing err = %v", err)
	}
	if _, _, err := manager.Cancel("missing"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("cancel missing err = %v", err)
	}
}

func waitForStatus(t *testing.T, manager *jobs.Manager, id string, want jobs.Status) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := manager.Get(id)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := manager.Get(id)
	t.Fatalf("job %s did not reach %s; last status = %s", id, want, job.Status)
	return jobs.Job{}
}
