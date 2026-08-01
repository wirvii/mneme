package mcp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// errCustomRead is a sentinel used to prove readMessage propagates a real
// I/O failure unwrapped (AC12) instead of ever turning it into a JSON-RPC
// error response.
var errCustomRead = errors.New("boom: disk on fire")

// erroringReader always returns errCustomRead without producing any bytes.
type erroringReader struct{}

func (erroringReader) Read(_ []byte) (int, error) { return 0, errCustomRead }

func TestReadMessage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int

		wantLine    string // asserted when wantErr and wantTooLong are both nil
		wantErr     error  // asserted via errors.Is when set
		wantTooLong bool   // when true, asserts errors.As into *messageTooLongError
		wantSize    int    // only checked when wantTooLong
		wantPrefix  string // only checked when wantTooLong
	}{
		{
			name:     "línea exacta al límite",
			input:    "aaaaaaaaaa\n", // 10 'a' + '\n' = 11 bytes, limit 11 → not over
			limit:    11,
			wantLine: "aaaaaaaaaa",
		},
		{
			name:        "límite+1",
			input:       "aaaaaaaaaaa\n", // 11 'a' + '\n' = 12 bytes, limit 11 → over by 1
			limit:       11,
			wantTooLong: true,
			wantSize:    12,
			wantPrefix:  "aaaaaaaaaaa\n",
		},
		{
			name:    "entrada vacía",
			input:   "",
			limit:   100,
			wantErr: io.EOF,
		},
		{
			name:     "línea final sin delimitador, bajo el límite",
			input:    "hello",
			limit:    100,
			wantLine: "hello",
		},
		{
			name:        "línea final sin delimitador, sobre el límite",
			input:       "aaaaaaaaaaa", // 11 bytes, no '\n', limit 10 → over
			limit:       10,
			wantTooLong: true,
			wantSize:    11,
			wantPrefix:  "aaaaaaaaaaa",
		},
		{
			name:     "línea con CRLF",
			input:    "hello\r\n",
			limit:    100,
			wantLine: "hello",
		},
		{
			name:     "línea vacía",
			input:    "\n",
			limit:    100,
			wantLine: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			line, err := readMessage(r, tt.limit)

			switch {
			case tt.wantTooLong:
				var tooLong *messageTooLongError
				if !errors.As(err, &tooLong) {
					t.Fatalf("readMessage error = %v, want *messageTooLongError", err)
				}
				if line != nil {
					t.Errorf("line = %q, want nil alongside a *messageTooLongError", line)
				}
				if tooLong.Size != tt.wantSize {
					t.Errorf("Size = %d, want %d", tooLong.Size, tt.wantSize)
				}
				if tooLong.Limit != tt.limit {
					t.Errorf("Limit = %d, want %d", tooLong.Limit, tt.limit)
				}
				if string(tooLong.Prefix) != tt.wantPrefix {
					t.Errorf("Prefix = %q, want %q", tooLong.Prefix, tt.wantPrefix)
				}
				if !strings.HasPrefix(tooLong.Error(), "mcp: message too large:") {
					t.Errorf("Error() = %q, want prefix %q", tooLong.Error(), "mcp: message too large:")
				}

			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("readMessage error = %v, want %v", err, tt.wantErr)
				}
				if line != nil {
					t.Errorf("line = %q, want nil alongside error %v", line, err)
				}

			default:
				if err != nil {
					t.Fatalf("readMessage unexpected error: %v", err)
				}
				if string(line) != tt.wantLine {
					t.Errorf("line = %q, want %q", line, tt.wantLine)
				}
			}
		})
	}
}

// TestReadMessage_ResyncsAfterOversizedLine verifies D3's resynchronisation
// guarantee directly: when the first of two lines exceeds the limit but the
// second does not, the first readMessage call discards the first line THROUGH
// its terminating '\n' and reports messageTooLongError, and the very next
// readMessage call on the same reader reads the second line as a normal,
// correct message — never a corrupted tail of the first.
func TestReadMessage_ResyncsAfterOversizedLine(t *testing.T) {
	input := "aaaaaaaaaaa\n" + "hi\n" // first line is 12 bytes (over limit 11); second is 3 (under)
	r := bufio.NewReader(strings.NewReader(input))

	line1, err1 := readMessage(r, 11)
	var tooLong *messageTooLongError
	if !errors.As(err1, &tooLong) {
		t.Fatalf("first readMessage error = %v, want *messageTooLongError", err1)
	}
	if line1 != nil {
		t.Errorf("first line = %q, want nil", line1)
	}
	if tooLong.Size != 12 {
		t.Errorf("Size = %d, want 12", tooLong.Size)
	}

	line2, err2 := readMessage(r, 11)
	if err2 != nil {
		t.Fatalf("second readMessage unexpected error: %v", err2)
	}
	if string(line2) != "hi" {
		t.Errorf("second line = %q, want %q", line2, "hi")
	}
}

// TestReadMessage_IOFailure verifies AC12: a real I/O error (never io.EOF)
// from the underlying reader is returned from readMessage unwrapped, not
// turned into a *messageTooLongError or otherwise swallowed.
func TestReadMessage_IOFailure(t *testing.T) {
	r := bufio.NewReader(erroringReader{})

	line, err := readMessage(r, 100)
	if !errors.Is(err, errCustomRead) {
		t.Fatalf("readMessage error = %v, want %v", err, errCustomRead)
	}
	if line != nil {
		t.Errorf("line = %q, want nil", line)
	}
}

// TestReadMessage_MemoryCeiling is AC11's directly-verifiable half: fed an
// input far larger than limit with no delimiter at all, readMessage must not
// buffer the whole thing — the returned error still reports the true total
// size, but the discarded line itself (buf) is never retained once the
// running total crosses limit. We can't assert on unexported allocation
// directly, but we CAN assert the function returns promptly with the right
// Size for an input orders of magnitude larger than limit, which is only
// possible if the accumulation loop actually stops appending (an accidental
// ReadBytes-style full accumulation would still return the right Size, but
// this test's real value is combined with a memory profiler in manual
// verification — see spec §7.2 AC11 note).
// TestRequestIDFromPrefix is the spec §7.3 table: requestIDFromPrefix must
// never panic or error, must preserve a numeric id's exact literal (via
// UseNumber), must correctly re-encode a string id, and must fall back to
// `null` for every ambiguous or malformed case rather than guessing.
func TestRequestIDFromPrefix(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{
			name:   "id numérico",
			prefix: `{"jsonrpc":"2.0","id":42,"method":"tools/call"}`,
			want:   "42",
		},
		{
			name:   "id string",
			prefix: `{"jsonrpc":"2.0","id":"abc-123","method":"tools/call"}`,
			want:   `"abc-123"`,
		},
		{
			name:   "id grande (>2^53), literal exacto",
			prefix: `{"jsonrpc":"2.0","id":9007199254740993123,"method":"tools/call"}`,
			want:   "9007199254740993123",
		},
		{
			name:   "id después de method",
			prefix: `{"jsonrpc":"2.0","method":"tools/call","id":7}`,
			want:   "7",
		},
		{
			name:   "id ausente",
			prefix: `{"jsonrpc":"2.0","method":"tools/call","params":{}}`,
			want:   "null",
		},
		{
			name:   "id anidado en params",
			prefix: `{"jsonrpc":"2.0","params":{"id":5},"method":"tools/call"}`,
			want:   "null",
		},
		{
			name:   `"method":"id" no debe confundirse`,
			prefix: `{"jsonrpc":"2.0","method":"id","params":{}}`,
			want:   "null",
		},
		{
			// A truncated NUMBER (e.g. `"id":12` with nothing after) is not
			// actually ambiguous to the decoder — digits are self-terminating
			// at EOF, so `12` decodes as a complete, correct value. A
			// truncated STRING is genuinely ambiguous (no closing quote means
			// the decoder cannot tell where the value ends), which is the
			// real "mid-value truncation" this case exercises.
			name:   "prefijo truncado a mitad del valor",
			prefix: `{"jsonrpc":"2.0","id":"abc`,
			want:   "null",
		},
		{
			name:   "prefijo que no es JSON",
			prefix: `not json at all`,
			want:   "null",
		},
		{
			name:   "prefijo vacío",
			prefix: ``,
			want:   "null",
		},
		{
			name:   "id: null explícito",
			prefix: `{"jsonrpc":"2.0","id":null,"method":"tools/call"}`,
			want:   "null",
		},
		{
			name:   "id booleano (tipo no soportado)",
			prefix: `{"jsonrpc":"2.0","id":true,"method":"tools/call"}`,
			want:   "null",
		},
		{
			name:   "id objeto (tipo no soportado)",
			prefix: `{"jsonrpc":"2.0","id":{"nested":1},"method":"tools/call"}`,
			want:   "null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := requestIDFromPrefix([]byte(tt.prefix))
			if string(got) != tt.want {
				t.Errorf("requestIDFromPrefix(%q) = %s, want %s", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestReadMessage_MemoryCeiling(t *testing.T) {
	const limit = 16
	huge := bytes.Repeat([]byte("x"), limit*1000) // no delimiter at all
	r := bufio.NewReader(bytes.NewReader(huge))

	line, err := readMessage(r, limit)
	var tooLong *messageTooLongError
	if !errors.As(err, &tooLong) {
		t.Fatalf("readMessage error = %v, want *messageTooLongError", err)
	}
	if line != nil {
		t.Errorf("line = %q, want nil", line)
	}
	if tooLong.Size != len(huge) {
		t.Errorf("Size = %d, want %d", tooLong.Size, len(huge))
	}
	if len(tooLong.Prefix) != idPrefixBytes {
		t.Errorf("Prefix length = %d, want %d (idPrefixBytes)", len(tooLong.Prefix), idPrefixBytes)
	}
}
