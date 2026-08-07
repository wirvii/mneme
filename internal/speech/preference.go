package speech

import "strings"

// Preference identifies the engine and voice desired for one language, plus
// the explicit native fallback used when the preferred engine is unavailable.
type Preference struct {
	Engine         string `json:"engine"`
	Voice          string `json:"voice"`
	FallbackEngine string `json:"fallback_engine,omitempty"`
	FallbackVoice  string `json:"fallback_voice,omitempty"`
}

// ResolvedPreference records which locale tier supplied a speech preference.
// Source is exact, base, default, or legacy.
type ResolvedPreference struct {
	Preference
	Language string `json:"language"`
	Source   string `json:"source"`
}

// ResolvePreference applies exact-locale, base-language, default, then legacy
// voice precedence without coupling the speech leaf to the config package.
func ResolvePreference(language string, preferences map[string]Preference, legacyVoices map[string]string, defaultEngine string) ResolvedPreference {
	language = normalizeLanguage(language)
	keys := []struct {
		key    string
		source string
	}{{language, "exact"}}
	if base := baseLanguage(language); base != "" && base != language {
		keys = append(keys, struct {
			key    string
			source string
		}{base, "base"})
	}
	keys = append(keys, struct {
		key    string
		source string
	}{"default", "default"})
	for _, candidate := range keys {
		if preference, ok := preferences[candidate.key]; ok && (preference.Engine != "" || preference.Voice != "") {
			if preference.Engine == "" {
				preference.Engine = defaultEngine
			}
			return ResolvedPreference{Preference: preference, Language: language, Source: candidate.source}
		}
	}
	for _, candidate := range keys[:len(keys)-1] {
		if voice := legacyVoices[candidate.key]; voice != "" {
			return ResolvedPreference{Preference: Preference{Engine: defaultEngine, Voice: voice}, Language: language, Source: "legacy"}
		}
	}
	return ResolvedPreference{Preference: Preference{Engine: defaultEngine}, Language: language, Source: "default"}
}

func normalizeLanguage(language string) string {
	language = strings.TrimSpace(strings.ReplaceAll(language, "_", "-"))
	parts := strings.Split(language, "-")
	if len(parts) == 0 {
		return ""
	}
	parts[0] = strings.ToLower(parts[0])
	if len(parts) > 1 {
		parts[1] = strings.ToUpper(parts[1])
	}
	return strings.Join(parts, "-")
}

func baseLanguage(language string) string {
	if index := strings.IndexByte(language, '-'); index >= 0 {
		return language[:index]
	}
	return language
}
