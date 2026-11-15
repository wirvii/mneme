// Package service — tests for SPEC-182: the delivery contract block of the
// entrega section, validated on write and read back by DeliveryCheck and
// AttachDeliveryState. A real in-memory SQLite store and real files in a temp
// workflow directory throughout.
package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/delivery"
	"github.com/wirvii/mneme/internal/model"
)

// deliveryProject is the project slug every test in this file uses.
const deliveryProject = "wirvii/mneme"

// contractBody is a valid hecho_con_dudas block body, without delimiters.
const contractBody = `schema_version = 1
estado = "hecho_con_dudas"
arbol = "2ba81d8c4e1f"
riesgo = { item = 3, razon = "spec_status gana campos" }

[[evidencia]]
comando = "go test ./internal/delivery/"
exit_code = 0
salida = "ok"
criterio = "AC1"
`

// blockedContractBody is a valid bloqueado block body with one evidencia.
const blockedContractBody = `schema_version = 1
estado = "bloqueado"
riesgo = "none"

[[evidencia]]
comando = "make test"
exit_code = 2
salida = "FAIL: TestX"
`

// entrega wraps a block body in prose and the two delimiter lines, the way an
// implementer writes the section.
func entrega(body string) string {
	return "Implementado.\n\n" + delivery.StartMarker + "\n```toml\n" + body + "```\n" + delivery.EndMarker + "\n\nNotas para QA.\n"
}

// writeEntrega writes content to spec's entrega section and returns the raw
// result, so a test can assert on refusals.
func writeEntrega(svc *SDDService, id, content string) (*model.SpecDocWriteResponse, error) {
	return svc.SpecDocWrite(context.Background(), model.SpecDocWriteRequest{
		ID: id, Kind: model.SpecDocKindTask, Section: model.TaskSectionDelivery, Content: content,
	})
}

// mustWriteEntrega is writeEntrega that fails the test on error.
func mustWriteEntrega(t *testing.T, svc *SDDService, id, content string) *model.SpecDocWriteResponse {
	t.Helper()
	resp, err := writeEntrega(svc, id, content)
	if err != nil {
		t.Fatalf("SpecDocWrite(entrega): %v", err)
	}
	return resp
}

// newTrivialImplementing quick-starts a trivial spec into implementing.
func newTrivialImplementing(t *testing.T, svc *SDDService) *model.Spec {
	t.Helper()
	ctx := context.Background()
	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "t", Lane: model.LaneTrivial, Scope: "internal/x/**"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: "fixture", By: "orch"})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Fatalf("trivial spec is %s, want implementing", spec.Status)
	}
	return spec
}

// assertNoTempFiles fails when writeTaskSection left a temp file behind.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(dir, ".tarea-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// readOrNil returns path's bytes, or nil when it does not exist.
func readOrNil(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestSpecDocWrite_Delivery_InvalidLeavesFileByteIdentical covers C3: every
// invalid block is refused with the sentinel and leaves tarea.md exactly as
// it was, or absent when it did not exist.
func TestSpecDocWrite_Delivery_InvalidLeavesFileByteIdentical(t *testing.T) {
	invalid := []struct {
		name     string
		content  string
		sentinel error
	}{
		{"R1_duplicate_start", delivery.StartMarker + "\n" + entrega(contractBody), delivery.ErrInvalid},
		{"R5_unknown_state", entrega(strings.Replace(contractBody, "hecho_con_dudas", "terminado", 1)), delivery.ErrInvalid},
		{"R7_done_without_evidence", entrega("schema_version = 1\nestado = \"hecho\"\narbol = \"2ba81d8\"\nriesgo = \"none\"\n"), delivery.ErrInvalid},
		{"R9_item_out_of_range", entrega(strings.Replace(contractBody, "item = 3", "item = 7", 1)), delivery.ErrInvalid},
		{
			"R10_stopped_with_done",
			entrega(strings.Replace(contractBody, "hecho_con_dudas", "hecho", 1) +
				"\n[[prueba_en_rojo]]\nnombre = \"TestX\"\naccion = \"parada_y_reportada\"\nmotivo = \"m\"\n"),
			delivery.ErrInvalid,
		},
		{"schema_2", entrega("schema_version = 2\n"), delivery.ErrUnsupportedSchema},
	}

	for _, existing := range []bool{true, false} {
		name := "absent_file"
		if existing {
			name = "existing_file"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			svc, _, _ := newReviewTestService(t, deliveryProject)
			spec := advanceToImplementing(t, svc, ctx, "entrega")
			path := reportPath(t, svc, spec, model.SpecDocKindTask)
			if existing {
				mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
			}
			for _, tc := range invalid {
				t.Run(tc.name, func(t *testing.T) {
					before := readOrNil(t, path)
					_, err := writeEntrega(svc, spec.ID, tc.content)
					if !errors.Is(err, model.ErrInvalidDeliveryContract) {
						t.Fatalf("err = %v, want ErrInvalidDeliveryContract", err)
					}
					if !errors.Is(err, tc.sentinel) {
						t.Errorf("err = %v, want errors.Is %v", err, tc.sentinel)
					}
					after := readOrNil(t, path)
					if existing && !bytes.Equal(before, after) {
						t.Errorf("tarea.md changed:\nbefore:\n%s\nafter:\n%s", before, after)
					}
					if !existing && after != nil {
						t.Errorf("tarea.md was created by a refused write:\n%s", after)
					}
					assertNoTempFiles(t, filepath.Dir(path))
				})
			}
		})
	}
}

// TestSpecDocWrite_Delivery_MissingBlock covers C4's first half: no block is
// refused only for a standard spec in implementing; everywhere else it is
// written and reported as missing.
func TestSpecDocWrite_Delivery_MissingBlock(t *testing.T) {
	ctx := context.Background()
	const prose = "Hecho, sin bloque."

	t.Run("standard_implementing_refused", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "entrega")
		path := reportPath(t, svc, spec, model.SpecDocKindTask)
		mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
		before := readOrNil(t, path)

		_, err := writeEntrega(svc, spec.ID, prose)
		if !errors.Is(err, model.ErrDeliveryContractMissing) {
			t.Fatalf("err = %v, want ErrDeliveryContractMissing", err)
		}
		if errors.Is(err, model.ErrInvalidDeliveryContract) {
			t.Errorf("err = %v also wraps ErrInvalidDeliveryContract", err)
		}
		for _, want := range []string{"references/entrega.md", "mneme-flow", delivery.StartMarker, delivery.EndMarker} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to contain %q", err, want)
			}
		}
		if after := readOrNil(t, path); !bytes.Equal(before, after) {
			t.Errorf("tarea.md changed by a refused write")
		}
	})

	accepted := []struct {
		name  string
		build func(t *testing.T) (*SDDService, *model.Spec)
	}{
		{"standard_draft", func(t *testing.T) (*SDDService, *model.Spec) {
			svc, spec, _ := newTaskSpec(t)
			return svc, spec
		}},
		{"standard_qa", func(t *testing.T) (*SDDService, *model.Spec) {
			svc, _, _ := newReviewTestService(t, deliveryProject)
			return svc, advanceToQA(t, svc, ctx, "entrega")
		}},
		{"trivial_implementing", func(t *testing.T) (*SDDService, *model.Spec) {
			svc, _, _ := newReviewTestService(t, deliveryProject)
			return svc, newTrivialImplementing(t, svc)
		}},
	}
	for _, tc := range accepted {
		t.Run(tc.name+"_warned", func(t *testing.T) {
			svc, spec := tc.build(t)
			resp := mustWriteEntrega(t, svc, spec.ID, prose)
			if resp.Contract != model.DeliveryContractMissing || resp.DeliveryState != "" {
				t.Errorf("response = %+v, want contract missing and no delivery_state", resp)
			}
		})
	}

	t.Run("valid_block", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "entrega")
		resp := mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
		if resp.Contract != model.DeliveryContractValid || resp.DeliveryState != delivery.StateDoneConcerns {
			t.Errorf("response = %+v, want valid hecho_con_dudas", resp)
		}
		if resp.Section != string(model.TaskSectionDelivery) {
			t.Errorf("section = %q", resp.Section)
		}
	})

	t.Run("blocked_with_evidence_valid", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "entrega")
		resp := mustWriteEntrega(t, svc, spec.ID, entrega(blockedContractBody))
		if resp.Contract != model.DeliveryContractValid || resp.DeliveryState != delivery.StateBlocked {
			t.Errorf("response = %+v, want valid bloqueado", resp)
		}
	})
}

// TestSpecDocWrite_Delivery_RoundTrip covers C4's last part: what the write
// path validated is what the read path parses back from disk, even when a
// salida carries the tarea marker prefix that escapeTaskContent rewrites.
func TestSpecDocWrite_Delivery_RoundTrip(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newReviewTestService(t, deliveryProject)
	spec := advanceToImplementing(t, svc, ctx, "entrega")

	body := strings.Replace(contractBody, `salida = "ok"`, `salida = "linea con `+tareaMarkerPrefix+`:x -->"`, 1)
	resp := mustWriteEntrega(t, svc, spec.ID, "## Entrega\n\n"+entrega(body))
	if resp.Contract != model.DeliveryContractValid {
		t.Fatalf("write contract = %q, want valid", resp.Contract)
	}

	section, err := readTaskSection(resp.Path, model.TaskSectionDelivery)
	if err != nil {
		t.Fatalf("readTaskSection: %v", err)
	}
	if strings.HasPrefix(section, "## Entrega") {
		t.Errorf("section still starts with its heading:\n%s", section)
	}
	c, err := delivery.Parse([]byte(section))
	if err != nil {
		t.Fatalf("delivery.Parse(section on disk): %v\n%s", err, section)
	}
	if c.State != resp.DeliveryState {
		t.Errorf("state on disk = %q, write reported %q", c.State, resp.DeliveryState)
	}
	if !strings.Contains(c.Evidence[0].Output, tareaEscapedPrefix) {
		t.Errorf("salida on disk = %q, want the escaped prefix", c.Evidence[0].Output)
	}

	check, err := svc.DeliveryCheck(ctx, spec.ID)
	if err != nil {
		t.Fatalf("DeliveryCheck: %v", err)
	}
	if check.Contract != model.DeliveryContractValid || check.State != resp.DeliveryState {
		t.Errorf("DeliveryCheck = %+v, want valid %s", check, resp.DeliveryState)
	}
}

// TestSpecDocWrite_Delivery_OtherSectionsUntouched covers C4: only entrega is
// validated; other sections and kinds carry delimiters freely.
func TestSpecDocWrite_Delivery_OtherSectionsUntouched(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newReviewTestService(t, deliveryProject)
	spec := advanceToImplementing(t, svc, ctx, "entrega")
	broken := "Cita del formato: " + delivery.StartMarker + "\n" + delivery.StartMarker + "\nsin cierre\n"

	for _, s := range []model.TaskSection{model.TaskSectionPlan, model.TaskSectionReview} {
		resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindTask, Section: s, Content: broken})
		if err != nil {
			t.Fatalf("SpecDocWrite(%s): %v", s, err)
		}
		if resp.Contract != "" || resp.DeliveryState != "" {
			t.Errorf("section %s: response = %+v, want no contract fields", s, resp)
		}
	}
	resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindQAReport, Content: broken})
	if err != nil {
		t.Fatalf("SpecDocWrite(qa-report): %v", err)
	}
	if resp.Contract != "" {
		t.Errorf("qa-report: contract = %q, want empty", resp.Contract)
	}
}

// latestImplementingEntry returns when spec last entered implementing.
func latestImplementingEntry(t *testing.T, svc *SDDService, id string) time.Time {
	t.Helper()
	history, err := svc.SpecHistory(context.Background(), id)
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	at, ok := latestEntry(history, model.SpecStatusImplementing)
	if !ok {
		t.Fatal("no entry to implementing")
	}
	return at
}

// breakEntregaOnDisk rewrites tarea.md by hand so its block is invalid, the
// way a person editing the file outside mneme would.
func breakEntregaOnDisk(t *testing.T, path string) {
	t.Helper()
	raw := readOrNil(t, path)
	broken := strings.Replace(string(raw), `estado = "hecho_con_dudas"`, `estado = "terminado"`, 1)
	if broken == string(raw) {
		t.Fatal("breakEntregaOnDisk: no estado to break")
	}
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestDeliveryCheck covers C5: the API SPEC-183 consumes tells valid, missing,
// invalid and stale apart and never errors except for an unknown spec.
func TestDeliveryCheck(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		setup func(t *testing.T, svc *SDDService) *model.Spec
		check func(t *testing.T, svc *SDDService, spec *model.Spec, c *model.DeliveryCheck)
	}{
		{
			name: "no_workflow_dir",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
				svc.config.Workflow.Dir = ""
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractMissing || c.Detail != noWorkflowDirMessage || c.Path != "" || c.Current {
					t.Errorf("check = %+v, want missing without workflow dir", c)
				}
			},
		},
		{
			name: "config_nil",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				svc.config = nil
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractMissing || c.Detail != noWorkflowDirMessage || c.Path != "" || c.Current {
					t.Errorf("check = %+v, want missing without config", c)
				}
			},
		},
		{
			name: "no_tarea_md",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				return advanceToImplementing(t, svc, ctx, "d")
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractMissing || c.Path != "" || c.Current || c.Detail == "" {
					t.Errorf("check = %+v, want missing with detail and no path", c)
				}
			},
		},
		{
			name: "section_without_block",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				writeTask(t, svc, spec.ID, model.TaskSectionPlan, "Plan.")
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractMissing || c.Path == "" || !c.Current || c.Detail == "" {
					t.Errorf("check = %+v, want missing, current, with path", c)
				}
				if c.Satisfied() {
					t.Error("Satisfied() = true without a block")
				}
			},
		},
		{
			name: "valid_current_satisfied",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
				return spec
			},
			check: func(t *testing.T, _ *SDDService, spec *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractValid || c.State != delivery.StateDoneConcerns || !c.Current || c.Detail != "" {
					t.Errorf("check = %+v, want valid current", c)
				}
				if c.SpecID != spec.ID || c.Status != model.SpecStatusImplementing || c.Lane != model.LaneStandard {
					t.Errorf("check identity = %+v", c)
				}
				if !c.Satisfied() {
					t.Error("Satisfied() = false, want true")
				}
			},
		},
		{
			name: "valid_stale_after_reject",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				resp := mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
				if _, err := advance(ctx, svc, spec.ID); err != nil {
					t.Fatalf("advance to qa: %v", err)
				}
				if _, err := svc.SpecReject(ctx, model.SpecRejectRequest{
					ID: spec.ID, Reason: "falla", By: "qa-agent",
					Findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "no se cumple"}},
				}); err != nil {
					t.Fatalf("SpecReject: %v", err)
				}
				entered := latestImplementingEntry(t, svc, spec.ID)
				setMtime(t, resp.Path, entered.Add(-5*time.Second))
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractValid || c.Current || c.Status != model.SpecStatusImplementing {
					t.Errorf("check = %+v, want valid but stale in implementing", c)
				}
				if c.Satisfied() {
					t.Error("Satisfied() = true for a stale block")
				}
			},
		},
		{
			name: "hand_edited_invalid",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				resp := mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
				breakEntregaOnDisk(t, resp.Path)
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractInvalid || c.State != "" || !strings.Contains(c.Detail, "estado") || c.Path == "" {
					t.Errorf("check = %+v, want invalid naming estado", c)
				}
			},
		},
		{
			name: "tarea_md_without_markers",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				writeTaskFile(t, svc, spec)
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractInvalid || !strings.Contains(c.Detail, "marker") || c.Path == "" {
					t.Errorf("check = %+v, want invalid malformed tarea.md", c)
				}
			},
		},
		{
			name: "qa_valid_not_satisfied",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := advanceToImplementing(t, svc, ctx, "d")
				mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
				spec, err := advance(ctx, svc, spec.ID)
				if err != nil {
					t.Fatalf("advance to qa: %v", err)
				}
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractValid || c.Status != model.SpecStatusQA {
					t.Errorf("check = %+v, want valid in qa", c)
				}
				if c.Satisfied() {
					t.Error("Satisfied() = true in qa")
				}
			},
		},
		{
			name: "trivial_valid_not_satisfied",
			setup: func(t *testing.T, svc *SDDService) *model.Spec {
				spec := newTrivialImplementing(t, svc)
				mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
				return spec
			},
			check: func(t *testing.T, _ *SDDService, _ *model.Spec, c *model.DeliveryCheck) {
				if c.Contract != model.DeliveryContractValid || c.Lane != model.LaneTrivial || !c.Current {
					t.Errorf("check = %+v, want valid current trivial", c)
				}
				if c.Satisfied() {
					t.Error("Satisfied() = true for a trivial spec")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newReviewTestService(t, deliveryProject)
			spec := tc.setup(t, svc)
			c, err := svc.DeliveryCheck(ctx, spec.ID)
			if err != nil {
				t.Fatalf("DeliveryCheck: %v", err)
			}
			tc.check(t, svc, spec, c)
		})
	}

	t.Run("unknown_id", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		if _, err := svc.DeliveryCheck(ctx, "SPEC-999"); err == nil {
			t.Fatal("DeliveryCheck(unknown) = nil error")
		}
	})
}

// TestAttachDeliveryState covers C5's spec_status half and the exact presence
// condition: tarea.md exists and the spec is implementing, qa, audit or done.
func TestAttachDeliveryState(t *testing.T) {
	ctx := context.Background()

	status := func(t *testing.T, svc *SDDService, id string) *model.SpecStatusResponse {
		t.Helper()
		resp, err := svc.SpecStatus(ctx, id)
		if err != nil {
			t.Fatalf("SpecStatus: %v", err)
		}
		svc.AttachDeliveryState(resp)
		return resp
	}

	t.Run("valid_in_implementing", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "a")
		mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
		resp := status(t, svc, spec.ID)
		if resp.DeliveryContract != model.DeliveryContractValid || resp.DeliveryState != delivery.StateDoneConcerns || resp.DeliveryDetail != "" {
			t.Errorf("resp = %q %q %q, want valid hecho_con_dudas", resp.DeliveryContract, resp.DeliveryState, resp.DeliveryDetail)
		}
	})

	t.Run("missing_in_implementing", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "a")
		writeTask(t, svc, spec.ID, model.TaskSectionPlan, "Plan.")
		resp := status(t, svc, spec.ID)
		if resp.DeliveryContract != model.DeliveryContractMissing || resp.DeliveryState != "" || resp.DeliveryDetail != "" {
			t.Errorf("resp = %q %q %q, want missing only", resp.DeliveryContract, resp.DeliveryState, resp.DeliveryDetail)
		}
	})

	t.Run("invalid_hand_edited", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "a")
		w := mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
		breakEntregaOnDisk(t, w.Path)
		resp := status(t, svc, spec.ID)
		if resp.DeliveryContract != model.DeliveryContractInvalid || resp.DeliveryState != "" || resp.DeliveryDetail == "" {
			t.Errorf("resp = %q %q %q, want invalid with detail", resp.DeliveryContract, resp.DeliveryState, resp.DeliveryDetail)
		}
	})

	t.Run("done_keeps_reporting", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "a")
		mustWriteEntrega(t, svc, spec.ID, entrega(contractBody))
		for range 2 {
			if _, err := advance(ctx, svc, spec.ID); err != nil {
				t.Fatalf("advance: %v", err)
			}
		}
		resp := status(t, svc, spec.ID)
		if resp.Spec.Status != model.SpecStatusDone || resp.DeliveryContract != model.DeliveryContractValid {
			t.Errorf("resp = %s %q, want done valid", resp.Spec.Status, resp.DeliveryContract)
		}
	})

	t.Run("no_tarea_md", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec := advanceToImplementing(t, svc, ctx, "a")
		resp := status(t, svc, spec.ID)
		if resp.DeliveryContract != "" || resp.DeliveryState != "" || resp.DeliveryDetail != "" {
			t.Errorf("resp = %q %q %q, want nothing", resp.DeliveryContract, resp.DeliveryState, resp.DeliveryDetail)
		}
	})

	t.Run("speccing_with_tarea_md", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "a", Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if _, err := advance(ctx, svc, spec.ID); err != nil {
			t.Fatalf("advance: %v", err)
		}
		writeTask(t, svc, spec.ID, model.TaskSectionWhat, "Que.")
		resp := status(t, svc, spec.ID)
		if resp.Spec.Status != model.SpecStatusSpeccing || resp.DeliveryContract != "" {
			t.Errorf("resp = %s %q, want speccing with nothing", resp.Spec.Status, resp.DeliveryContract)
		}
	})

	t.Run("nil_safe", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, deliveryProject)
		svc.AttachDeliveryState(nil)
		resp := &model.SpecStatusResponse{}
		svc.AttachDeliveryState(resp)
		if resp.DeliveryContract != "" {
			t.Errorf("resp = %+v, want untouched", resp)
		}
	})
}

// TestDelivery_NoTransitionGate covers C6 (D5): no transition checks the
// block. A spec with no entrega at all and one that reports bloqueado both
// walk implementing -> qa -> done.
func TestDelivery_NoTransitionGate(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newReviewTestService(t, deliveryProject)

	silent := advanceToImplementing(t, svc, ctx, "sin entrega")
	blocked := advanceToImplementing(t, svc, ctx, "bloqueada")
	mustWriteEntrega(t, svc, blocked.ID, entrega(blockedContractBody))

	for _, spec := range []*model.Spec{silent, blocked} {
		for _, want := range []model.SpecStatus{model.SpecStatusQA, model.SpecStatusDone} {
			got, err := advance(ctx, svc, spec.ID)
			if err != nil {
				t.Fatalf("%s: advance to %s: %v", spec.Title, want, err)
			}
			if got.Status != want {
				t.Fatalf("%s: status = %s, want %s", spec.Title, got.Status, want)
			}
		}
	}
}
