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
	"testing"
)

func assertErr(t *testing.T, got error, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("expected error %v, got %v", want, got)
	}
}

func TestEmptyImage(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	empty := image.NewRGBA(image.Rectangle{})

	if _, err := a.FindBestCropWithSettings(empty, DefaultSettings()); err != ErrEmptyImage {
		t.Fatalf("expected ErrEmptyImage, got %v", err)
	}
}

func TestInvalidDimensions(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))

	for _, s := range []Settings{
		DefaultSettings(),
		func() Settings {
			s := DefaultSettings()
			s.TargetWidth = -1
			return s
		}(),
		func() Settings {
			s := DefaultSettings()
			s.TargetHeight = -5
			return s
		}(),
	} {
		if _, err := a.FindBestCropWithSettings(img, s); err != ErrInvalidDimensions {
			t.Fatalf("expected ErrInvalidDimensions for %+v, got %v", s, err)
		}
	}

	// Legacy entry point keeps returning the shared sentinel error.
	if _, err := NewAnalyzer(testResizer()).FindBestCrop(img, 0, 0); err != ErrInvalidDimensions {
		t.Fatalf("expected ErrInvalidDimensions, got %v", err)
	}
}

func TestTargetTooLarge(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))

	s := DefaultSettings()
	s.TargetWidth, s.TargetHeight = 41, 40
	if _, err := a.FindBestCropWithSettings(img, s); err != ErrTargetTooLarge {
		t.Fatalf("expected ErrTargetTooLarge, got %v", err)
	}

	s = DefaultSettings()
	s.TargetWidth, s.TargetHeight = 40, 41
	if _, err := a.FindBestCropWithSettings(img, s); err != ErrTargetTooLarge {
		t.Fatalf("expected ErrTargetTooLarge, got %v", err)
	}
}

func TestZeroSizeAnalysis(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))

	s := DefaultSettings()
	s.TargetWidth, s.TargetHeight = 10, 10
	// 40 * 0.001 chops to zero analysis pixels.
	s.AnalysisScale = 0.001
	if _, err := a.FindBestCropWithSettings(img, s); err != ErrZeroSizeAnalysis {
		t.Fatalf("expected ErrZeroSizeAnalysis, got %v", err)
	}
}

func TestSingleRowNoCandidate(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	// A 40x1 image cannot host a 10px tall window: no candidate may be
	// returned and no uninitialized rectangle must escape.
	img := image.NewRGBA(image.Rect(0, 0, 40, 1))

	s := DefaultSettings()
	s.TargetWidth, s.TargetHeight = 10, 10
	rect, err := a.FindBestCropWithSettings(img, s)
	if err != ErrTargetTooLarge {
		t.Fatalf("expected ErrTargetTooLarge for over-tall target, got %v", err)
	}
	if rect != (image.Rectangle{}) {
		t.Fatalf("error path must return a zero rectangle, got %v", rect)
	}
}

func TestSingleColumnNoCandidate(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	img := image.NewRGBA(image.Rect(0, 0, 1, 40))

	s := DefaultSettings()
	s.TargetWidth, s.TargetHeight = 10, 10
	rect, err := a.FindBestCropWithSettings(img, s)
	if err != ErrTargetTooLarge {
		t.Fatalf("expected ErrTargetTooLarge, got %v", err)
	}
	if rect != (image.Rectangle{}) {
		t.Fatalf("error path must return a zero rectangle, got %v", rect)
	}
}

func TestOnePixelFeasibleCrop(t *testing.T) {
	// A feasible single-row request must still produce a legal 1px window
	// instead of an error or a NaN score.
	a := NewSettingsAnalyzer(testResizer())
	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	img.SetRGBA(2, 0, color.RGBA{R: 200, G: 100, B: 50, A: 255})

	s := DefaultSettings()
	s.TargetWidth, s.TargetHeight = 1, 1
	rect, err := a.FindBestCropWithSettings(img, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rect.Dx() != 1 || rect.Dy() != 1 {
		t.Fatalf("expected a 1x1 crop, got %v", rect)
	}
	if !rect.In(img.Bounds()) {
		t.Fatalf("crop %v outside bounds %v", rect, img.Bounds())
	}
}

// TestNoCropCandidateReturnsStableError drives the candidate generator into a
// zero-candidate configuration and asserts the analyzer returns
// ErrNoCropCandidate plus a zero rectangle, never an uninitialized Crop.
func TestNoCropCandidateReturnsStableError(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))

	s := DefaultSettings()
	s.TargetWidth, s.TargetHeight = 8, 8
	s.Prescale = false
	// Every candidate scale is capped so low that the scaled window still
	// exceeds the image, leaving the candidate list empty.
	s.MaxScale = 0.01
	s.MinScale = 0.005
	s.ScaleStep = 0.001

	rect, err := a.FindBestCropWithSettings(img, s)
	if err != ErrNoCropCandidate {
		t.Fatalf("expected ErrNoCropCandidate, got %v", err)
	}
	if rect != (image.Rectangle{}) {
		t.Fatalf("expected a zero rectangle on the no-candidate path, got %v", rect)
	}

	// The generator itself reports the same stable error contract.
	if cs := crops(s, img, 8, 8, s.MinScale); len(cs) != 0 {
		t.Fatalf("expected no candidates, got %d", len(cs))
	}
}

// TestErrorRectanglesAreZero pins that every error path returns the zero
// value rather than a partially computed rectangle.
func TestErrorRectanglesAreZero(t *testing.T) {
	a := NewSettingsAnalyzer(testResizer())
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))

	calls := []func() (image.Rectangle, error){
		func() (image.Rectangle, error) {
			return a.FindBestCropWithSettings(img, DefaultSettings())
		},
		func() (image.Rectangle, error) {
			s := DefaultSettings()
			s.TargetWidth, s.TargetHeight = 100, 100
			return a.FindBestCropWithSettings(img, s)
		},
		func() (image.Rectangle, error) {
			s := DefaultSettings()
			s.TargetWidth, s.TargetHeight = 10, 10
			s.AnalysisScale = 0.001
			return a.FindBestCropWithSettings(img, s)
		},
	}
	for i, call := range calls {
		rect, err := call()
		if err == nil {
			t.Fatalf("call %d: expected an error", i)
		}
		if rect != (image.Rectangle{}) {
			t.Fatalf("call %d: error path returned non-zero rectangle %v", i, rect)
		}
	}
}
