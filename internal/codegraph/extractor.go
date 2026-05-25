// Package codegraph — this file defines the Extractor registry and language
// detection logic. Language-specific extractors register themselves via
// RegisterExtractor so the indexer can select the right parser at runtime
// without a static switch on language names.
package codegraph

import "path/filepath"

// extractorRegistry maps language identifiers to factory functions that produce
// fresh Extractor instances. Factories are used instead of singleton values
// because Extractor implementations may carry per-file parse state.
var extractorRegistry = map[string]func() Extractor{}

// RegisterExtractor records a factory function for the given language identifier.
// It must be called before the first call to GetExtractor for that language.
// Conventionally, each language extractor package calls RegisterExtractor in its
// init() function so registration is automatic on import.
func RegisterExtractor(lang string, factory func() Extractor) {
	extractorRegistry[lang] = factory
}

// DetectLanguage infers the programming language from a file's extension.
// It returns an empty string for unrecognised extensions.
func DetectLanguage(filePath string) string {
	switch filepath.Ext(filePath) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs":
		return "javascript"
	default:
		return ""
	}
}

// GetExtractor returns a fresh Extractor for the given language identifier, or
// nil if no extractor has been registered for that language.
func GetExtractor(language string) Extractor {
	factory, ok := extractorRegistry[language]
	if !ok {
		return nil
	}
	return factory()
}

func init() {
	RegisterExtractor("go", func() Extractor { return NewGoExtractor() })
}
