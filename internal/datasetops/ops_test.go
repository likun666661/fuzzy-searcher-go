package datasetops_test

import (
	"path/filepath"
	"testing"

	"github.com/fuzzy-searcher-go/internal/datasetops"
	"github.com/fuzzy-searcher-go/internal/jobs"
)

func TestStorePersistsAndFiltersOperations(t *testing.T) {
	dir := t.TempDir()
	store := datasetops.NewStore(dir)
	first := store.Append(datasetops.Operation{
		Dataset: "alpha",
		Type:    datasetops.TypeImport,
		Status:  "succeeded",
		Artifacts: []jobs.Artifact{{
			Name:   "corpus",
			Status: "written",
			Path:   filepath.Join(dir, "corpus.json"),
		}},
	})
	second := store.Append(datasetops.Operation{
		Dataset: "beta",
		Type:    datasetops.TypeDelete,
		Status:  "succeeded",
	})
	if first.SchemaVersion != "dataset-operation/v1" || first.ID == "" || second.ID == "" {
		t.Fatalf("operations = %#v %#v", first, second)
	}

	loaded := datasetops.NewStore(dir)
	got, err := loaded.Get(first.ID)
	if err != nil {
		t.Fatalf("get persisted operation: %v", err)
	}
	if got.Dataset != "alpha" || got.Type != datasetops.TypeImport || len(got.Artifacts) != 1 {
		t.Fatalf("got = %#v", got)
	}
	if len(loaded.List("")) != 2 {
		t.Fatalf("all operations = %#v", loaded.List(""))
	}
	alpha := loaded.List("alpha")
	if len(alpha) != 1 || alpha[0].ID != first.ID {
		t.Fatalf("alpha operations = %#v", alpha)
	}
}

func TestStoreMarksInterruptedOperationsOnLoad(t *testing.T) {
	dir := t.TempDir()
	store := datasetops.NewStore(dir)
	running := store.Append(datasetops.Operation{
		Dataset: "alpha",
		Type:    datasetops.TypeRebuild,
		Status:  "running",
	})

	loaded := datasetops.NewStore(dir)
	got, err := loaded.Get(running.ID)
	if err != nil {
		t.Fatalf("get persisted operation: %v", err)
	}
	if got.Status != "failed" || got.Error != "operation interrupted by service restart" || got.FinishedAt == nil {
		t.Fatalf("got = %#v", got)
	}
}
