package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/install"
)

// plainLanguageAnchors son las tres frases literales que la regla de SPEC-121
// obliga a repetir en las TRES superficies que la enseñan: los dos manuales
// operativos y el skill mneme-init. Son cadenas literales, no una constante de
// producción, por el mismo motivo que TestCodegraphVocabularyCoherence
// (internal/cli/codegraph_coherence_test.go): ningún código de producción las
// consume, y una constante que solo existe para que la lea un test es
// ceremonia.
var plainLanguageAnchors = []string{
	"Plain language",
	"Channels that reach a person",
	"The exemption never travels with the text",
}

// missingAnchors devuelve los anclajes que NO aparecen en content, en el mismo
// orden en que se declaran. Devuelve el resultado en vez de llamar a t.Errorf
// justamente para que TestPlainLanguageAnchorsAreDiscriminating pueda comprobar
// que discrimina: un guardián que no puede fallar no es un guardián.
func missingAnchors(content string, anchors []string) []string {
	var missing []string
	for _, anchor := range anchors {
		if !strings.Contains(content, anchor) {
			missing = append(missing, anchor)
		}
	}
	return missing
}

// mnemeInitSkillContent localiza mneme-init/SKILL.md entre las entradas
// embebidas, igual que TestMnemeInitSkill_DocumentsEveryLayer23ForbiddenToken.
func mnemeInitSkillContent(t *testing.T) string {
	t.Helper()

	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}
	for _, e := range entries {
		if filepath.ToSlash(e.RelPath) == "mneme-init/SKILL.md" {
			return string(e.Content)
		}
	}
	t.Fatal("mneme-init/SKILL.md not found among bundled entries")
	return ""
}

// plainLanguageSurfaces devuelve las tres superficies que enseñan la regla,
// indexadas por el nombre con el que se las nombra en un fallo.
func plainLanguageSurfaces(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{
		"operating-manual.md":       install.OperatingManual(),
		"operating-manual-codex.md": install.OperatingManualCodex(),
		"mneme-init/SKILL.md":       mnemeInitSkillContent(t),
	}
}

// TestPlainLanguageAcrossSurfaces es G3 de SPEC-121: las tres superficies que
// enseñan la regla de lenguaje llano deben contener los tres anclajes
// compartidos. Si una de ellas los pierde, esa superficie ha dejado de enseñar
// la regla mientras las otras dos siguen prometiéndola.
func TestPlainLanguageAcrossSurfaces(t *testing.T) {
	for name, content := range plainLanguageSurfaces(t) {
		if content == "" {
			t.Fatalf("%s: surface is empty", name)
		}
		if missing := missingAnchors(content, plainLanguageAnchors); len(missing) > 0 {
			t.Errorf("%s: missing plain-language anchors %q", name, missing)
		}
	}
}

// TestPlainLanguageAnchorsAreDiscriminating es G4 de SPEC-121: el hermano que
// falla. Mismo montaje, mismas superficies y la misma missingAnchors — pero
// sobre copias EN MEMORIA a las que se les ha quitado un anclaje. Si
// missingAnchors dejara de discriminar (por ejemplo devolviendo siempre nil),
// TestPlainLanguageAcrossSurfaces seguiría verde y este test se pondría rojo.
// Ninguna copia toca el disco: los assets quedan intactos.
func TestPlainLanguageAnchorsAreDiscriminating(t *testing.T) {
	for name, content := range plainLanguageSurfaces(t) {
		for _, anchor := range plainLanguageAnchors {
			stripped := strings.ReplaceAll(content, anchor, "")

			got := missingAnchors(stripped, plainLanguageAnchors)
			if len(got) != 1 || got[0] != anchor {
				t.Errorf("%s: removing anchor %q should leave exactly that anchor missing, got %q", name, anchor, got)
			}
		}
	}
}
