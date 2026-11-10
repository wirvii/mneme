package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/specreview"
)

// validReviewSpecDoc and validReviewPlanDoc are minimal valid reports of each
// moment, shared by the service tests of SPEC-181.
const validReviewSpecDoc = `schema_version = 1

[[revisor]]
papel = "comprension"
estado = "completo"
parafrasis_propuesta = "Propone revisar la spec sin contexto."
parafrasis_hoy = "Hoy nadie la revisa."

[[revisor]]
papel = "diseno"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"
`

const validReviewPlanDoc = `schema_version = 1

[[revisor]]
papel = "preparacion"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"
`

// TestSpecDocWrite_Review covers SPEC-181 C4: both review kinds write their
// own file inside the spec's workflow directory, and an invalid or
// wrong-moment document is rejected without touching the disk.
func TestSpecDocWrite_Review(t *testing.T) {
	ctx := context.Background()

	t.Run("each kind writes its own file", func(t *testing.T) {
		svc, workflowDir := newContractTestService(t, "wirvii/mneme")
		spec := insertContractTestSpec(t, svc, "SPEC-100", model.LaneStandard, model.SpecStatusSpeccing)

		cases := []struct {
			kind model.SpecDocKind
			file string
			doc  string
		}{
			{model.SpecDocKindReviewSpec, "revision-spec.toml", validReviewSpecDoc},
			{model.SpecDocKindReviewPlan, "revision-plan.toml", validReviewPlanDoc},
		}
		for _, tc := range cases {
			resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: tc.kind, Content: tc.doc})
			if err != nil {
				t.Fatalf("SpecDocWrite(%s): %v", tc.kind, err)
			}
			want := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, tc.file)
			if resp.Path != want {
				t.Errorf("path = %q, want %q", resp.Path, want)
			}
			data, err := os.ReadFile(want)
			if err != nil || string(data) != tc.doc {
				t.Errorf("file %s: err=%v content mismatch", tc.file, err)
			}
		}
	})

	t.Run("invalid content is rejected and nothing is written", func(t *testing.T) {
		svc, workflowDir := newContractTestService(t, "wirvii/mneme")
		spec := insertContractTestSpec(t, svc, "SPEC-100", model.LaneStandard, model.SpecStatusSpeccing)

		_, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
			ID: spec.ID, Kind: model.SpecDocKindReviewSpec, Content: "schema_version = 1\n",
		})
		if !errors.Is(err, model.ErrInvalidReviewReport) {
			t.Fatalf("err = %v, want ErrInvalidReviewReport", err)
		}
		if !errors.Is(err, specreview.ErrInvalid) {
			t.Errorf("err = %v, want it to keep the specreview cause", err)
		}
		if _, statErr := os.Stat(filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "revision-spec.toml")); !os.IsNotExist(statErr) {
			t.Errorf("file exists after a rejected write: %v", statErr)
		}
	})

	t.Run("rejected overwrite keeps the previous bytes", func(t *testing.T) {
		svc, _ := newContractTestService(t, "wirvii/mneme")
		spec := insertContractTestSpec(t, svc, "SPEC-100", model.LaneStandard, model.SpecStatusSpeccing)

		resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindReviewSpec, Content: validReviewSpecDoc})
		if err != nil {
			t.Fatalf("first write: %v", err)
		}
		if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindReviewSpec, Content: "garbage {{{"}); !errors.Is(err, model.ErrInvalidReviewReport) {
			t.Fatalf("second write err = %v, want ErrInvalidReviewReport", err)
		}
		data, err := os.ReadFile(resp.Path)
		if err != nil || string(data) != validReviewSpecDoc {
			t.Errorf("content changed after a rejected overwrite (err=%v)", err)
		}
	})

	t.Run("a plan-moment report sent as review-spec is rejected", func(t *testing.T) {
		svc, _ := newContractTestService(t, "wirvii/mneme")
		spec := insertContractTestSpec(t, svc, "SPEC-100", model.LaneStandard, model.SpecStatusSpeccing)

		_, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindReviewSpec, Content: validReviewPlanDoc})
		if !errors.Is(err, model.ErrInvalidReviewReport) {
			t.Errorf("err = %v, want ErrInvalidReviewReport", err)
		}
		_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindReviewPlan, Content: validReviewSpecDoc})
		if !errors.Is(err, model.ErrInvalidReviewReport) {
			t.Errorf("spec report as review-plan: err = %v, want ErrInvalidReviewReport", err)
		}
	})
}
