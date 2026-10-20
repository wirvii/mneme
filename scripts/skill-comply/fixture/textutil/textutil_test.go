package textutil

import "testing"

func TestCapitalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello", "Hello"},
		{"Hello", "Hello"},
		{"a", "A"},
	}
	for _, tc := range tests {
		if got := Capitalize(tc.in); got != tc.want {
			t.Errorf("Capitalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
