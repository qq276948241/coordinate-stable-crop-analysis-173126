/*
 * Copyright (c) 2014-2017 Christian Muehlhaeuser
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package smartcrop

import (
	"image"
	"math"
	"testing"
)

// settingsFor builds target settings and keeps prescale explicit so a test
// failure states which path it exercised.
func settingsFor(w, h int, prescale bool) Settings {
	s := DefaultSettings()
	s.TargetWidth = w
	s.TargetHeight = h
	s.Prescale = prescale
	return s
}

func assertLegalRect(t *testing.T, rect, b image.Rectangle) {
	t.Helper()
	if rect.Dx() <= 0 || rect.Dy() <= 0 {
		t.Fatalf("crop has non-positive area: %v", rect)
	}
	if !rect.In(b) {
		t.Fatalf("crop %v exceeds source bounds %v", rect, b)
	}
}

// TestRectInsideBoundsForAllFixtures exercises every fixture on both code
// paths: returned rectangles must be legal and never escape the source bounds.
func TestRectInsideBoundsForAllFixtures(t *testing.T) {
	fixtures := []string{
		"solid_black.png",
		"solid_white.png",
		"transparent.png",
		"gradient.png",
		"skin_blob.png",
		"edge_band.png",
		"subject_on_edge.png",
		"wide_320x40.png",
		"tall_40x320.png",
		"tie_grid.png",
	}
	a := NewSettingsAnalyzer(testResizer())

	for _, name := range fixtures {
		img := loadFixture(t, name)
		b := img.Bounds()
		targetW, targetH := b.Dx()/4, b.Dy()/4
		if targetW < 1 {
			targetW = 1
		}
		if targetH < 1 {
			targetH = 1
		}

		for _, prescale := range []bool{false, true} {
			rect, err := a.FindBestCropWithSettings(img, settingsFor(targetW, targetH, prescale))
			if err != nil {
				t.Fatalf("%s prescale=%v: unexpected error %v", name, prescale, err)
			}
			assertLegalRect(t, rect, b)
		}
	}
}

// TestNonZeroOriginPreserved verifies that an image whose bounds do not start
// at (0,0) yields a crop in the image's own coordinate system. Before the fix
// the pre-scaled path returned coordinates relative to (0,0), which escaped
// the source bounds.
func TestNonZeroOriginPreserved(t *testing.T) {
	base := toZeroOriginRGBA(loadFixture(t, "gradient.png"))
	const ox, oy = 37, 51
	shifted := image.NewRGBA(image.Rect(ox, oy, ox+base.Bounds().Dx(), oy+base.Bounds().Dy()))
	for y := base.Bounds().Min.Y; y < base.Bounds().Max.Y; y++ {
		for x := base.Bounds().Min.X; x < base.Bounds().Max.X; x++ {
			shifted.SetRGBA(ox+x, oy+y, base.RGBAAt(x, y))
		}
	}

	a := NewSettingsAnalyzer(testResizer())
	for _, prescale := range []bool{false, true} {
		rect, err := a.FindBestCropWithSettings(shifted, settingsFor(20, 20, prescale))
		if err != nil {
			t.Fatalf("prescale=%v: %v", prescale, err)
		}
		assertLegalRect(t, rect, shifted.Bounds())
		if rect.Min.X < ox || rect.Min.Y < oy {
			t.Fatalf("prescale=%v: origin lost, rect %v starts before (%d,%d)", prescale, rect, ox, oy)
		}
	}
}

// TestPrescaleRoundTripAligned uses an image size that divides evenly by the
// pre-scale factor, so prescaled and non-prescaled analysis must select the
// same window, case by case.
func TestPrescaleRoundTripAligned(t *testing.T) {
	img := loadFixture(t, "skin_blob.png")
	a := NewSettingsAnalyzer(testResizer())

	noPrescale, err := a.FindBestCropWithSettings(img, settingsFor(24, 24, false))
	if err != nil {
		t.Fatal(err)
	}
	withPrescale, err := a.FindBestCropWithSettings(img, settingsFor(24, 24, true))
	if err != nil {
		t.Fatal(err)
	}
	assertLegalRect(t, noPrescale, img.Bounds())
	assertLegalRect(t, withPrescale, img.Bounds())
	if noPrescale != withPrescale {
		t.Fatalf("aligned prescale round-trip mismatch: no-prescale %v, prescale %v",
			noPrescale, withPrescale)
	}
}

// TestPrescaleRoundTripNatural checks the real, non-divisible example image:
// the two paths may differ by at most one source pixel per edge (rounding), but
// both rectangles must remain inside the bounds.
func TestPrescaleRoundTripNatural(t *testing.T) {
	img := loadExampleGopher(t)
	a := NewSettingsAnalyzer(testResizer())

	noPrescale, err := a.FindBestCropWithSettings(img, settingsFor(250, 250, false))
	if err != nil {
		t.Fatal(err)
	}
	withPrescale, err := a.FindBestCropWithSettings(img, settingsFor(250, 250, true))
	if err != nil {
		t.Fatal(err)
	}
	assertLegalRect(t, noPrescale, img.Bounds())
	assertLegalRect(t, withPrescale, img.Bounds())

	for i := range []int{0, 1, 2, 3} {
		got := []int{withPrescale.Min.X, withPrescale.Min.Y, withPrescale.Max.X, withPrescale.Max.Y}[i]
		want := []int{noPrescale.Min.X, noPrescale.Min.Y, noPrescale.Max.X, noPrescale.Max.Y}[i]
		if abs(got-want) > 1 {
			t.Fatalf("prescale drift >1px on edge %d: prescale %v vs no-prescale %v",
				i, withPrescale, noPrescale)
		}
	}
}

// TestExplicitAnalysisScale proves a caller-chosen analysis scale maps back
// inside the source bounds and that scale=1 is equivalent to disabling
// prescaling.
func TestExplicitAnalysisScale(t *testing.T) {
	img := loadFixture(t, "skin_blob.png")
	a := NewSettingsAnalyzer(testResizer())

	s0 := settingsFor(24, 24, false)
	base, err := a.FindBestCropWithSettings(img, s0)
	if err != nil {
		t.Fatal(err)
	}

	s1 := settingsFor(24, 24, true)
	s1.AnalysisScale = 1.0
	oneToOne, err := a.FindBestCropWithSettings(img, s1)
	if err != nil {
		t.Fatal(err)
	}
	if base != oneToOne {
		t.Fatalf("AnalysisScale=1.0 changed the window: %v vs %v", oneToOne, base)
	}

	for _, scale := range []float64{0.5, 0.25} {
		s := settingsFor(24, 24, true)
		s.AnalysisScale = scale
		rect, err := a.FindBestCropWithSettings(img, s)
		if err != nil {
			t.Fatalf("scale %v: %v", scale, err)
		}
		assertLegalRect(t, rect, img.Bounds())
	}
}

// TestExtremeAspectRatios covers very wide and very tall sources and targets.
func TestExtremeAspectRatios(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		targetW int
		targetH int
	}{
		{"wide source, wide target", "wide_320x40.png", 80, 10},
		{"wide source, square target", "wide_320x40.png", 20, 20},
		{"tall source, tall target", "tall_40x320.png", 10, 80},
		{"tall source, square target", "tall_40x320.png", 20, 20},
		{"single row target", "wide_320x40.png", 40, 1},
		{"single column target", "tall_40x320.png", 1, 40},
	}
	a := NewSettingsAnalyzer(testResizer())

	for _, tc := range cases {
		img := loadFixture(t, tc.fixture)
		for _, prescale := range []bool{false, true} {
			rect, err := a.FindBestCropWithSettings(img, settingsFor(tc.targetW, tc.targetH, prescale))
			if err != nil {
				t.Fatalf("%s prescale=%v: %v", tc.name, prescale, err)
			}
			assertLegalRect(t, rect, img.Bounds())
		}
	}
}

// TestAspectRatioDerived proves the requested aspect ratio is recoverable from
// the request when only one target dimension is supplied. The derived side
// follows the chop (floor) rounding rule; the final crop fills as much of the
// source as the candidate scales allow.
func TestAspectRatioDerived(t *testing.T) {
	img := loadFixture(t, "wide_320x40.png")
	a := NewSettingsAnalyzer(testResizer())

	// Height given, width derived from the 320:40 = 8:1 source ratio.
	s := DefaultSettings()
	s.Prescale = false
	s.TargetHeight = 10
	resolvedW, resolvedH, err := resolveTarget(s, img.Bounds())
	if err != nil {
		t.Fatal(err)
	}
	if resolvedH != 10 || resolvedW != 80 {
		t.Fatalf("derived target = %dx%d, want 80x10", resolvedW, resolvedH)
	}

	rect, err := a.FindBestCropWithSettings(img, s)
	if err != nil {
		t.Fatal(err)
	}
	assertLegalRect(t, rect, img.Bounds())
	// The returned window must preserve the 8:1 target ratio within one pixel
	// of chop rounding.
	ratio := float64(rect.Dx()) / float64(rect.Dy())
	if math.Abs(ratio-8.0) > 0.25 {
		t.Fatalf("crop ratio %v deviates from target 8:1 (rect %v)", ratio, rect)
	}
}

// TestOriginAndStrideRecoverable reconstructs the analysis window and pixel
// stride from a returned rectangle, proving source origin, scale and target
// ratio round-trip.
func TestOriginAndStrideRecoverable(t *testing.T) {
	base := toZeroOriginRGBA(loadFixture(t, "gradient.png"))
	const ox, oy = 13, 7
	shifted := image.NewRGBA(image.Rect(ox, oy, ox+base.Bounds().Dx(), oy+base.Bounds().Dy()))
	for y := 0; y < base.Bounds().Dy(); y++ {
		for x := 0; x < base.Bounds().Dx(); x++ {
			shifted.SetRGBA(ox+x, oy+y, base.RGBAAt(x, y))
		}
	}

	a := NewSettingsAnalyzer(testResizer())
	s := settingsFor(16, 16, true)
	s.AnalysisScale = 0.5
	rect, err := a.FindBestCropWithSettings(shifted, s)
	if err != nil {
		t.Fatal(err)
	}

	// Origin: every returned coordinate must be expressible as source origin
	// plus a non-negative, in-bounds offset.
	if rect.Min.X < ox || rect.Min.Y < oy {
		t.Fatalf("source origin not recoverable: %v", rect)
	}
	local := image.Rect(rect.Min.X-ox, rect.Min.Y-oy, rect.Max.X-ox, rect.Max.Y-oy)
	if !local.In(image.Rect(0, 0, base.Bounds().Dx(), base.Bounds().Dy())) {
		t.Fatalf("local window %v outside zero-origin source", local)
	}

	// Stride: pixel index math on the caller's RGBA buffer using the returned
	// rectangle and the real PixStride must address every covered pixel.
	idx := 0
	stride := shifted.Stride
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			off := (y-shifted.Rect.Min.Y)*stride + (x-shifted.Rect.Min.X)*4
			if off < 0 || off+3 >= len(shifted.Pix) {
				t.Fatalf("stride arithmetic out of range at (%d,%d): offset %d", x, y, off)
			}
			_ = shifted.Pix[off]
			idx++
		}
	}
	if idx != rect.Dx()*rect.Dy() {
		t.Fatalf("addressed %d pixels, expected %d", idx, rect.Dx()*rect.Dy())
	}
}

// TestMapToSourceContainsAnalysisWindow verifies the inverse mapping at the
// unit level: for a range of scales and windows, the source rectangle must
// stay in bounds and contain the analysis window once mapped back.
func TestMapToSourceContainsAnalysisWindow(t *testing.T) {
	srcBounds := image.Rect(0, 0, 100, 60)
	cases := []struct {
		scaleX, scaleY float64
		crop           image.Rectangle
	}{
		{1.0, 1.0, image.Rect(0, 0, 25, 25)},
		{0.5, 0.5, image.Rect(10, 5, 40, 25)},
		{0.37, 0.61, image.Rect(3, 7, 33, 29)},
		{0.5, 0.5, image.Rect(0, 0, 50, 30)},
	}
	for _, tc := range cases {
		mapped := mapToSource(tc.crop, srcBounds, tc.scaleX, tc.scaleY)
		if !mapped.In(srcBounds) {
			t.Fatalf("mapped %v outside %v (scales %v/%v)", mapped, srcBounds, tc.scaleX, tc.scaleY)
		}
		if mapped.Dx() <= 0 || mapped.Dy() <= 0 {
			t.Fatalf("mapped window has non-positive area: %v", mapped)
		}

		// Every analysis corner, when scaled back, must fall inside mapped.
		corners := []image.Point{
			tc.crop.Min,
			{tc.crop.Max.X - 1, tc.crop.Min.Y},
			{tc.crop.Min.X, tc.crop.Max.Y - 1},
			{tc.crop.Max.X - 1, tc.crop.Max.Y - 1},
		}
		for _, p := range corners {
			sx := srcBounds.Min.X + int(math.Floor(float64(p.X)/tc.scaleX))
			sy := srcBounds.Min.Y + int(math.Floor(float64(p.Y)/tc.scaleY))
			if !image.Pt(sx, sy).In(mapped) {
				t.Fatalf("analysis pixel %v -> source (%d,%d) escapes mapped %v", p, sx, sy, mapped)
			}
		}
	}
}

// TestNonUniformScaleStaysInBounds exercises a real resizer on an extreme
// aspect ratio and asserts the mapped rectangle never escapes the source.
func TestNonUniformScaleStaysInBounds(t *testing.T) {
	img := loadFixture(t, "wide_320x40.png")
	a := NewSettingsAnalyzer(testResizer())
	s := settingsFor(60, 8, true)
	s.AnalysisScale = 0.31 // forces independent rounding of width and height

	rect, err := a.FindBestCropWithSettings(img, s)
	if err != nil {
		t.Fatal(err)
	}
	assertLegalRect(t, rect, img.Bounds())
}

func TestLegacyFindBestCropIsPrescaleCompatible(t *testing.T) {
	img := loadExampleGopher(t)
	rect, err := NewAnalyzer(testResizer()).FindBestCrop(img, 250, 250)
	if err != nil {
		t.Fatal(err)
	}
	expected := image.Rect(464, 24, 719, 279)
	if rect != expected {
		t.Fatalf("legacy behavior changed: expected %v, got %v", expected, rect)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
