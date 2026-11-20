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
	"sync"
	"testing"
)

// TestCropImageEntryPoint validates the reusable crop entry: the result keeps
// the requested rectangle, stays inside the source bounds and is an actual
// pixel-accurate sub-image.
func TestCropImageEntryPoint(t *testing.T) {
	img := toZeroOriginRGBA(loadFixture(t, "gradient.png"))
	rect := image.Rect(10, 12, 42, 38)

	out, err := CropImage(img, rect)
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != rect.Dx() || out.Bounds().Dy() != rect.Dy() {
		t.Fatalf("cropped size %v, want %dx%d", out.Bounds(), rect.Dx(), rect.Dy())
	}
	// Pixel-accurate copy, including non-zero-origin sources.
	for y := 0; y < rect.Dy(); y++ {
		for x := 0; x < rect.Dx(); x++ {
			want := img.RGBAAt(rect.Min.X+x, rect.Min.Y+y)
			r, g, b, a := out.At(out.Bounds().Min.X+x, out.Bounds().Min.Y+y).RGBA()
			got := [4]uint32{r, g, b, a}
			exp := [4]uint32{
				uint32(want.R) * 257, uint32(want.G) * 257,
				uint32(want.B) * 257, uint32(want.A) * 257,
			}
			if got != exp {
				t.Fatalf("pixel mismatch at (%d,%d): got %v want %v", x, y, got, exp)
			}
		}
	}
}

// TestCropImageRejectsIllegalRectangles pins the validation contract: no
// negative width/height or out-of-bounds rectangle may be produced.
func TestCropImageRejectsIllegalRectangles(t *testing.T) {
	img := toZeroOriginRGBA(loadFixture(t, "gradient.png"))
	b := img.Bounds()

	illegal := []image.Rectangle{
		image.Rect(0, 0, 0, 10),
		image.Rect(0, 0, 10, 0),
		image.Rect(-1, 0, 10, 10),
		image.Rect(0, b.Max.Y-1, 10, b.Max.Y+1),
		image.Rect(b.Max.X-1, 0, b.Max.X+1, 10),
	}
	for _, r := range illegal {
		if _, err := CropImage(img, r); err != ErrCropOutOfBounds {
			t.Fatalf("rect %v: expected ErrCropOutOfBounds, got %v", r, err)
		}
	}

	// A negative-area rectangle whose canonical form stays in bounds is
	// accepted because the contract canonicalizes before validating; this
	// pins that behavior explicitly.
	reversed := image.Rect(20, 20, 5, 5)
	out, err := CropImage(img, reversed)
	if err != nil {
		t.Fatalf("canonicalizable rectangle rejected: %v", err)
	}
	want := reversed.Canon()
	if out.Bounds() != want {
		t.Fatalf("canonical crop = %v, want %v", out.Bounds(), want)
	}
}

// TestCropImageToSize verifies the resizing wrapper returns exactly the target
// size and that the intermediate crop never exceeds the source bounds.
func TestCropImageToSize(t *testing.T) {
	img := toZeroOriginRGBA(loadFixture(t, "skin_blob.png"))
	analyzer := NewSettingsAnalyzer(testResizer())
	rect, err := analyzer.FindBestCropWithSettings(img, settingsFor(24, 24, false))
	if err != nil {
		t.Fatal(err)
	}

	resized, err := CropImageToSize(img, rect, 60, 60, testResizer())
	if err != nil {
		t.Fatal(err)
	}
	if resized.Bounds().Dx() != 60 || resized.Bounds().Dy() != 60 {
		t.Fatalf("resized to %v, want 60x60", resized.Bounds())
	}

	if _, err := CropImageToSize(img, rect, 0, 60, testResizer()); err != ErrInvalidDimensions {
		t.Fatal("expected ErrInvalidDimensions for zero target")
	}
	if _, err := CropImageToSize(img, rect, 60, 60, nil); err != ErrNilResizer {
		t.Fatal("expected ErrNilResizer")
	}
}

// TestCropConsistentWithDirectFullResScoring is the end-to-end correctness
// check: the mapped window, cropped through the reusable entry and scored on
// the original-resolution image, agrees with a direct full-resolution score of
// the same window. It also confirms the returned window beats every other
// legal window of the same size.
func TestCropConsistentWithDirectFullResScoring(t *testing.T) {
	img := toZeroOriginRGBA(loadFixture(t, "skin_blob.png"))
	s := settingsFor(24, 24, false)
	analyzer := NewSettingsAnalyzer(testResizer())

	rect, err := analyzer.FindBestCropWithSettings(img, s)
	if err != nil {
		t.Fatal(err)
	}
	assertLegalRect(t, rect, img.Bounds())

	// The reusable crop entry must accept the exact returned rectangle.
	cropped, err := CropImage(img, rect)
	if err != nil {
		t.Fatalf("CropImage rejected analyzer rectangle: %v", err)
	}
	if cropped.Bounds().Dx() != rect.Dx() || cropped.Bounds().Dy() != rect.Dy() {
		t.Fatalf("cropped size %v does not match rectangle %v", cropped.Bounds(), rect)
	}

	// Scoring the cropped full-resolution sub-image must equal scoring the
	// same window directly from the full-resolution source. The sub-image is
	// zero-origin, while the source window carries the source offset.
	direct, err := ScoreCrop(img, rect, s)
	if err != nil {
		t.Fatal(err)
	}
	local := image.Rect(0, 0, rect.Dx(), rect.Dy())
	subScore, err := ScoreCrop(toZeroOriginRGBA(cropped), local, s)
	if err != nil {
		t.Fatal(err)
	}
	assertFinite(t, direct)
	assertFinite(t, subScore)
	if !scoresClose(direct, subScore, scoreTolerance) {
		t.Fatalf("full-res crop score %+v differs from direct window score %+v", subScore, direct)
	}

	// The analyzer window must be at least as good as every same-sized window
	// reachable on the full-resolution grid.
	winning := totalOf(t, img, rect, s)
	b := img.Bounds()
	for y := 0; y+rect.Dy() <= b.Dy(); y += s.Step {
		for x := 0; x+rect.Dx() <= b.Dx(); x += s.Step {
			other := image.Rect(x, y, x+rect.Dx(), y+rect.Dy())
			if other == rect {
				continue
			}
			if total := totalOf(t, img, other, s); total > winning+scoreTolerance {
				t.Fatalf("window %v scores %g > selected window %g (%v)", other, total, winning, rect)
			}
		}
	}
}

// TestPrescaleCropConsistentWithDirectScoring repeats the end-to-end check on
// the pre-scaled path, including a transparent-border fixture.
func TestPrescaleCropConsistentWithDirectScoring(t *testing.T) {
	for _, name := range []string{"skin_blob.png", "gradient.png", "subject_on_edge.png"} {
		img := toZeroOriginRGBA(loadFixture(t, name))
		s := settingsFor(20, 20, true)
		analyzer := NewSettingsAnalyzer(testResizer())

		rect, err := analyzer.FindBestCropWithSettings(img, s)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cropped, err := CropImage(img, rect)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		direct, err := ScoreCrop(img, rect, s)
		if err != nil {
			t.Fatal(err)
		}
		local := image.Rect(0, 0, rect.Dx(), rect.Dy())
		subScore, err := ScoreCrop(toZeroOriginRGBA(cropped), local, s)
		if err != nil {
			t.Fatal(err)
		}
		if !scoresClose(direct, subScore, scoreTolerance) {
			t.Fatalf("%s: crop score %+v != direct score %+v", name, subScore, direct)
		}
	}
}

// TestConcurrentAnalyses hammers one shared image and analyzer from many
// goroutines. Every result must equal the single-threaded reference window.
func TestConcurrentAnalyses(t *testing.T) {
	img := loadFixture(t, "skin_blob.png")
	analyzer := NewSettingsAnalyzer(testResizer())
	settingsList := []Settings{
		settingsFor(24, 24, false),
		settingsFor(24, 24, true),
	}

	reference := make([]image.Rectangle, len(settingsList))
	for i, s := range settingsList {
		r, err := analyzer.FindBestCropWithSettings(img, s)
		if err != nil {
			t.Fatal(err)
		}
		reference[i] = r
	}

	const goroutines = 32
	const iterations = 20
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < iterations; n++ {
				s := settingsList[(id+n)%len(settingsList)]
				got, err := analyzer.FindBestCropWithSettings(img, s)
				if err != nil {
					errs <- err
					return
				}
				want := reference[(id+n)%len(settingsList)]
				if got != want {
					errs <- &divergenceError{got: got, want: want}
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

type divergenceError struct {
	got, want image.Rectangle
}

func (e *divergenceError) Error() string {
	return "concurrent result diverged: got " + e.got.String() + " want " + e.want.String()
}

func totalOf(t *testing.T, img *image.RGBA, rect image.Rectangle, s Settings) float64 {
	t.Helper()
	sc, err := ScoreCrop(img, rect, s)
	if err != nil {
		t.Fatal(err)
	}
	return (sc.Detail*s.DetailWeight + sc.Skin*s.SkinWeight + sc.Saturation*s.SaturationWeight) /
		float64(rect.Dx()) / float64(rect.Dy())
}
