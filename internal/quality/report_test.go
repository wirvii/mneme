package quality

import (
	"strings"
	"testing"
)

func sampleReportInput(text string) ReportInput {
	return ReportInput{
		SpecID: "SPEC-117", CertificateID: "cert-1", HeadSHA: "abc123", BaseSHA: "def456",
		Verdict: "findings", ConstitutionHash: "hash-const", CriteriaHash: "hash-crit",
		MnemeVersion: "v-test", GeneratedAtUTC: "2026-01-01T00:00:00Z",
		Checks: []ReportCheck{
			{Seq: 1, Kind: "criteria", Name: "declared", Status: "pass"},
			{Seq: 4, Kind: "criterion", Name: "AC1", Mode: "assert", Text: text, Status: "pass"},
			{Seq: 5, Kind: "criterion-command", Name: "AC2", Mode: "command", Text: "el comando pasa", Status: "finding", Summary: "vacuity-unprovable"},
			{
				Seq: 6, Kind: "criterion-manual", Name: "AC3", Mode: "manual", Text: "revision visual",
				Status: "acked", AckedBy: "qa-tester", AckedAt: "2026-01-02T00:00:00Z", Justification: "captura adjunta",
			},
		},
	}
}

// TestRenderReport_ContainsGenerationMarker anchors D12's own contract:
// every rendered report carries the marker Report checks for before
// overwriting.
func TestRenderReport_ContainsGenerationMarker(t *testing.T) {
	out := RenderReport(sampleReportInput("texto A"))
	if !strings.Contains(out, ReportGenerationMarker) {
		t.Fatalf("RenderReport output does not contain the generation marker:\n%s", out)
	}
}

// TestRenderReport_Deterministic covers AC28: two invocations with the
// SAME input produce byte-identical output.
func TestRenderReport_Deterministic(t *testing.T) {
	in := sampleReportInput("texto A")
	a := RenderReport(in)
	b := RenderReport(in)
	if a != b {
		t.Fatal("RenderReport(in) produced different bytes across two invocations with the same input")
	}
}

// TestRenderReport_ContainsCriterionFieldsAndSignature covers AC28: id and
// text of each criterion, its status, and — for the signed manual — who
// signed, when, and with what evidence.
func TestRenderReport_ContainsCriterionFieldsAndSignature(t *testing.T) {
	out := RenderReport(sampleReportInput("texto A"))

	for _, want := range []string{"AC1", "texto A", "AC2", "vacuity-unprovable", "AC3", "revision visual", "qa-tester", "2026-01-02T00:00:00Z", "captura adjunta"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderReport output does not contain %q:\n%s", want, out)
		}
	}
}

// TestRenderReport_NoCriterionRows covers AC28's third row: a certificate
// with zero criterion rows produces an explicit statement, not a silently
// empty table.
func TestRenderReport_NoCriterionRows(t *testing.T) {
	in := ReportInput{
		SpecID: "SPEC-1", CertificateID: "cert-1", HeadSHA: "abc", Verdict: "pass",
		ConstitutionHash: "hash", GeneratedAtUTC: "2026-01-01T00:00:00Z",
		Checks: []ReportCheck{{Seq: 1, Kind: "gate", Name: "build", Status: "pass"}},
	}
	out := RenderReport(in)
	if !strings.Contains(out, "no tiene ninguna fila de criterio") {
		t.Errorf("RenderReport with zero criterion rows does not say so explicitly:\n%s", out)
	}
	if strings.Contains(out, "| id | modo | estado | texto | resumen |") {
		t.Error("RenderReport rendered an empty criteria table header despite zero criterion rows")
	}
}

// TestRenderReport_FromCertificate_NotFromFile is the hoja-level half of
// AC27: RenderReport ONLY ever reads what ReportInput hands it — there is
// no file path anywhere in its signature, so a caller cannot accidentally
// feed it criteria.toml. The service-level round trip (verify with "texto
// A", rewrite criteria.toml to "texto B", report still says "texto A") is
// covered in internal/service; this test pins the STRUCTURAL guarantee
// that makes that property possible in the first place.
func TestRenderReport_FromCertificate_NotFromFile(t *testing.T) {
	out := RenderReport(sampleReportInput("texto A"))
	if strings.Contains(out, "texto B") {
		t.Error("RenderReport output unexpectedly contains texto B — it must only ever reflect its ReportInput argument")
	}
}

// TestRenderReport_EvidenceLine_AppearsBeforeTables covers SPEC-137 D6/AC13
// at the leaf level: a non-empty Evidence sentence is rendered verbatim,
// and it appears strictly before the "## Criterios" heading — the "primera
// linea del cuerpo, antes de las tablas" the design requires.
func TestRenderReport_EvidenceLine_AppearsBeforeTables(t *testing.T) {
	in := sampleReportInput("texto A")
	in.Evidence = "Evidencia: 2/2 puertas del proyecto en verde · 1/1 criterios cumplidos · cobertura no declarada · cliquet no declarado · presupuesto no declarado · mutacion no declarada · verificacion visual no declarada"

	out := RenderReport(in)
	if !strings.Contains(out, in.Evidence) {
		t.Fatalf("RenderReport output does not contain the evidence sentence literally:\n%s", out)
	}
	evidenceIdx := strings.Index(out, in.Evidence)
	tablesIdx := strings.Index(out, "## Criterios")
	if evidenceIdx < 0 || tablesIdx < 0 || evidenceIdx > tablesIdx {
		t.Errorf("evidence line (idx %d) must appear before the tables (idx %d)", evidenceIdx, tablesIdx)
	}
}

// TestRenderReport_EmptyEvidence_ShowsMissingText covers AC16: a
// certificate emitted before this field existed (Evidence == "") is
// rendered with EvidenceMissingText, NEVER a fabricated sentence derived
// from the certificate's other fields.
func TestRenderReport_EmptyEvidence_ShowsMissingText(t *testing.T) {
	in := sampleReportInput("texto A")
	in.Evidence = ""

	out := RenderReport(in)
	if !strings.Contains(out, EvidenceMissingText) {
		t.Fatalf("RenderReport output does not contain %q for empty Evidence:\n%s", EvidenceMissingText, out)
	}
	if strings.Contains(out, "puertas del proyecto en verde") {
		t.Error("RenderReport fabricated an evidence-shaped sentence for an empty Evidence field")
	}
}
