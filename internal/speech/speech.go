// Package speech implements mneme's private, host-local spoken response
// channel. It deliberately has no dependency on mneme's persistence layers.
package speech

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// BriefLimit is the maximum cleaned size of a brief spoken response.
	BriefLimit = 800
	// FullLimit is the maximum cleaned size of a full spoken response.
	FullLimit = 20_000
)

// Mode selects the amount of useful response content to speak.
type Mode string

const (
	ModeBrief Mode = "brief"
	ModeFull  Mode = "full"
)

// Disposition tells speech_emit whether to speak or intentionally remain silent.
type Disposition string

const (
	DispositionEmit Disposition = "emit"
	DispositionSkip Disposition = "skip"
)

var (
	ErrDisabled           = errors.New("speech is disabled")
	ErrInvalidMode        = errors.New("invalid speech mode")
	ErrInvalidDisposition = errors.New("invalid speech disposition")
	ErrTextRequired       = errors.New("spoken text is required")
	ErrTextForbidden      = errors.New("skipped speech must not include text")
	ErrTextTooLong        = errors.New("spoken text exceeds mode limit")
	// ErrUnknownEngine indicates an engine name mneme does not recognize. The
	// native engines are host globals: voice does not depend on language, so
	// listing them takes no language filter.
	ErrUnknownEngine = errors.New("speech: unknown engine")
	markdownLink     = regexp.MustCompile(`\[([^]]+)\]\([^)]*\)`)
	codeFence        = regexp.MustCompile("(?s)```.*?```")
	inlineCode       = regexp.MustCompile("`([^`]*)`")
	markdownPrefix   = regexp.MustCompile(`(?m)^\s{0,3}(?:#{1,6}\s+|[-*+]\s+|>\s*)`)
	whitespace       = regexp.MustCompile(`\s+`)
)

// Clean removes visual-only Markdown while retaining prose worth hearing.
func Clean(text string) string {
	text = codeFence.ReplaceAllString(text, " ")
	text = markdownLink.ReplaceAllString(text, "$1")
	text = inlineCode.ReplaceAllString(text, "$1")
	text = markdownPrefix.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "__", "")
	return strings.TrimSpace(whitespace.ReplaceAllString(text, " "))
}

// ValidateEmit validates and cleans one semantic speech resolution.
func ValidateEmit(disposition Disposition, mode Mode, text string) (string, error) {
	if mode != ModeBrief && mode != ModeFull {
		return "", ErrInvalidMode
	}
	if disposition != DispositionEmit && disposition != DispositionSkip {
		return "", ErrInvalidDisposition
	}
	if disposition == DispositionSkip {
		if strings.TrimSpace(text) != "" {
			return "", ErrTextForbidden
		}
		return "", nil
	}
	cleaned := Clean(text)
	if cleaned == "" {
		return "", ErrTextRequired
	}
	limit := BriefLimit
	if mode == ModeFull {
		limit = FullLimit
	}
	if utf8.RuneCountInString(cleaned) > limit {
		return "", ErrTextTooLong
	}
	return cleaned, nil
}
