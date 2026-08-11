package speech

import "testing"

func TestResolvePreference(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		language    string
		preferences map[string]Preference
		legacy      map[string]string
		wantEngine  string
		wantVoice   string
		wantSource  string
	}{
		{name: "exact locale", language: "es_MX", preferences: map[string]Preference{"es-MX": {Engine: "piper", Voice: "es-mx-voice"}}, wantEngine: "piper", wantVoice: "es-mx-voice", wantSource: "exact"},
		{name: "base language", language: "es-CO", preferences: map[string]Preference{"es": {Engine: "piper", Voice: "es-mx-voice"}}, wantEngine: "piper", wantVoice: "es-mx-voice", wantSource: "base"},
		{name: "default preference", language: "fr", preferences: map[string]Preference{"default": {Engine: "system", Voice: "Default"}}, wantEngine: "system", wantVoice: "Default", wantSource: "default"},
		{name: "legacy exact", language: "es-MX", legacy: map[string]string{"es-MX": "Paulina"}, wantEngine: "system", wantVoice: "Paulina", wantSource: "legacy"},
		{name: "legacy base", language: "es-CO", legacy: map[string]string{"es": "Paulina"}, wantEngine: "system", wantVoice: "Paulina", wantSource: "legacy"},
		{name: "engine fallback", language: "de", wantEngine: "system", wantSource: "default"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ResolvePreference(test.language, test.preferences, test.legacy, "system")
			if got.Engine != test.wantEngine || got.Voice != test.wantVoice || got.Source != test.wantSource {
				t.Fatalf("ResolvePreference() = %+v", got)
			}
		})
	}
}
