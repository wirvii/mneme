package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

func TestWorkCriteriaCompatibleWithQualityParser(t *testing.T) {
	fragments := []model.WorkCriterion{
		{Key: "assert-file", Declaration: "[[criterion]]\nid = \"assert-file\"\nmode = \"assert\"\ntext = \"file\"\n[[criterion.assert]]\nverb = \"file_exists\"\npath = \"go.mod\"\nnew = true"},
		{Key: "assert-count", Declaration: "[[criterion]]\nid = \"assert-count\"\nmode = \"assert\"\ntext = \"count\"\n[[criterion.assert]]\nverb = \"pattern_count\"\ncontains = \"module\"\nin = [\"go.mod\"]\nword = false\ncomparator = \">=\"\ncount = 1\nnew = true"},
		{Key: "assert-defined", Declaration: "[[criterion]]\nid = \"assert-defined\"\nmode = \"assert\"\ntext = \"defined\"\n[[criterion.assert]]\nverb = \"symbol_defined\"\nsymbol = \"main\"\nin = [\"*.go\"]\nnew = true"},
		{Key: "assert-referenced", Declaration: "[[criterion]]\nid = \"assert-referenced\"\nmode = \"assert\"\ntext = \"referenced\"\n[[criterion.assert]]\nverb = \"symbol_referenced\"\nsymbol = \"main\"\ndefined_in = [\"*.go\"]\nignore = []\nnew = true"},
		{Key: "command", Declaration: "[[criterion]]\nid = \"command\"\nmode = \"command\"\ntext = \"command\"\ncommand = [\"go\", \"test\", \"./...\"]\ntimeout = \"1m\""},
		{Key: "manual", Declaration: "[[criterion]]\nid = \"manual\"\nmode = \"manual\"\ntext = \"manual\"\nevidence_required = \"review note\""},
	}
	var source strings.Builder
	source.WriteString("schema_version = 1\n")
	for _, fragment := range fragments {
		source.WriteString("\n")
		source.WriteString(fragment.Declaration)
		source.WriteString("\n")
	}
	parsed, err := quality.ParseCriteria([]byte(source.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Criteria) != len(fragments) {
		t.Fatalf("criteria=%d", len(parsed.Criteria))
	}
	for i, criterion := range parsed.Criteria {
		if criterion.ID != fragments[i].Key {
			t.Errorf("criterion %d id=%q want=%q", i, criterion.ID, fragments[i].Key)
		}
	}

	t.Run("key-boundaries", func(t *testing.T) {
		tests := []struct {
			key   string
			valid bool
		}{
			{"", false}, {"-A", false}, {"A", true}, {"A_b.c-1", true},
			{strings.Repeat("a", 32), true}, {strings.Repeat("a", 33), false},
			{"A B", false}, {"Á", false},
		}
		for _, tt := range tests {
			fragment := fmt.Sprintf("schema_version = 1\n[[criterion]]\nid = %q\ntext = \"x\"\nmode = \"manual\"\nevidence_required = \"proof\"\n", tt.key)
			_, parseErr := quality.ParseCriteria([]byte(fragment))
			if model.ValidWorkKey(tt.key) != tt.valid {
				t.Errorf("model key %q mismatch", tt.key)
			}
			if (parseErr == nil) != tt.valid {
				t.Errorf("quality key %q valid=%v err=%v", tt.key, tt.valid, parseErr)
			}
		}
	})

	qualityEffects := map[string]bool{
		string(quality.EffectBlocks): true, string(quality.EffectMeasures): true,
		string(quality.EffectAbsent): true, string(quality.EffectStopped): true,
		string(quality.EffectSignable): true,
	}
	for _, effect := range []model.DeliveryCheckEffect{
		model.DeliveryEffectBlocks, model.DeliveryEffectMeasures,
		model.DeliveryEffectAbsent, model.DeliveryEffectStopped,
	} {
		if !qualityEffects[string(effect)] {
			t.Errorf("delivery effect %q missing from quality", effect)
		}
	}
	if model.DeliveryCheckEffect(quality.EffectSignable).Valid() {
		t.Fatal("signable must not be a delivery effect")
	}
}
