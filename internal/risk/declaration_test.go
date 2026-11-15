package risk

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		none   bool
		items  []Item
		reason string
		bad    bool
	}{
		{name: "none", line: "Risk: none", none: true},
		{name: "ninguno", line: "Riesgo: ninguno", none: true},
		{name: "riesgo_none", line: "Riesgo: none", none: true},
		{name: "case_and_spaces", line: "risk:   NONE", none: true},
		{name: "two_items_reason", line: "Risk: item 1, 5 (x)", items: []Item{1, 5}, reason: "x"},
		{name: "accented_item", line: "Risk: ítem 3", items: []Item{3}},
		{name: "repeated_word_sorted", line: "Risk: item 5, item 1", items: []Item{1, 5}},
		{name: "duplicates", line: "Risk: item 1, 1", items: []Item{1}},
		{name: "dash_list", line: "- Risk: none", none: true},
		{name: "star_list", line: "* Risk: none", none: true},
		{name: "item_out_of_range", line: "Risk: item 7", bad: true},
		{name: "item_zero", line: "Risk: item 0", bad: true},
		{name: "maybe", line: "Risk: maybe", bad: true},
		{name: "empty", line: "Risk:", bad: true},
		{name: "not_a_declaration", line: "hello", bad: true},
		{name: "number_without_word", line: "Risk: 1", bad: true},
		{name: "unclosed_reason", line: "Risk: item 1 (x", bad: true},
		{name: "empty_list_entry", line: "Risk: item 1,", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLine(tt.line)
			if tt.bad {
				if !errors.Is(err, ErrMalformedDeclaration) {
					t.Fatalf("err = %v, want ErrMalformedDeclaration", err)
				}
				if !strings.Contains(err.Error(), tt.line) {
					t.Errorf("error %q does not quote the line %q", err, tt.line)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.None != tt.none || !reflect.DeepEqual(got.Items, tt.items) || got.Reason != tt.reason {
				t.Errorf("got %+v, want None=%v Items=%v Reason=%q", got, tt.none, tt.items, tt.reason)
			}
			if got.Line != tt.line {
				t.Errorf("Line = %q, want %q", got.Line, tt.line)
			}
		})
	}
}

func TestFindDeclaration(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		found bool
		none  bool
		items []Item
		bad   bool
	}{
		{name: "no_line", text: "just prose\nmore prose"},
		{name: "last_wins", text: "Risk: item 1\nprose\nRisk: none\n", found: true, none: true},
		{name: "last_malformed_reported", text: "Risk: none\nRisk: perhaps", found: true, bad: true},
		{name: "backtick_fence", text: "Risk: none\n```\nRisk: item 1\n```\n", found: true, none: true},
		{name: "tilde_fence", text: "Risk: none\n~~~\nRisk: item 1\n~~~\n", found: true, none: true},
		{name: "four_backticks_not_closed_by_three", text: "Risk: none\n````\n```\nRisk: item 1\n", found: true, none: true},
		{name: "unclosed_fence", text: "Risk: none\n```go\nRisk: item 1\nmore\n", found: true, none: true},
		{name: "none_outside_item_inside_fence", text: "```\nRisk: item 1\n```\nRisk: none", found: true, none: true},
		{name: "tilde_not_closed_by_backticks", text: "Risk: none\n~~~\n```\nRisk: item 1\n", found: true, none: true},
		{name: "closing_line_with_text_does_not_close", text: "Risk: none\n```\n``` not a close\nRisk: item 1\n", found: true, none: true},
		{name: "fence_closed_then_declaration", text: "```\nx\n```\nRisk: item 2 (y)", found: true, items: []Item{2}},
		{name: "inline_code_is_not_a_declaration", text: "see `Risk: item 1` here", found: false},
		{name: "crlf_lines", text: "prose\r\nRisk: item 5\r\n", found: true, items: []Item{5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := FindDeclaration(tt.text)
			if found != tt.found {
				t.Fatalf("found = %v, want %v", found, tt.found)
			}
			if tt.bad {
				if !errors.Is(err, ErrMalformedDeclaration) {
					t.Fatalf("err = %v, want ErrMalformedDeclaration", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.None != tt.none || !reflect.DeepEqual(got.Items, tt.items) {
				t.Errorf("got %+v, want None=%v Items=%v", got, tt.none, tt.items)
			}
		})
	}
}
