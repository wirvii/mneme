package model

// Excerpt trims s to maxRunes RUNES — never bytes — and additionally reports
// whether any trimming occurred. Cutting by bytes (s[:200]) would split a
// multibyte character in half and produce invalid UTF-8: this repo's backlog
// ledgers carry accented characters and em-dashes (—, 3 bytes in UTF-8), so
// the case is not theoretical.
//
// No suffix is appended: the "..." is presentation and is up to each caller.
// makePreview appends it for backward compatibility; the SDD list views use
// the bool instead, which is an unambiguous signal rather than three dots
// that could themselves be part of the original content.
//
// maxRunes <= 0 returns ("", true) when s is non-empty: "nothing fits" is
// trimming, not the absence of it — returning false there would make a
// caller believe it holds the full text, which is exactly the false-data
// pattern SPEC-109 fixes.
func Excerpt(s string, maxRunes int) (excerpt string, truncated bool) {
	if maxRunes <= 0 {
		return "", s != ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s, false
	}
	return string(runes[:maxRunes]), true
}
