package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/delivery"
	"github.com/wirvii/mneme/internal/model"
)

// validateDeliveryWrite is SPEC-182's write-time check of the entrega
// section, run by SpecDocWrite before any I/O so a refused write leaves
// tarea.md byte-identical. It returns ("", "", nil) for every other kind and
// section. A malformed block is always refused; a missing one only for a
// standard spec in implementing, the one moment the block is the
// implementer's report. Otherwise a missing block is written and reported as
// DeliveryContractMissing. mneme checks the shape, never the truth.
func validateDeliveryWrite(spec *model.Spec, req model.SpecDocWriteRequest) (model.DeliveryContractState, string, error) {
	if req.Kind != model.SpecDocKindTask || req.Section != model.TaskSectionDelivery {
		return "", "", nil
	}
	c, err := delivery.Parse([]byte(req.Content))
	switch {
	case err == nil:
		return model.DeliveryContractValid, c.State, nil
	case errors.Is(err, delivery.ErrMissing):
		if spec.Lane == model.LaneStandard && spec.Status == model.SpecStatusImplementing {
			return "", "", fmt.Errorf("service: spec doc write: the entrega section of a standard spec in implementing must carry the delivery contract block between the lines %s and %s (skill mneme-flow, references/entrega.md): %w",
				delivery.StartMarker, delivery.EndMarker, model.ErrDeliveryContractMissing)
		}
		return model.DeliveryContractMissing, "", nil
	default:
		return "", "", fmt.Errorf("service: spec doc write: %w: %w", model.ErrInvalidDeliveryContract, err)
	}
}

// readTaskSection returns the body of section s of the tarea.md at path: the
// lines between its markers, without the heading renderTaskRegion puts first.
// A tarea.md whose markers cannot be located is ErrTaskDocMalformed.
func readTaskSection(path string, s model.TaskSection) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("service: read tarea.md section %s: %w", s, err)
	}
	doc := string(raw)
	spans, err := parseTaskDoc(doc)
	if err != nil {
		return "", err
	}
	span := spans[s]
	body := strings.Split(doc, "\n")[span.start+1 : span.end]
	if heading, ok := s.Heading(); ok && len(body) > 0 && strings.TrimSpace(body[0]) == heading {
		body = body[1:]
	}
	return strings.Join(body, "\n"), nil
}

// deliveryCheckFor is the pure check behind DeliveryCheck and
// AttachDeliveryState, over a spec and history already loaded: it never
// queries the store. It is a method only because it needs the workflow
// directory from svc.config (amendment E3). Every failure to read becomes a
// state with Detail, never an error.
func (svc *SDDService) deliveryCheckFor(spec *model.Spec, history []*model.SpecHistory) *model.DeliveryCheck {
	c := &model.DeliveryCheck{
		SpecID:   spec.ID,
		Status:   spec.Status,
		Lane:     spec.Lane,
		Contract: model.DeliveryContractMissing,
	}
	if svc.config == nil || svc.config.WorkflowDir() == "" {
		c.Detail = noWorkflowDirMessage
		return c
	}
	path, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, model.SpecDocKindTask)
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	info, err := os.Stat(path)
	if err != nil {
		c.Detail = fmt.Sprintf("tarea.md not readable: %v", err)
		return c
	}

	c.Path = path
	if entered, ok := latestEntry(history, model.SpecStatusImplementing); ok {
		c.Current = !info.ModTime().Truncate(time.Second).Before(entered.Truncate(time.Second))
	}

	section, err := readTaskSection(path, model.TaskSectionDelivery)
	if err != nil {
		c.Contract = model.DeliveryContractInvalid
		c.Detail = err.Error()
		return c
	}
	contract, err := delivery.Parse([]byte(section))
	switch {
	case err == nil:
		c.Contract = model.DeliveryContractValid
		c.State = contract.State
	case errors.Is(err, delivery.ErrMissing):
		c.Detail = err.Error()
	default:
		c.Contract = model.DeliveryContractInvalid
		c.Detail = err.Error()
	}
	return c
}

// DeliveryCheck reports whether spec id's entrega section holds a valid
// delivery contract block written at or after its latest entry to
// implementing (SPEC-182). It is the function the stop hooks of SPEC-183
// consume, through DeliveryCheck.Satisfied. Only loading the spec or its
// history can fail; everything about the file is reported in the result.
func (svc *SDDService) DeliveryCheck(ctx context.Context, id string) (*model.DeliveryCheck, error) {
	spec, err := svc.store.GetSpec(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: delivery check: get: %w", err)
	}
	history, err := svc.store.GetSpecHistory(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: delivery check: history: %w", err)
	}
	return svc.deliveryCheckFor(spec, history), nil
}

// AttachDeliveryState fills resp's delivery fields (SPEC-182 D4) the way
// AttachReviewRange fills its range: best-effort, nil-safe and additive. Unlike
// it, it takes no context and never queries the store: resp already carries
// the spec and its history. The fields are set only when tarea.md exists and
// the spec is implementing, qa, audit or done; before implementing there can
// be no delivery, and reporting one as missing would be noise. Callers:
// handleSpecStatus (MCP) and `mneme spec status` (CLI).
func (svc *SDDService) AttachDeliveryState(resp *model.SpecStatusResponse) {
	if resp == nil || resp.Spec == nil {
		return
	}
	switch resp.Spec.Status {
	case model.SpecStatusImplementing, model.SpecStatusQA, model.SpecStatusAudit, model.SpecStatusDone:
	default:
		return
	}
	c := svc.deliveryCheckFor(resp.Spec, resp.History)
	if c.Path == "" {
		return
	}
	resp.DeliveryContract = c.Contract
	switch c.Contract {
	case model.DeliveryContractValid:
		resp.DeliveryState = c.State
	case model.DeliveryContractInvalid:
		resp.DeliveryDetail = c.Detail
	}
}
