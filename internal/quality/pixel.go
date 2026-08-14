// Package quality — this file implements the visual mechanism's nivel 2
// (SPEC-120 EPIC-calidad S6 D7): a pure, Go-native, stdlib-only comparison
// of two PNG images. mneme does the comparison itself rather than trusting
// the harness to report "match"/"no match" — in this one layer, THE
// COMPARISON IS THE CHECK, and it is a pure function of two byte slices
// mneme can read and judge without knowing anything about browsers,
// screenshots, or ecosystems (a PNG is not an ecosystem).
package quality

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
)

// MaxComparePixels bounds the width*height product ComparePNG will ever
// decode pixel data for (D7/G12c) — checked via png.DecodeConfig, which
// reads ONLY the PNG header (the IHDR chunk), never the pixel data, BEFORE
// png.Decode is ever called. This is a resource cota on THIS binary, never a
// quality threshold a project tunes — the same boundary MaxSurvivorRows and
// MaxVisualTargetRows draw between "how much this mechanism can safely
// hold" and "what a project's constitution decides" — so it lives here, not
// in the constitution. Without this check, a hostile or merely corrupt PNG
// that declares absurd dimensions (e.g. 40000x40000) could make `mneme
// quality verify` allocate gigabytes of pixel buffer just to answer "do
// these two images match".
const MaxComparePixels = 64_000_000 // 64 megapixels (e.g. 8000x8000)

// ErrNotPNG is returned by ComparePNG when either input's bytes fail to
// decode as PNG — naming WHICH one (reference or capture), never silently
// treating an undecodable image as a mismatch or a pass.
var ErrNotPNG = errors.New("quality: not a valid PNG image")

// ErrImageTooLarge is returned by ComparePNG when an image's declared
// width*height exceeds MaxComparePixels, determined from the HEADER ALONE
// (png.DecodeConfig) — before any pixel data is ever read (G12c).
var ErrImageTooLarge = errors.New("quality: image exceeds MaxComparePixels")

// PixelDiff is ComparePNG's pure result (D7): RefWidth/RefHeight/Width/
// Height are always populated (even on a dimension mismatch, so the
// service layer can report the two shapes verbatim); DiffPixels/TotalPixels/
// DiffPct are populated ONLY when DimensionMismatch is false — comparing
// pixel-by-pixel across two different shapes has no meaning, and inventing
// a percentage (0 or 100) would be fabricating a figure this package never
// measured (G12b).
type PixelDiff struct {
	RefWidth  int
	RefHeight int
	Width     int
	Height    int

	// DimensionMismatch is true when reference and capture do not share the
	// same width and height — in which case DiffPixels/TotalPixels/DiffPct
	// are left at their zero value and must never be read as "0% different".
	DimensionMismatch bool

	DiffPixels  int
	TotalPixels int
	DiffPct     float64
}

// ComparePNG compares reference against capture, both raw PNG bytes (D7) —
// PURE over bytes, no filesystem access of any kind: the caller (the
// service layer, P8) is what reads the two files from repoDir-derived paths
// this package never sees, keeping this leaf's stdlib-only posture intact
// (V10).
//
// Order of operations, and why it is fixed (G12c/U-E): png.DecodeConfig
// (header only) runs FIRST for both images, checked against
// MaxComparePixels, before png.Decode ever touches pixel data for either
// one — so a hostile reference OR a hostile capture is caught identically,
// and neither image is ever ​fully decoded​ merely to measure the other.
func ComparePNG(reference, capture []byte) (PixelDiff, error) {
	refCfg, err := png.DecodeConfig(bytes.NewReader(reference))
	if err != nil {
		return PixelDiff{}, fmt.Errorf("quality: reference: %s: %w", err, ErrNotPNG)
	}
	if refCfg.Width*refCfg.Height > MaxComparePixels {
		return PixelDiff{}, fmt.Errorf(
			"quality: reference dimensions %dx%d exceed MaxComparePixels (%d): %w",
			refCfg.Width, refCfg.Height, MaxComparePixels, ErrImageTooLarge)
	}

	capCfg, err := png.DecodeConfig(bytes.NewReader(capture))
	if err != nil {
		return PixelDiff{}, fmt.Errorf("quality: capture: %s: %w", err, ErrNotPNG)
	}
	if capCfg.Width*capCfg.Height > MaxComparePixels {
		return PixelDiff{}, fmt.Errorf(
			"quality: capture dimensions %dx%d exceed MaxComparePixels (%d): %w",
			capCfg.Width, capCfg.Height, MaxComparePixels, ErrImageTooLarge)
	}

	if refCfg.Width != capCfg.Width || refCfg.Height != capCfg.Height {
		// G12b: a dimension mismatch is `fail` WITHOUT a percentage — there
		// is no pixel-to-pixel correspondence to compute, and fabricating a
		// 0% or 100% figure here would be inventing evidence this package
		// never measured.
		return PixelDiff{
			RefWidth: refCfg.Width, RefHeight: refCfg.Height,
			Width: capCfg.Width, Height: capCfg.Height,
			DimensionMismatch: true,
		}, nil
	}

	refImg, err := png.Decode(bytes.NewReader(reference))
	if err != nil {
		return PixelDiff{}, fmt.Errorf("quality: reference: %s: %w", err, ErrNotPNG)
	}
	capImg, err := png.Decode(bytes.NewReader(capture))
	if err != nil {
		return PixelDiff{}, fmt.Errorf("quality: capture: %s: %w", err, ErrNotPNG)
	}

	diffPixels, totalPixels := countDiffPixels(refImg, capImg)

	diffPct := 0.0
	if totalPixels > 0 {
		diffPct = float64(diffPixels) / float64(totalPixels) * 100
	}

	return PixelDiff{
		RefWidth: refCfg.Width, RefHeight: refCfg.Height,
		Width: capCfg.Width, Height: capCfg.Height,
		DiffPixels: diffPixels, TotalPixels: totalPixels, DiffPct: diffPct,
	}, nil
}

// ExceedsTolerance reports whether d's DiffPct is STRICTLY GREATER than
// maxDiffPct (D7/G12a) — the boundary is deliberately `>`, never `>=`: with
// the tolerance declared exactly at the measured difference, the
// comparison PASSES. Meaningless (always false) when d.DimensionMismatch is
// true — that case is judged by DimensionMismatch itself, never by this
// method, since DiffPct carries no information in that shape.
func (d PixelDiff) ExceedsTolerance(maxDiffPct float64) bool {
	return d.DiffPct > maxDiffPct
}

// countDiffPixels counts, over two SAME-DIMENSION images, how many pixel
// positions have a different color — compared via color.Color's own RGBA()
// (alpha-premultiplied 16-bit channels), which is the stdlib's own notion of
// "the same color" regardless of which concrete image.Image type either
// decoded into (NRGBA, RGBA, Paletted, Gray, ...).
func countDiffPixels(ref, cap image.Image) (diff, total int) {
	bounds := ref.Bounds()
	capBounds := cap.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			total++
			r1, g1, b1, a1 := ref.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			r2, g2, b2, a2 := cap.At(capBounds.Min.X+x, capBounds.Min.Y+y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				diff++
			}
		}
	}
	return diff, total
}
