package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// maxMessageBytes bounds a single incoming JSON-RPC message. It is a fixed,
// non-configurable ceiling (SPEC-104 D2): mneme's own SDD documents can
// legitimately exceed the historical 64 KiB bufio.Scanner default that used
// to kill the server (the largest workflow document observed in the wild is
// ~104 KB), but no legitimate MCP message needs 10 MiB. The limit exists to
// bound memory against a pathological or hostile sender, not to reject real
// payloads — see Server.maxMessage (server.go) for why this constant is
// seeded into an unexported field rather than read directly.
const maxMessageBytes = 10 * 1024 * 1024

// readerBufferSize is the initial size of the bufio.Reader backing the
// message loop. 64 KiB matches the buffer size already used elsewhere in
// this codebase for bounded line readers (sync.go, querylog.go), and is
// large enough that the overwhelming majority of MCP messages need no
// further internal growth.
const readerBufferSize = 64 * 1024

// idPrefixBytes bounds how many leading bytes of a discarded oversized
// message are retained so its JSON-RPC "id" can be recovered (requestIDFromPrefix).
// In MCP the id field is emitted early in practice — typically within the
// first ~40 bytes, e.g. `{"jsonrpc":"2.0","id":123,"method":"tools/call","params":{...` —
// so 512 bytes gives generous headroom for field reordering while costing
// nothing once the oversized message itself is discarded.
const idPrefixBytes = 512

// messageTooLongError reports that a single incoming message exceeded the
// per-message limit. It carries the retained prefix so the caller can
// best-effort recover the JSON-RPC id of a request whose body was never read
// in full (SPEC-104 DD3/DD6). Its Error() text is also used verbatim as the
// JSON-RPC error.message sent back to the client (DD7): the prefix
// "mcp: message too large:" is the stable substring tests and clients can
// match on; the exact byte counts are informational, not part of the
// contract.
type messageTooLongError struct {
	Size   int    // total bytes consumed by the discarded message, delimiter included
	Limit  int    // the limit that was exceeded
	Prefix []byte // leading idPrefixBytes bytes of the discarded message
}

// Error implements the error interface. The message is deliberately a single
// ASCII line: it is echoed as-is into a JSON string value (error.message) and
// a multi-line or non-ASCII message would be harder to grep in client logs
// for no benefit.
func (e *messageTooLongError) Error() string {
	return fmt.Sprintf(
		"mcp: message too large: %d bytes exceeds the %d-byte per-message limit; "+
			"the request was discarded and the server is still running. Send a "+
			"smaller payload: shorten the content or split it across several calls.",
		e.Size, e.Limit,
	)
}

// trimEOL removes a trailing '\n' and, if present, a preceding '\r' from
// line. bufio.Scanner's default split function (ScanLines) does the same —
// replicating it here avoids a silent framing regression for any client that
// emits CRLF line endings (SPEC-104 DD5).
func trimEOL(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
	}
	return line
}

// readMessage reads one line-delimited JSON-RPC message from r, bounded by
// limit bytes. limit is a parameter rather than reading maxMessageBytes
// directly so tests can exercise the oversized path cheaply with a tiny
// limit instead of allocating maxMessageBytes-sized buffers per case
// (SPEC-104 DD1/DD2).
//
// The accumulation loop calls bufio.Reader.ReadSlice explicitly instead of
// the seemingly simpler bufio.Reader.ReadBytes: ReadBytes is implemented as
// a ReadSlice loop that ALWAYS appends every chunk it reads, so a hostile or
// corrupt input with no delimiter would make it allocate the entire input
// before anyone could compare it against limit — trading "the process dies
// on an oversized message" for "the process dies of OOM reading an
// oversized message", which is not a fix (DD4). Writing the loop explicitly
// lets readMessage stop accumulating — while continuing to drain — the
// instant the running total crosses limit, capping retained memory at
// roughly limit plus one reader-buffer's worth of bytes.
//
// Contract (DD3): a complete message returns (line, nil) with no trailing
// '\n' (and no preceding '\r'); the final message of the input with no
// trailing '\n' also returns (line, nil) — the same behaviour ScanLines has
// at EOF; an oversized message returns (nil, *messageTooLongError), and only
// once the reader has consumed through the next '\n' (or reached EOF) —
// so the following readMessage call always starts on a clean message
// boundary instead of reading the oversized message's leftover bytes as a
// new, bogus message; EOF found exactly at a message boundary returns
// (nil, io.EOF); any other read error is fatal and is returned unwrapped so
// the caller can add its own context.
func readMessage(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	var prefix []byte
	total := 0
	over := false

	for {
		chunk, err := r.ReadSlice('\n')
		total += len(chunk)

		if !over {
			if total > limit {
				over = true
				buf = nil // stop retaining the discarded message's bytes
			} else {
				buf = append(buf, chunk...)
			}
		}

		if need := idPrefixBytes - len(prefix); need > 0 {
			take := len(chunk)
			if take > need {
				take = need
			}
			prefix = append(prefix, chunk[:take]...)
		}

		if errors.Is(err, bufio.ErrBufferFull) {
			continue // delimiter not found yet within one buffer's worth; keep draining
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, err // real I/O failure: fatal, caller wraps it
			}
			if total == 0 {
				return nil, io.EOF // clean EOF exactly at a message boundary
			}
			if over {
				return nil, &messageTooLongError{Size: total, Limit: limit, Prefix: prefix}
			}
			return trimEOL(buf), nil // last message of the input, no trailing '\n'
		}

		if over {
			return nil, &messageTooLongError{Size: total, Limit: limit, Prefix: prefix}
		}
		return trimEOL(buf), nil
	}
}

// jsonFrame tracks one level of container nesting while requestIDFromPrefix
// walks a truncated JSON document token by token. atKey is only meaningful
// for object frames: true means the next scalar token at this depth is a
// key, false means it is that key's value. Arrays have no keys, so their
// atKey is never consulted.
type jsonFrame struct {
	isObject bool
	atKey    bool
}

// requestIDFromPrefix best-effort extracts the top-level JSON-RPC "id"
// member from prefix, the leading idPrefixBytes of a message that was
// discarded before it could be read in full (SPEC-104 DD6). It never panics
// and never returns an error to the caller: any ambiguity — the prefix ends
// mid-token, isn't JSON at all, never contains an "id" key, or contains one
// nested inside another member (e.g. inside "params") rather than at the
// top level — falls back to json.RawMessage("null"), which is exactly the
// JSON-RPC 2.0-compliant id to use when correlation isn't possible.
//
// The walk uses json.Decoder.Token() with UseNumber() rather than a full
// json.Unmarshal (the prefix is, by construction, very likely not a
// complete, parseable document — that's the whole reason we only have a
// prefix) or a regexp over raw JSON (fragile and unable to distinguish
// nesting depth, which is exactly what lets a "method":"id" value or an
// "id" nested inside "params" produce a false positive). UseNumber
// preserves a numeric id's exact source literal — critical for large
// integer ids, which json.Marshal would otherwise risk reformatting (e.g.
// scientific notation) and silently break client-side correlation.
//
// Only a key literally named "id" at depth 1 (directly inside the
// top-level object, the shape every JSON-RPC message has) is recognised;
// the same key name nested inside another object (or appearing as a VALUE
// rather than a key, e.g. "method":"id") is deliberately ignored.
func requestIDFromPrefix(prefix []byte) json.RawMessage {
	null := json.RawMessage("null")

	dec := json.NewDecoder(bytes.NewReader(prefix))
	dec.UseNumber()

	var stack []jsonFrame
	pendingID := false // true right after consuming the "id" key at depth 1

	popAndMarkParentValueConsumed := func() bool {
		if len(stack) == 0 {
			return false
		}
		stack = stack[:len(stack)-1]
		if n := len(stack); n > 0 && stack[n-1].isObject {
			stack[n-1].atKey = true
		}
		return true
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			return null // truncated mid-token, malformed, or exhausted: no id found
		}

		if delim, isDelim := tok.(json.Delim); isDelim {
			switch delim {
			case '{', '[':
				if pendingID {
					// The id's value is itself a container: per DD6 only
					// scalar ids (number/string) are recovered.
					return null
				}
				stack = append(stack, jsonFrame{isObject: delim == '{', atKey: true})
			case '}', ']':
				if !popAndMarkParentValueConsumed() {
					return null // unbalanced input: give up rather than guess
				}
			}
			continue
		}

		// tok is a scalar: string, json.Number, bool, or nil. This is the
		// id's value: every branch below returns, so there is no need to
		// clear pendingID here — the walk never continues past this point.
		if pendingID {
			if n := len(stack); n > 0 && stack[n-1].isObject {
				stack[n-1].atKey = true
			}
			switch v := tok.(type) {
			case json.Number:
				return json.RawMessage(v.String())
			case string:
				b, err := json.Marshal(v)
				if err != nil {
					return null
				}
				return b
			default: // bool or nil: not a correlatable id (DD6)
				return null
			}
		}

		if len(stack) == 0 || !stack[len(stack)-1].isObject {
			// A scalar outside any object (or inside an array) is never a
			// key we track; nothing to toggle.
			continue
		}

		top := len(stack) - 1
		if stack[top].atKey {
			// This scalar is the KEY for the object currently on top.
			stack[top].atKey = false
			if top == 0 { // depth 1: the only level whose "id" key counts
				if key, ok := tok.(string); ok && key == "id" {
					pendingID = true
				}
			}
			continue
		}

		// This scalar is a VALUE for the key just consumed at this depth.
		stack[top].atKey = true
	}
}
