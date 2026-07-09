package sync_test

import (
	"bytes"
	"context"
	"testing"

	mnemeSync "github.com/wirvii/mneme/internal/sync"
)

// TestDetectFormat_Extension verifies that the file name alone is sufficient
// to distinguish the two known formats.
func TestDetectFormat_Extension(t *testing.T) {
	tests := []struct {
		name string
		want mnemeSync.SyncFormat
	}{
		{"my-project.manifest.tar.gz", mnemeSync.FormatManifest},
		{"my-project.MANIFEST.TAR.GZ", mnemeSync.FormatManifest}, // case-insensitive
		{"my-project.jsonl.gz", mnemeSync.FormatJSONL},
		{"my-project.JSONL.GZ", mnemeSync.FormatJSONL},
		{"my-project.json", mnemeSync.FormatUnknown},
		{"my-project.gz", mnemeSync.FormatUnknown},
		{"my-project", mnemeSync.FormatUnknown},
		{"", mnemeSync.FormatUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mnemeSync.DetectFormat(tc.name)
			if got != tc.want {
				t.Errorf("DetectFormat(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestDetectFormatFromContent_Manifest verifies that a real manifest tar.gz
// produced by ManifestExporter is identified as FormatManifest.
func TestDetectFormatFromContent_Manifest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "detect/manifest"
	if _, err := s.Create(ctx, makeMemory(project)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(s, "mneme", "test")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	got := mnemeSync.DetectFormatFromContent(buf.Bytes())
	if got != mnemeSync.FormatManifest {
		t.Errorf("DetectFormatFromContent(manifest) = %v, want FormatManifest", got)
	}
}

// TestDetectFormatFromContent_JSONL verifies that a real JSONL.gz archive is
// identified as FormatJSONL.
func TestDetectFormatFromContent_JSONL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "detect/jsonl"
	if _, err := s.Create(ctx, makeMemory(project)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var buf bytes.Buffer
	exp := mnemeSync.NewExporter(s)
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	got := mnemeSync.DetectFormatFromContent(buf.Bytes())
	if got != mnemeSync.FormatJSONL {
		t.Errorf("DetectFormatFromContent(jsonl) = %v, want FormatJSONL", got)
	}
}

// TestDetectFormatFromContent_Invalid verifies that garbage bytes return
// FormatUnknown without panicking.
func TestDetectFormatFromContent_Invalid(t *testing.T) {
	got := mnemeSync.DetectFormatFromContent([]byte("this is not a gzip stream"))
	if got != mnemeSync.FormatUnknown {
		t.Errorf("got %v, want FormatUnknown", got)
	}
}
