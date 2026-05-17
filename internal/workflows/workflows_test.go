package workflows_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fuzzy-searcher-go/internal/jobs"
	"github.com/fuzzy-searcher-go/internal/workflows"
)

func TestManagerRunsWorkflowAndRecordsSteps(t *testing.T) {
	manager := workflows.NewManager()
	workflow := manager.SubmitSpec(workflows.TypeBuildAndAnswer, workflows.BuildAndAnswerSpec{
		Dataset:  "demo",
		Question: "Who?",
	}, []jobs.Artifact{{
		Name:   "answer",
		Role:   "output",
		Kind:   "answer_json",
		Status: "pending",
	}}, func(ctx context.Context, recorder *workflows.Recorder) (any, error) {
		now := time.Now().UTC()
		job := jobs.Job{
			ID:         "job_1",
			Type:       jobs.TypeAnswer,
			Status:     jobs.StatusSucceeded,
			StartedAt:  &now,
			FinishedAt: &now,
			Artifacts: []jobs.Artifact{{
				Name:   "answer",
				Role:   "output",
				Kind:   "answer_json",
				Status: "written",
			}},
		}
		recorder.StepStarted("answer", job)
		recorder.StepFinished("answer", job)
		recorder.Artifact("answer", "written", "/tmp/answer.json")
		return map[string]string{"answer": "ok"}, nil
	})

	workflow = waitForStatus(t, manager, workflow.ID, workflows.StatusSucceeded)
	if workflow.SchemaVersion != "workflow/v1" || workflow.Result == nil {
		t.Fatalf("workflow = %#v", workflow)
	}
	if len(workflow.Steps) != 1 || workflow.Steps[0].Name != "answer" || workflow.Steps[0].JobID != "job_1" {
		t.Fatalf("steps = %#v", workflow.Steps)
	}
	if len(workflow.Artifacts) != 1 || workflow.Artifacts[0].Status != "written" {
		t.Fatalf("artifacts = %#v", workflow.Artifacts)
	}
}

func TestManagerFileStoreReloadsCompletedWorkflows(t *testing.T) {
	dir := t.TempDir()
	manager := workflows.NewManager(workflows.WithFileStore(dir))
	workflow := manager.SubmitSpec(workflows.TypeBuildAndAnswer, workflows.BuildAndAnswerSpec{
		Dataset:  "demo",
		Question: "Who?",
	}, nil, func(ctx context.Context, recorder *workflows.Recorder) (any, error) {
		recorder.Event("step", "persist me")
		return map[string]string{"ok": "true"}, nil
	})
	workflow = waitForStatus(t, manager, workflow.ID, workflows.StatusSucceeded)

	reloaded := workflows.NewManager(workflows.WithFileStore(dir))
	loaded, err := reloaded.Get(workflow.ID)
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if loaded.SchemaVersion != "workflow/v1" || loaded.Type != workflows.TypeBuildAndAnswer || loaded.Status != workflows.StatusSucceeded || loaded.Result == nil {
		t.Fatalf("loaded workflow = %#v", loaded)
	}
	events, err := reloaded.Events(workflow.ID)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) < 4 || events[len(events)-1].Type != "succeeded" {
		t.Fatalf("events = %#v", events)
	}
}

func TestManagerMarksStaleRunningWorkflowsInterrupted(t *testing.T) {
	dir := t.TempDir()
	manager := workflows.NewManager(workflows.WithFileStore(dir))
	started := make(chan struct{})
	block := make(chan struct{})
	workflow := manager.SubmitSpec(workflows.TypeBuildAndAnswer, nil, nil, func(ctx context.Context, recorder *workflows.Recorder) (any, error) {
		close(started)
		<-block
		return nil, nil
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("workflow did not start")
	}

	reloaded := workflows.NewManager(workflows.WithFileStore(dir))
	loaded, err := reloaded.Get(workflow.ID)
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if loaded.Status != workflows.StatusFailed || !strings.Contains(loaded.Error, "interrupted") {
		t.Fatalf("loaded workflow = %#v", loaded)
	}
	events, err := reloaded.Events(workflow.ID)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if events[len(events)-1].Type != "interrupted" {
		t.Fatalf("events = %#v", events)
	}
	close(block)
}

func waitForStatus(t *testing.T, manager *workflows.Manager, id string, want workflows.Status) workflows.Workflow {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		workflow, err := manager.Get(id)
		if err != nil {
			t.Fatalf("get workflow: %v", err)
		}
		if workflow.Status == want {
			return workflow
		}
		time.Sleep(10 * time.Millisecond)
	}
	workflow, _ := manager.Get(id)
	t.Fatalf("workflow did not reach %s: %#v", want, workflow)
	return workflows.Workflow{}
}
