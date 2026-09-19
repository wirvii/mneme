package store

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

func TestSpecExecutionModel_ProjectionAndUpdate(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	spec := &model.Spec{ID: "SPEC-001", Title: "delivery", Status: model.SpecStatusDraft, Project: "p", Lane: model.LaneStandard}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSpec(ctx, spec.ID)
	if err != nil || got.ExecutionModel != model.ExecutionModelLegacy {
		t.Fatalf("GetSpec model=%q err=%v", got.ExecutionModel, err)
	}
	list, _, unreadable, err := s.ListSpecs(ctx, "p", "", 0)
	if err != nil || len(unreadable) != 0 || len(list) != 1 || list[0].ExecutionModel != model.ExecutionModelLegacy {
		t.Fatalf("ListSpecs=%v unreadable=%v err=%v", list, unreadable, err)
	}
	if err := s.UpdateSpecExecutionModel(ctx, spec.ID, model.ExecutionModelDeliveryV2); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSpecStatus(ctx, spec.ID, model.SpecStatusDraft, model.SpecStatusSpeccing, "coordinator", ""); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSpec(ctx, spec.ID)
	if err != nil || got.ExecutionModel != model.ExecutionModelDeliveryV2 {
		t.Fatalf("updated model=%q err=%v", got.ExecutionModel, err)
	}
	if err := s.UpdateSpecExecutionModel(ctx, spec.ID, "future"); err == nil {
		t.Fatal("invalid execution model accepted")
	}
}
