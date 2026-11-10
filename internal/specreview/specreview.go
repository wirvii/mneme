package specreview

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

// SchemaVersion is the only schema_version Parse accepts. The report lives in
// the local workflow directory and never travels between machines, so there is
// nothing for a version bump to widen.
const SchemaVersion = 1

// ErrInvalid is returned when a report is malformed: a missing or unknown key,
// a value outside its vocabulary, or a violated cross-field rule.
var ErrInvalid = errors.New("specreview: invalid review report")

// ErrUnsupportedSchema is returned when a report declares a schema_version
// this reader does not recognise.
var ErrUnsupportedSchema = errors.New("specreview: review report schema_version unsupported")

// Moment is the point of the flow a report belongs to.
type Moment string

const (
	// MomentSpec is the review of the spec, before the human approval gate.
	MomentSpec Moment = "spec"
	// MomentPlan is the review of the plan, before implementation starts.
	MomentPlan Moment = "plan"
)

// Reviewer roles (the "papel" key of a [[revisor]] entry).
const (
	// RoleComprension paraphrases the document and checks it is understandable.
	RoleComprension = "comprension"
	// RoleDiseno critiques the design and its simplicity.
	RoleDiseno = "diseno"
	// RoleHechos checks every claim about today's code against the code.
	RoleHechos = "hechos"
	// RolePreparacion checks the plan leaves no decision to the implementer.
	RolePreparacion = "preparacion"
)

// Reviewer states (the "estado" key of a [[revisor]] entry). The state
// describes the original round only.
const (
	// StatusCompleto means the reviewer ran and delivered.
	StatusCompleto = "completo"
	// StatusNoCorrio means the reviewer could not run; it never counts as zero findings.
	StatusNoCorrio = "no_corrio"
)

// Finding severities.
const (
	// SeverityBlocker stops the flow until resolved.
	SeverityBlocker = "blocker"
	// SeverityMajor is a significant issue.
	SeverityMajor = "major"
	// SeverityNit is a minor remark.
	SeverityNit = "nit"
)

// Finding outcomes (the "salida" key). The first four are the resolution
// paths; OutcomeRiesgoAceptado is the state of what stays pending after the
// single re-review.
const (
	// OutcomeCorregir means the document was fixed.
	OutcomeCorregir = "corregir"
	// OutcomeRefutar means the finding was refuted with evidence.
	OutcomeRefutar = "refutar"
	// OutcomeEscalar means the decision goes to the person.
	OutcomeEscalar = "escalar"
	// OutcomeDescartar means a nit was dropped.
	OutcomeDescartar = "descartar"
	// OutcomeRiesgoAceptado means the finding stays open as a visible, accepted risk.
	OutcomeRiesgoAceptado = "riesgo_aceptado"
)

// idPattern is the shape every finding id must have. It is a local copy of the
// criteria id pattern in internal/acceptance, which is private there.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// Reviewer is one parsed [[revisor]] entry.
type Reviewer struct {
	Role               string
	Status             string
	Note               string
	ParaphraseProposal string
	ParaphraseToday    string
}

// Finding is one parsed [[hallazgo]] entry.
type Finding struct {
	ID         string
	Reviewer   string
	Severity   string
	Text       string
	Outcome    string
	Evidence   string
	Resolution string
}

// Report is the parsed form of a revision-spec.toml or revision-plan.toml.
type Report struct {
	SchemaVersion int
	ReReview      bool
	Reviewers     []Reviewer
	Findings      []Finding
}

// Counts summarises a Report. It is computed on read and never stored.
type Counts struct {
	Requested    int
	Completed    int
	DidNotRun    int
	Blocker      int
	Major        int
	Nit          int
	Fix          int
	Refute       int
	Escalate     int
	Discard      int
	AcceptedRisk int
	ReReview     bool
}

// schemaProbe is the first-pass decode target: only the version, decoded
// without strictness so that a future schema with unknown keys still reports
// ErrUnsupportedSchema instead of a misleading unknown-key error.
type schemaProbe struct {
	SchemaVersion *int `toml:"schema_version"`
}

type rawReviewer struct {
	Role               string `toml:"papel"`
	Status             string `toml:"estado"`
	Note               string `toml:"nota"`
	ParaphraseProposal string `toml:"parafrasis_propuesta"`
	ParaphraseToday    string `toml:"parafrasis_hoy"`
}

type rawFinding struct {
	ID         string `toml:"id"`
	Reviewer   string `toml:"revisor"`
	Severity   string `toml:"severidad"`
	Text       string `toml:"texto"`
	Outcome    string `toml:"salida"`
	Evidence   string `toml:"evidencia"`
	Resolution string `toml:"resolucion"`
}

type rawReport struct {
	SchemaVersion *int          `toml:"schema_version"`
	ReReview      bool          `toml:"re_revision"`
	Reviewers     []rawReviewer `toml:"revisor"`
	Findings      []rawFinding  `toml:"hallazgo"`
}

// rolesByMoment lists the roles each moment must contain exactly once.
var rolesByMoment = map[Moment][]string{
	MomentSpec: {RoleComprension, RoleDiseno, RoleHechos},
	MomentPlan: {RolePreparacion, RoleHechos},
}

// Parse decodes and validates a review report for the given moment. The
// schema version is checked first and on its own, then the whole document is
// decoded strictly, then the rules of SPEC-181 D2 are applied in order. Every
// error wraps ErrInvalid or ErrUnsupportedSchema and names the failing field.
func Parse(data []byte, m Moment) (*Report, error) {
	var probe schemaProbe
	if err := toml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("specreview: parse report: %s: %w", err, ErrInvalid)
	}
	if probe.SchemaVersion == nil {
		return nil, fmt.Errorf("specreview: missing required key %q: %w", "schema_version", ErrInvalid)
	}
	if *probe.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("specreview: schema_version %d not supported (only %d): %w",
			*probe.SchemaVersion, SchemaVersion, ErrUnsupportedSchema)
	}

	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw rawReport
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("specreview: parse report: %s: %w", err, ErrInvalid)
	}

	roles, ok := rolesByMoment[m]
	if !ok {
		return nil, fmt.Errorf("specreview: unknown moment %q: %w", m, ErrInvalid)
	}

	reviewers, completed, err := parseReviewers(raw.Reviewers, roles)
	if err != nil {
		return nil, err
	}
	findings, err := parseFindings(raw.Findings, completed, raw.ReReview)
	if err != nil {
		return nil, err
	}
	return &Report{
		SchemaVersion: *raw.SchemaVersion,
		ReReview:      raw.ReReview,
		Reviewers:     reviewers,
		Findings:      findings,
	}, nil
}

// parseReviewers applies rules 2 to 4: exactly the moment's roles once each,
// the state vocabulary with its note rule, and the paraphrase rule.
func parseReviewers(raws []rawReviewer, roles []string) ([]Reviewer, map[string]bool, error) {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	seen := make(map[string]bool, len(raws))
	completed := make(map[string]bool, len(raws))
	out := make([]Reviewer, 0, len(raws))
	for i, rr := range raws {
		if !allowed[rr.Role] {
			return nil, nil, fmt.Errorf("specreview: revisor[%d]: papel %q is not valid for this moment (want %v): %w", i, rr.Role, roles, ErrInvalid)
		}
		if seen[rr.Role] {
			return nil, nil, fmt.Errorf("specreview: revisor[%d]: duplicate papel %q: %w", i, rr.Role, ErrInvalid)
		}
		seen[rr.Role] = true

		switch rr.Status {
		case StatusCompleto:
			completed[rr.Role] = true
		case StatusNoCorrio:
			if rr.Note == "" {
				return nil, nil, fmt.Errorf("specreview: revisor[%d] %q: estado no_corrio requires nota: %w", i, rr.Role, ErrInvalid)
			}
		default:
			return nil, nil, fmt.Errorf("specreview: revisor[%d] %q: unknown estado %q: %w", i, rr.Role, rr.Status, ErrInvalid)
		}

		if err := checkParaphrases(i, rr); err != nil {
			return nil, nil, err
		}
		out = append(out, Reviewer(rr))
	}
	for _, r := range roles {
		if !seen[r] {
			return nil, nil, fmt.Errorf("specreview: missing revisor with papel %q: %w", r, ErrInvalid)
		}
	}
	return out, completed, nil
}

// checkParaphrases enforces rule 4: a completed comprension reviewer carries
// both paraphrases, every other reviewer carries none.
func checkParaphrases(i int, rr rawReviewer) error {
	hasAny := rr.ParaphraseProposal != "" || rr.ParaphraseToday != ""
	if rr.Role != RoleComprension {
		if hasAny {
			return fmt.Errorf("specreview: revisor[%d] %q: paraphrases only belong to comprension: %w", i, rr.Role, ErrInvalid)
		}
		return nil
	}
	if rr.Status != StatusCompleto {
		return nil
	}
	if rr.ParaphraseProposal == "" {
		return fmt.Errorf("specreview: revisor[%d] comprension: missing %q: %w", i, "parafrasis_propuesta", ErrInvalid)
	}
	if rr.ParaphraseToday == "" {
		return fmt.Errorf("specreview: revisor[%d] comprension: missing %q: %w", i, "parafrasis_hoy", ErrInvalid)
	}
	return nil
}

// parseFindings applies rules 5 to 7 to every [[hallazgo]] entry.
func parseFindings(raws []rawFinding, completed map[string]bool, reReview bool) ([]Finding, error) {
	seen := make(map[string]bool, len(raws))
	out := make([]Finding, 0, len(raws))
	for i, rf := range raws {
		if !idPattern.MatchString(rf.ID) {
			return nil, fmt.Errorf("specreview: hallazgo[%d]: id %q must match %s: %w", i, rf.ID, idPattern.String(), ErrInvalid)
		}
		if seen[rf.ID] {
			return nil, fmt.Errorf("specreview: hallazgo[%d]: duplicate id %q: %w", i, rf.ID, ErrInvalid)
		}
		seen[rf.ID] = true

		if !completed[rf.Reviewer] {
			return nil, fmt.Errorf("specreview: hallazgo %q: revisor %q is not declared or did not complete: %w", rf.ID, rf.Reviewer, ErrInvalid)
		}
		switch rf.Severity {
		case SeverityBlocker, SeverityMajor, SeverityNit:
		default:
			return nil, fmt.Errorf("specreview: hallazgo %q: unknown severidad %q: %w", rf.ID, rf.Severity, ErrInvalid)
		}
		if rf.Text == "" {
			return nil, fmt.Errorf("specreview: hallazgo %q: missing required key %q: %w", rf.ID, "texto", ErrInvalid)
		}
		if err := checkOutcome(rf, reReview); err != nil {
			return nil, err
		}
		out = append(out, Finding(rf))
	}
	return out, nil
}

// checkOutcome applies rule 6: the vocabulary of salida and the extra field
// or severity each outcome demands.
func checkOutcome(rf rawFinding, reReview bool) error {
	switch rf.Outcome {
	case OutcomeCorregir:
		if rf.Resolution == "" {
			return fmt.Errorf("specreview: hallazgo %q: salida corregir requires resolucion: %w", rf.ID, ErrInvalid)
		}
	case OutcomeRefutar:
		if rf.Evidence == "" {
			return fmt.Errorf("specreview: hallazgo %q: salida refutar requires evidencia: %w", rf.ID, ErrInvalid)
		}
	case OutcomeEscalar:
	case OutcomeDescartar:
		if rf.Severity != SeverityNit {
			return fmt.Errorf("specreview: hallazgo %q: salida descartar only applies to nit, got %q: %w", rf.ID, rf.Severity, ErrInvalid)
		}
	case OutcomeRiesgoAceptado:
		if !reReview {
			return fmt.Errorf("specreview: hallazgo %q: salida riesgo_aceptado requires re_revision = true: %w", rf.ID, ErrInvalid)
		}
	default:
		return fmt.Errorf("specreview: hallazgo %q: unknown salida %q: %w", rf.ID, rf.Outcome, ErrInvalid)
	}
	return nil
}

// Counts summarises the report. Requested counts every declared reviewer;
// findings are tallied by severity and by outcome.
func (r *Report) Counts() Counts {
	c := Counts{Requested: len(r.Reviewers), ReReview: r.ReReview}
	for _, rv := range r.Reviewers {
		if rv.Status == StatusCompleto {
			c.Completed++
		} else {
			c.DidNotRun++
		}
	}
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityBlocker:
			c.Blocker++
		case SeverityMajor:
			c.Major++
		case SeverityNit:
			c.Nit++
		}
		switch f.Outcome {
		case OutcomeCorregir:
			c.Fix++
		case OutcomeRefutar:
			c.Refute++
		case OutcomeEscalar:
			c.Escalate++
		case OutcomeDescartar:
			c.Discard++
		case OutcomeRiesgoAceptado:
			c.AcceptedRisk++
		}
	}
	return c
}
