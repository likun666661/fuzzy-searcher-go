package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fuzzy-searcher-go/internal/jobs"
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
