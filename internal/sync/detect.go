package sync

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
)

// SyncFormat identifies the wire format of a sync archive.
type SyncFormat int

const (
	// FormatUnknown means the format could not be determined.
	FormatUnknown SyncFormat = iota

	// FormatJSONL is the legacy gzip-compressed JSON Lines format (.jsonl.gz).
	FormatJSONL

	// FormatManifest is the Memory Manifest v1.0 tar.gz format (.manifest.tar.gz).
	FormatManifest
)

// DetectFormat infers the sync format from the file name. If the name is
// ambiguous or the extension is unrecognised, DetectFormatFromContent should
// be used on the file bytes instead.
//
// Detection rules (highest priority first):
//  1. Suffix ".manifest.tar.gz" → FormatManifest
//  2. Suffix ".jsonl.gz" → FormatJSONL
//  3. Otherwise → FormatUnknown
func DetectFormat(name string) SyncFormat {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, manifestTarGzExtension):
		return FormatManifest
	case strings.HasSuffix(lower, ".jsonl.gz"):
		return FormatJSONL
	default:
		return FormatUnknown
	}
}

// DetectFormatFromContent reads the first bytes of a gzip-compressed stream
// to distinguish a tar archive (manifest) from a JSONL stream. Both formats
// begin with a gzip magic header (1f 8b); the difference is in what follows
// after decompression.
//
// After gzip-decompressing, a manifest archive starts with the 257-byte POSIX
// tar magic sequence; a JSONL archive starts with a '{' JSON character.
//
// r must be positioned at the start of the stream. The function reads at most
// 512 bytes of decompressed content and does not advance r beyond the header.
//
// Returns FormatManifest, FormatJSONL, or FormatUnknown.
func DetectFormatFromContent(data []byte) SyncFormat {
	if len(data) < 2 {
		return FormatUnknown
	}

	// Both formats are gzip — if decompression fails, it is neither.
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return FormatUnknown
	}
	defer gz.Close()

	// Read up to 512 bytes of decompressed content.
	var buf [512]byte
	n, err := io.ReadFull(gz, buf[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return FormatUnknown
	}
	decompressed := buf[:n]

	// A POSIX tar header has the magic string "ustar" at byte offset 257.
	// GNU tar uses "ustar " (with a space), POSIX uses "ustar\0".
	if len(decompressed) >= 265 {
		magic := decompressed[257:262]
		if bytes.Equal(magic, []byte("ustar")) {
			return FormatManifest
		}
	}

	// JSONL starts with '{' (the opening brace of the first JSON object).
	if len(decompressed) > 0 && decompressed[0] == '{' {
		return FormatJSONL
	}

	return FormatUnknown
}
