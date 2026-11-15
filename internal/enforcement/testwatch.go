package enforcement

import (
	"path"
	"regexp"
	"strings"
)

// TestLanguage names the test conventions a file follows, which decide what
// counts as a skip marker and as a test function in it (SPEC-185 D3).
type TestLanguage string

// The three languages the test watch recognises. Any other file is judged by
// its path only.
const (
	TestGo     TestLanguage = "go"
	TestJS     TestLanguage = "js"
	TestPython TestLanguage = "python"
)

// jsTestDir is the directory name Jest treats as holding tests.
const jsTestDir = "__tests__"

// jsExtensions are the JavaScript and TypeScript file extensions a JS test
// file may carry.
var jsExtensions = map[string]bool{
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true,
}

// skipMarkers are, per language, the patterns that switch a test off or focus
// a run on one test (which silently skips the rest). Each pattern's first
// group is the marker as it is reported. Bare identifiers are anchored so
// that xit( does not match in exit( nor fit( in benefit(.
var skipMarkers = map[TestLanguage][]*regexp.Regexp{
	TestGo: {
		regexp.MustCompile(`\b(t\.Skip(?:f|Now)?\()`),
		regexp.MustCompile(`\b(b\.Skipf?\()`),
	},
	TestJS: {
		regexp.MustCompile(`(\.skip\()`),
		regexp.MustCompile(`(\.only\()`),
		regexp.MustCompile(`(?m)(?:^|[^A-Za-z0-9_.$])((?:xit|xtest|xdescribe|fit|fdescribe)\s*\()`),
	},
	TestPython: {
		regexp.MustCompile(`(@pytest\.mark\.skip(?:if)?)\b`),
		regexp.MustCompile(`(@unittest\.skip)`),
		regexp.MustCompile(`\b(pytest\.skip\()`),
		regexp.MustCompile(`\b(self\.skipTest\()`),
	},
}

// testFunctions are, per language, the pattern of a test function.
var testFunctions = map[TestLanguage]*regexp.Regexp{
	TestGo:     regexp.MustCompile(`(?m)^func (Test|Benchmark|Example|Fuzz)\w*\(`),
	TestJS:     regexp.MustCompile(`(?m)(^|[^A-Za-z0-9_.$])(it|test|describe)\s*\(`),
	TestPython: regexp.MustCompile(`(?m)^\s*def test_`),
}

// TestFile reports whether rel, a slash-separated path the caller already
// normalised, is a test file and in which language: Go `*_test.go`; JS/TS
// `*.test.<ext>`, `*.spec.<ext>` or any file under a `__tests__` directory;
// Python `test_*.py` or `*_test.py`. It looks at names only, so the decision is
// the same for a file that does not exist yet.
func TestFile(rel string) (TestLanguage, bool) {
	base := path.Base(rel)
	if rel == "" || base == "." || base == "/" {
		return "", false
	}
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch {
	case ext == ".go" && strings.HasSuffix(stem, "_test"):
		return TestGo, true
	case jsExtensions[ext]:
		if strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") || inJSTestDir(rel) {
			return TestJS, true
		}
	case ext == ".py" && (strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test")):
		return TestPython, true
	}
	return "", false
}

// inJSTestDir reports whether a directory segment of rel, not the file itself,
// is exactly __tests__.
func inJSTestDir(rel string) bool {
	segs := strings.Split(rel, "/")
	for _, seg := range segs[:len(segs)-1] {
		if seg == jsTestDir {
			return true
		}
	}
	return false
}

// SkipMarkersAdded lists the skip or focus markers whose count grows from
// before to after, one entry per pattern, as the marker last written in after.
// Only growth counts: moving an existing t.Skip leaves the count unchanged. It
// is a text heuristic (D3): a marker in a comment or a string counts the same,
// and the way out of that false positive is declaring item 6 in the spec.
func SkipMarkersAdded(lang TestLanguage, before, after string) []string {
	var added []string
	for _, re := range skipMarkers[lang] {
		got := re.FindAllStringSubmatch(after, -1)
		if len(got) > len(re.FindAllStringIndex(before, -1)) {
			added = append(added, got[len(got)-1][1])
		}
	}
	return added
}

// HasTestFunction reports whether text declares at least one test function
// in lang's convention. The guard compares it before and after a write to
// catch a test file left with no tests at all.
func HasTestFunction(lang TestLanguage, text string) bool {
	re, ok := testFunctions[lang]
	return ok && re.MatchString(text)
}

// ApplyEdit simulates Edit/MultiEdit. applied is false when oldString is
// absent: the tool itself will fail, so after == before and nothing is judged.
// An empty oldString only applies to an empty file, the way the tool creates
// one.
func ApplyEdit(before, oldString, newString string, replaceAll bool) (after string, applied bool) {
	if oldString == "" {
		if before == "" {
			return newString, true
		}
		return before, false
	}
	if !strings.Contains(before, oldString) {
		return before, false
	}
	if replaceAll {
		return strings.ReplaceAll(before, oldString, newString), true
	}
	return strings.Replace(before, oldString, newString, 1), true
}
