package quality

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// encodeTestPNG encodes a w x h RGBA canvas, filled with base, except the
// given diffCount pixels (starting at 0,0, row-major) set to a visibly
// different color — the exact shape ComparePNG's real-world callers (a
// project's own captures) will hand it, generated with image/png itself so
// the fixture is a REAL PNG, never a synthetic byte string (Trampa 5 of the
// plan).
func encodeTestPNG(t *testing.T, w, h, diffCount int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	base := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	diff := color.RGBA{R: 250, G: 5, B: 5, A: 255}

	set := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := base
			if set < diffCount {
				c = diff
				set++
			}
			img.Set(x, y, c)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// TestComparePNG_IdenticalImages covers the identical-images hermana of
// AC13: diff_pct is exactly 0.
func TestComparePNG_IdenticalImages(t *testing.T) {
	ref := encodeTestPNG(t, 40, 50, 0)
	cap := encodeTestPNG(t, 40, 50, 0)

	d, err := ComparePNG(ref, cap)
	if err != nil {
		t.Fatalf("ComparePNG: %v", err)
	}
	if d.DimensionMismatch {
		t.Fatalf("DimensionMismatch = true, want false")
	}
	if d.DiffPct != 0 {
		t.Errorf("DiffPct = %v, want 0", d.DiffPct)
	}
	if d.ExceedsTolerance(0) {
		t.Errorf("ExceedsTolerance(0) = true, want false for identical images")
	}
}

// TestComparePNG_ToleranceBoundary covers AC13's three-way tolerance
// table, and IS G12a's guardian: 2000 total pixels makes each pixel worth
// exactly 0.05 percentage points, so 1/2/4 diff pixels land at 0.05%/0.1%/
// 0.2% precisely.
func TestComparePNG_ToleranceBoundary(t *testing.T) {
	const w, h = 40, 50 // 2000 pixels total
	ref := encodeTestPNG(t, w, h, 0)

	tests := []struct {
		name        string
		diffPixels  int
		maxDiffPct  float64
		wantDiffPct float64
		wantExceeds bool
	}{
		{name: "0.05% under 0.1% tolerance passes", diffPixels: 1, maxDiffPct: 0.1, wantDiffPct: 0.05, wantExceeds: false},
		{name: "exactly at tolerance passes (strict >)", diffPixels: 2, maxDiffPct: 0.1, wantDiffPct: 0.1, wantExceeds: false},
		{name: "0.2% over 0.1% tolerance fails", diffPixels: 4, maxDiffPct: 0.1, wantDiffPct: 0.2, wantExceeds: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := encodeTestPNG(t, w, h, tt.diffPixels)
			d, err := ComparePNG(ref, cap)
			if err != nil {
				t.Fatalf("ComparePNG: %v", err)
			}
			if d.DiffPixels != tt.diffPixels {
				t.Errorf("DiffPixels = %d, want %d", d.DiffPixels, tt.diffPixels)
			}
			if d.DiffPct != tt.wantDiffPct {
				t.Errorf("DiffPct = %v, want %v", d.DiffPct, tt.wantDiffPct)
			}
			if got := d.ExceedsTolerance(tt.maxDiffPct); got != tt.wantExceeds {
				t.Errorf("ExceedsTolerance(%v) = %v, want %v", tt.maxDiffPct, got, tt.wantExceeds)
			}
		})
	}
}

// TestComparePNG_DimensionMismatch covers G12b: different dimensions is a
// mismatch WITHOUT a computed percentage — DiffPct/DiffPixels/TotalPixels
// all stay at their zero value, never a fabricated 0 or 100.
func TestComparePNG_DimensionMismatch(t *testing.T) {
	ref := encodeTestPNG(t, 40, 50, 0)
	cap := encodeTestPNG(t, 41, 50, 0)

	d, err := ComparePNG(ref, cap)
	if err != nil {
		t.Fatalf("ComparePNG: %v", err)
	}
	if !d.DimensionMismatch {
		t.Fatalf("DimensionMismatch = false, want true")
	}
	if d.RefWidth != 40 || d.Width != 41 {
		t.Errorf("RefWidth/Width = %d/%d, want 40/41", d.RefWidth, d.Width)
	}
	if d.DiffPixels != 0 || d.TotalPixels != 0 || d.DiffPct != 0 {
		t.Errorf("DimensionMismatch must leave DiffPixels/TotalPixels/DiffPct at zero, got %+v", d)
	}
}

// TestComparePNG_NotPNG covers ErrNotPNG for both positions — reference and
// capture — each naming which one failed.
func TestComparePNG_NotPNG(t *testing.T) {
	valid := encodeTestPNG(t, 4, 4, 0)
	garbage := []byte("this is not a png file")

	t.Run("reference not PNG", func(t *testing.T) {
		_, err := ComparePNG(garbage, valid)
		if !errors.Is(err, ErrNotPNG) {
			t.Fatalf("error = %v, want ErrNotPNG", err)
		}
	})
	t.Run("capture not PNG", func(t *testing.T) {
		_, err := ComparePNG(valid, garbage)
		if !errors.Is(err, ErrNotPNG) {
			t.Fatalf("error = %v, want ErrNotPNG", err)
		}
	})
}

// patchIHDRDimensions rewrites a real PNG's IHDR chunk to declare width x
// height instead of whatever the image actually contains, recalculating the
// chunk's CRC-32 with hash/crc32 (stdlib) so the header alone remains
// well-formed even though the pixel data no longer matches it — exactly the
// "hostile or corrupt PNG declaring absurd dimensions" MaxComparePixels
// exists to catch, built for real rather than asserted about (Trampa 5).
func patchIHDRDimensions(t *testing.T, raw []byte, width, height uint32) []byte {
	t.Helper()
	// PNG: 8-byte signature, then one chunk per record:
	// 4-byte length + 4-byte type + <length> bytes data + 4-byte CRC.
	// IHDR is always the first chunk and its data is always 13 bytes:
	// width(4) height(4) bitdepth(1) colortype(1) compression(1) filter(1)
	// interlace(1).
	const sigLen = 8
	if len(raw) < sigLen+8+13+4 {
		t.Fatalf("fixture PNG too short to contain a full IHDR chunk")
	}
	chunkType := raw[sigLen+4 : sigLen+8]
	if string(chunkType) != "IHDR" {
		t.Fatalf("first chunk is %q, want IHDR", chunkType)
	}

	patched := make([]byte, len(raw))
	copy(patched, raw)

	dataStart := sigLen + 8
	binary.BigEndian.PutUint32(patched[dataStart:dataStart+4], width)
	binary.BigEndian.PutUint32(patched[dataStart+4:dataStart+8], height)

	// CRC covers the chunk TYPE plus its DATA (not the length field).
	crcInput := patched[sigLen+4 : dataStart+13]
	crc := crc32.ChecksumIEEE(crcInput)
	crcStart := dataStart + 13
	binary.BigEndian.PutUint32(patched[crcStart:crcStart+4], crc)

	return patched
}

// TestComparePNG_HeaderExceedsMaxComparePixels covers G12c: a PNG whose
// IHDR alone declares dimensions above MaxComparePixels is rejected via
// png.DecodeConfig BEFORE png.Decode ever runs — proven by patching only the
// header of an otherwise-tiny, valid PNG (its pixel DATA still describes a
// 4x4 image) and confirming the error fires anyway, from the header alone.
func TestComparePNG_HeaderExceedsMaxComparePixels(t *testing.T) {
	small := encodeTestPNG(t, 4, 4, 0)
	hostile := patchIHDRDimensions(t, small, 40000, 40000) // 1.6e9 > MaxComparePixels

	valid := encodeTestPNG(t, 4, 4, 0)

	t.Run("hostile reference", func(t *testing.T) {
		_, err := ComparePNG(hostile, valid)
		if !errors.Is(err, ErrImageTooLarge) {
			t.Fatalf("error = %v, want ErrImageTooLarge", err)
		}
	})
	t.Run("hostile capture", func(t *testing.T) {
		_, err := ComparePNG(valid, hostile)
		if !errors.Is(err, ErrImageTooLarge) {
			t.Fatalf("error = %v, want ErrImageTooLarge", err)
		}
	})
}
