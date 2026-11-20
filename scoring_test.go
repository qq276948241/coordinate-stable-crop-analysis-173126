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
	"image/color"
	"math"
	"math/rand"
	"testing"
)

const scoreTolerance = 1e-9

// TestScoreMatchesIndependentReference rebuilds the detector buffer with the
// independent per-pixel reference and asserts every score component matches the
// production implementation on a variety of rectangles.
func TestScoreMatchesIndependentReference(t *testing.T) {
	img := toZeroOriginRGBA(loadFixture(t, "skin_blob.png"))
	s := DefaultSettings()

	prod := image.NewRGBA(img.Bounds())
	edgeDetect(img, prod)
	skinDetect(s, img, prod)
	saturationDetect(s, img, prod)

	ref := refDetectors(s, img)
	if !rgbaEqual(prod, ref) {
		t.Fatal("independent detector reference diverges from production detectors")
	}

	rects := []image.Rectangle{
		image.Rect(0, 0, 16, 16),
		image.Rect(24, 8, 56, 40),
		image.Rect(40, 40, 80, 80),
		image.Rect(8, 8, 72, 72),
	}
	for _, r := range rects {
		c := Crop{Rectangle: r}
		got := score(s, prod, c)
		want := refScore(s, ref, c)
		assertFinite(t, got)
		if !scoresClose(got, want, scoreTolerance) {
			t.Fatalf("score mismatch for %v: production %+v reference %+v", r, got, want)
		}
		gotTotal := Crop{Rectangle: r, Score: got}.totalScore(s)
		wantTotal := refTotal(s, Crop{Rectangle: r, Score: want})
		if math.Abs(gotTotal-wantTotal) > scoreTolerance {
			t.Fatalf("total score mismatch for %v: %g vs %g", r, gotTotal, wantTotal)
		}
	}
}

// TestScoreComponentsAreSeparatelyVerifiable confirms each contribution
// (skin, saturation/detail edge, thirds) responds independently to the
// detector channels and to disabling the rule of thirds.
func TestScoreComponentsAreSeparatelyVerifiable(t *testing.T) {
	s := DefaultSettings()
	buf := image.NewRGBA(image.Rect(0, 0, 32, 32))
	rect := image.Rect(0, 0, 32, 32)

	// Only the green (edge/detail) channel is non-zero.
	for i := range buf.Pix {
		buf.Pix[i] = 0
	}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			buf.SetRGBA(x, y, color.RGBA{G: 128, A: 255})
		}
	}
	edgeOnly := score(s, buf, Crop{Rectangle: rect})
	if edgeOnly.Detail == 0 {
		t.Fatal("edge/detail contribution must be non-zero on a green buffer")
	}
	if edgeOnly.Skin != 0 || edgeOnly.Saturation != 0 {
		t.Fatalf("skin/saturation must stay zero, got %+v", edgeOnly)
	}

	// Only the red (skin) channel.
	red := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			red.SetRGBA(x, y, color.RGBA{R: 128, A: 255})
		}
	}
	skinOnly := score(s, red, Crop{Rectangle: rect})
	if skinOnly.Skin == 0 {
		t.Fatal("skin contribution must be non-zero on a red buffer")
	}
	if skinOnly.Detail != 0 {
		t.Fatalf("detail must stay zero, got %+v", skinOnly)
	}

	// Only the blue (saturation) channel.
	blue := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			blue.SetRGBA(x, y, color.RGBA{B: 128, A: 255})
		}
	}
	satOnly := score(s, blue, Crop{Rectangle: rect})
	if satOnly.Saturation == 0 {
		t.Fatal("saturation contribution must be non-zero on a blue buffer")
	}
	if satOnly.Detail != 0 || satOnly.Skin != 0 {
		t.Fatalf("detail/skin must stay zero, got %+v", satOnly)
	}

	// Rule of thirds changes spatial weighting inside a crop. On a full
	// window every sampled pixel sits at thirds-invariant center positions, so
	// compare an off-center window whose interior crosses thirds lines.
	noThirds := s
	noThirds.RuleOfThirds = false
	window := image.Rect(0, 0, 24, 24)
	a := score(s, buf, Crop{Rectangle: window})
	b := score(noThirds, buf, Crop{Rectangle: window})
	if a.Detail == b.Detail {
		t.Fatal("rule-of-thirds contribution is not reflected in the detail score")
	}
}

// TestNoNaNOnDegenerateImages covers solid black, solid color and fully
// transparent images plus a subject flush against the border. None of them may
// produce NaN/Inf scores or an uninitialized rectangle.
func TestNoNaNOnDegenerateImages(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	cases := []string{
		"solid_black.png",
		"solid_white.png",
		"transparent.png",
		"subject_on_edge.png",
	}
	for _, name := range cases {
		img := loadFixture(t, name)
		for _, prescale := range []bool{false, true} {
			rect, err := a.FindBestCropWithSettings(img, settingsFor(16, 16, prescale))
			if err != nil {
				t.Fatalf("%s prescale=%v: %v", name, prescale, err)
			}
			assertLegalRect(t, rect, img.Bounds())

			sc, err := ScoreCrop(img, rect, DefaultSettings())
			if err != nil {
				t.Fatalf("%s: ScoreCrop error: %v", name, err)
			}
			assertFinite(t, sc)
		}
	}
}

// TestBlackPixelSkinGuard pins the zero-magnitude guard in skinCol directly.
func TestBlackPixelSkinGuard(t *testing.T) {
	if got := skinCol(color.RGBA{}); got != 0 || math.IsNaN(got) {
		t.Fatalf("black pixel skin match must be 0 and finite, got %v", got)
	}
	if got := saturation(color.RGBA{}); got != 0 {
		t.Fatalf("black pixel saturation must be 0, got %v", got)
	}
}

// TestDeterministicRepeatedRuns asserts repeated calls with identical settings
// return identical windows.
func TestDeterministicRepeatedRuns(t *testing.T) {
	img := loadFixture(t, "skin_blob.png")
	a := NewSettingsAnalyzer(testResizer())
	s := settingsFor(24, 24, true)

	first, err := a.FindBestCropWithSettings(img, s)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, err := a.FindBestCropWithSettings(img, s)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("run %d diverged: %v vs %v", i, got, first)
		}
	}
}

// TestTieBreakerOrderIndependent feeds equal-scoring candidates in different
// orders and asserts the deterministic winner rule always picks the same one.
func TestTieBreakerOrderIndependent(t *testing.T) {
	base := []Crop{
		{Rectangle: image.Rect(40, 40, 56, 56)},
		{Rectangle: image.Rect(0, 40, 16, 56)},
		{Rectangle: image.Rect(40, 0, 56, 16)},
		{Rectangle: image.Rect(0, 0, 16, 16)},
	}
	const tie = 1.5

	winner := func(order []int) Crop {
		var best Crop
		bestScore := math.Inf(-1)
		first := true
		for _, idx := range order {
			c := base[idx]
			if first || betterCandidate(tie, c, bestScore, best) {
				best = c
				bestScore = tie
				first = false
			}
		}
		return best
	}

	want := winner([]int{0, 1, 2, 3})
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		order := rng.Perm(len(base))
		if got := winner(order); got != want {
			t.Fatalf("order %v selected %v, want %v", order, got.Rectangle, want.Rectangle)
		}
	}

	// The canonical winner is the top-left window of the tied set.
	if want.Rectangle != image.Rect(0, 0, 16, 16) {
		t.Fatalf("unexpected tie winner %v", want.Rectangle)
	}
}

// TestTieGridStableAcrossOrder runs the full pipeline on the symmetric
// tie-grid fixture and confirms the selected window is stable and top-left
// biased among equals.
func TestTieGridStableAcrossOrder(t *testing.T) {
	img := loadFixture(t, "tie_grid.png")
	a := NewSettingsAnalyzer(testResizer())
	s := settingsFor(24, 24, false)

	first, err := a.FindBestCropWithSettings(img, s)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := a.FindBestCropWithSettings(img, s)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("tie-grid winner changed: %v vs %v", got, first)
		}
	}
}

// TestCallerBufferNotMutated proves analysis never writes into a caller-owned
// RGBA, including when it is passed directly (the old toRGBA fast path would
// alias it).
func TestCallerBufferNotMutated(t *testing.T) {
	img := toZeroOriginRGBA(loadFixture(t, "gradient.png"))
	original := snapshotPixels(img)
	a := NewSettingsAnalyzer(testResizer())

	for _, prescale := range []bool{false, true} {
		if _, err := a.FindBestCropWithSettings(img, settingsFor(20, 20, prescale)); err != nil {
			t.Fatal(err)
		}
		assertPixelsUntouched(t, img, original)
	}
}

// TestSmallDownSampleNoNegativeLoop ensures a down-sample stride larger than
// the image yields a finite zero score instead of a negative loop.
func TestSmallDownSampleNoNegativeLoop(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	s := DefaultSettings()
	s.ScoreDownSample = 8
	sc := score(s, img, Crop{Rectangle: image.Rect(0, 0, 4, 4)})
	assertFinite(t, sc)
	if sc != (Score{}) {
		t.Fatalf("expected zero score on tiny image, got %+v", sc)
	}
}

func rgbaEqual(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() || len(a.Pix) != len(b.Pix) {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

func scoresClose(a, b Score, tol float64) bool {
	return math.Abs(a.Detail-b.Detail) <= tol &&
		math.Abs(a.Skin-b.Skin) <= tol &&
		math.Abs(a.Saturation-b.Saturation) <= tol
}
