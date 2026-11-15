package enforcement

import (
	"reflect"
	"testing"
)

func TestTestFile(t *testing.T) {
	tests := []struct {
		rel  string
		lang TestLanguage
		ok   bool
	}{
		{"internal/store/x_test.go", TestGo, true},
		{"a.test.ts", TestJS, true},
		{"b.spec.jsx", TestJS, true},
		{"__tests__/a.js", TestJS, true},
		{"web/__tests__/a.js", TestJS, true},
		{"c.test.mjs", TestJS, true},
		{"d.spec.cjs", TestJS, true},
		{"web/__tests__/deep/e.tsx", TestJS, true},
		{"test_a.py", TestPython, true},
		{"pkg/a_test.py", TestPython, true},
		{"x.go", "", false},
		{"testdata/a.go", "", false},
		{"contest.go", "", false},
		{"fit.go", "", false},
		{"test.py", "", false},
		{"latest_test.txt", "", false},
		{"__tests__", "", false},
		{"my__tests__/a.js", "", false},
		{"__tests__/a.go", "", false},
		{"a.test.go", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		lang, ok := TestFile(tt.rel)
		if ok != tt.ok || lang != tt.lang {
			t.Errorf("TestFile(%q) = (%q, %v), want (%q, %v)", tt.rel, lang, ok, tt.lang, tt.ok)
		}
	}
}

func TestSkipMarkersAdded(t *testing.T) {
	tests := []struct {
		name          string
		lang          TestLanguage
		before, after string
		want          []string
	}{
		{"go_skip", TestGo, "func TestA(t *testing.T) {}", "func TestA(t *testing.T) { t.Skip(\"x\") }", []string{"t.Skip("}},
		{"go_skipf", TestGo, "", "t.Skipf(\"%d\", 1)", []string{"t.Skipf("}},
		{"go_skipnow", TestGo, "", "t.SkipNow()", []string{"t.SkipNow("}},
		{"go_bench_skip", TestGo, "", "b.Skip(\"x\")", []string{"b.Skip("}},
		{"go_moved", TestGo, "a\nt.Skip(\"x\")\nb", "b\nt.Skip(\"x\")\na", nil},
		{"go_removed", TestGo, "t.Skip(\"x\")", "", nil},
		{"js_skip", TestJS, "", "it.skip('a', () => {})", []string{".skip("}},
		{"js_only", TestJS, "", "describe.only('a')", []string{".only("}},
		{"js_xit", TestJS, "", "xit('a')", []string{"xit("}},
		{"js_fit", TestJS, "", "  fit('a')", []string{"fit("}},
		{"js_xdescribe_space", TestJS, "", "xdescribe ('a')", []string{"xdescribe ("}},
		{"js_exit", TestJS, "", "  exit(1)", nil},
		{"js_benefit", TestJS, "", "benefit(2)", nil},
		{"js_method_xit", TestJS, "", "obj.xit(3)", nil},
		{"py_skipif_once", TestPython, "", "@pytest.mark.skipif(x)\ndef test_a(): pass", []string{"@pytest.mark.skipif"}},
		{"py_skip", TestPython, "", "@pytest.mark.skip", []string{"@pytest.mark.skip"}},
		{"py_unittest", TestPython, "", "@unittest.skip('x')", []string{"@unittest.skip"}},
		{"py_call", TestPython, "", "pytest.skip('x')", []string{"pytest.skip("}},
		{"py_self", TestPython, "", "self.skipTest('x')", []string{"self.skipTest("}},
		{"unknown_language", TestLanguage("x"), "", "t.Skip(", nil},
	}
	for _, tt := range tests {
		got := SkipMarkersAdded(tt.lang, tt.before, tt.after)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: SkipMarkersAdded = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestHasTestFunction(t *testing.T) {
	tests := []struct {
		lang TestLanguage
		text string
		want bool
	}{
		{TestGo, "func TestA(t *testing.T) {}", true},
		{TestGo, "func BenchmarkX(b *testing.B) {}", true},
		{TestGo, "func ExampleY() {}", true},
		{TestGo, "func FuzzZ(f *testing.F) {}", true},
		{TestGo, "func helper() {}", false},
		{TestGo, "package store", false},
		{TestJS, "it('a', () => {})", true},
		{TestJS, "test ('a')", true},
		{TestJS, "describe('a')", true},
		{TestJS, "submit('a')", false},
		{TestPython, "class T:\n    def test_a(self): pass", true},
		{TestPython, "def helper(): pass", false},
		{TestLanguage("x"), "func TestA(", false},
	}
	for _, tt := range tests {
		if got := HasTestFunction(tt.lang, tt.text); got != tt.want {
			t.Errorf("HasTestFunction(%q, %q) = %v, want %v", tt.lang, tt.text, got, tt.want)
		}
	}
}

func TestApplyEdit(t *testing.T) {
	tests := []struct {
		name             string
		before, old, new string
		all              bool
		wantAfter        string
		wantApplied      bool
	}{
		{"single", "a a", "a", "b", false, "b a", true},
		{"all", "a a", "a", "b", true, "b b", true},
		{"absent", "a a", "z", "b", false, "a a", false},
		{"empty_old_on_new_file", "", "", "x", false, "x", true},
		{"empty_old_on_existing_file", "a", "", "x", false, "a", false},
	}
	for _, tt := range tests {
		after, applied := ApplyEdit(tt.before, tt.old, tt.new, tt.all)
		if after != tt.wantAfter || applied != tt.wantApplied {
			t.Errorf("%s: ApplyEdit = (%q, %v), want (%q, %v)", tt.name, after, applied, tt.wantAfter, tt.wantApplied)
		}
	}

	// MultiEdit applies its edits in sequence, each over the previous result.
	doc := "func TestA(t *testing.T) {}"
	doc, _ = ApplyEdit(doc, "{}", "{ x() }", false)
	doc, ok := ApplyEdit(doc, "x()", "t.Skip(\"y\")", false)
	if !ok || doc != "func TestA(t *testing.T) { t.Skip(\"y\") }" {
		t.Errorf("chained edits = (%q, %v)", doc, ok)
	}
}
