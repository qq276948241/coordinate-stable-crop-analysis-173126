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
	"io/ioutil"
	"log"
	"math"
	"sync"
	"testing"

	"github.com/muesli/smartcrop/nfnt"
)

func testAnalyzer() OptionsAnalyzer {
	return NewAnalyzerWithOptions(nfnt.NewDefaultResizer(), DefaultOptions())
}

func noPrescaleOptions() Options {
	o := DefaultOptions()
	o.Prescale = false
	return o
}

func rectCenter(r image.Rectangle) (float64, float64) {
	return float64(r.Min.X+r.Max.X) / 2.0, float64(r.Min.Y+r.Max.Y) / 2.0
}

func centerDistance(a, b image.Rectangle) float64 {
	ax, ay := rectCenter(a)
	bx, by := rectCenter(b)
	return math.Hypot(ax-bx, ay-by)
}

// detectionImage runs the three detection channels on img, like analyse does.
func detectionImage(img *image.RGBA) *image.RGBA {
	o := image.NewRGBA(img.Bounds())
	EdgeDetect(img, o)
	SkinDetect(img, o)
	SaturationDetect(img, o)
	return o
}

// ---------------------------------------------------------------------------
// input validation: stable errors, never negative sizes or out-of-bounds rects
// ---------------------------------------------------------------------------

func TestInvalidInputs(t *testing.T) {
	analyzer := testAnalyzer()

	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	singleRow := image.NewRGBA(image.Rect(0, 0, 16, 1))
	singleCol := image.NewRGBA(image.Rect(0, 0, 1, 16))
	normal := noiseImage(64, 64, 1)

	cases := []struct {
		name   string
		img    image.Image
		width  int
		height int
		err    error
	}{
		{"nil image", nil, 10, 10, ErrEmptyImage},
		{"empty image", empty, 10, 10, ErrEmptyImage},
		{"single row", singleRow, 4, 1, ErrImageTooSmall},
		{"single column", singleCol, 1, 4, ErrImageTooSmall},
		{"zero dimensions", normal, 0, 0, ErrInvalidDimensions},
		{"negative width", normal, -5, 10, ErrInvalidDimensions},
		{"negative height", normal, 10, -5, ErrInvalidDimensions},
		{"width exceeds image", normal, 65, 10, ErrCropTooLarge},
		{"height exceeds image", normal, 10, 65, ErrCropTooLarge},
		{"both exceed image", normal, 1000, 1000, ErrCropTooLarge},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := analyzer.FindBestCrop(c.img, c.width, c.height)
			if err != c.err {
				t.Fatalf("expected error %v, got %v", c.err, err)
			}
			if r != (image.Rectangle{}) {
				t.Fatalf("expected zero rectangle on error, got %v", r)
			}
		})
	}
}

func TestPrescaleToZeroFails(t *testing.T) {
	img := noiseImage(1000, 290, 3)

	// prescale width rounds down to 0
	o := DefaultOptions()
	o.PrescaleMin = 0.2
	if _, err := testAnalyzer().FindBestCropWithOptions(img, 100, 29, o); err != ErrPrescaleFailed {
		t.Fatalf("expected ErrPrescaleFailed, got %v", err)
	}

	// prescale width is 1, height would round to 0
	o.PrescaleMin = 0.435
	if _, err := testAnalyzer().FindBestCropWithOptions(img, 100, 29, o); err != ErrPrescaleFailed {
		t.Fatalf("expected ErrPrescaleFailed, got %v", err)
	}

	// PrescaleMin <= 0 disables prescaling entirely: no error
	o.PrescaleMin = 0
	r, err := testAnalyzer().FindBestCropWithOptions(img, 100, 29, o)
	if err != nil {
		t.Fatalf("unexpected error with prescaling disabled: %v", err)
	}
	if r.Empty() {
		t.Fatal("expected non-empty crop with prescaling disabled")
	}
}

// ---------------------------------------------------------------------------
// coordinate mapping: prescale round-trip, extreme ratios, non-zero origin
// ---------------------------------------------------------------------------

func TestPrescaleRoundTrip(t *testing.T) {
	// 2000x480 engages prescaling (min dimension 480 > 400)
	img := blobImage(2000, 480, 1500, 240, 40)
	analyzer := testAnalyzer()

	rOn, err := analyzer.FindBestCropWithOptions(img, 200, 200, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	rOff, err := analyzer.FindBestCropWithOptions(img, 200, 200, noPrescaleOptions())
	if err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]image.Rectangle{"prescale": rOn, "full": rOff} {
		if r.Empty() {
			t.Fatalf("%s: empty crop", name)
		}
		if !r.In(img.Bounds()) {
			t.Fatalf("%s: crop %v escapes image bounds %v", name, r, img.Bounds())
		}
		// the best window must contain the subject
		if !image.Pt(1500, 240).In(r) {
			t.Fatalf("%s: crop %v does not contain the subject at (1500,240)", name, r)
		}
	}

	// prescaling must not shift the window significantly
	if d := centerDistance(rOn, rOff); d > 40 {
		t.Fatalf("prescale round-trip shifted the window by %v px (on %v, off %v)", d, rOn, rOff)
	}
}

// TestPrescaleMappingRoundTrip pins the coordinate transform: the window found
// in analysis space must be mapped back to the original image with the actual
// per-axis prescale factors (not the theoretical factor), chop rounding,
// clamping to the image bounds, and re-application of the bounds origin.
func TestPrescaleMappingRoundTrip(t *testing.T) {
	cases := []struct {
		w, h   int
		tw, th int
	}{
		{2000, 480, 200, 200},
		{4000, 500, 100, 100},
		{900, 284, 250, 250},
		{3999, 501, 150, 75}, // odd sizes: resizer rounding diverges per axis
	}

	resizer := nfnt.NewDefaultResizer()
	logger := Logger{Log: log.New(ioutil.Discard, "", 0)}
	opts := DefaultOptions()

	for _, c := range cases {
		img := blobImage(c.w, c.h, c.w*3/4, c.h/2, c.h/12+10)
		b := img.Bounds()

		// replicate the documented pipeline step by step
		scale := math.Min(float64(b.Dx())/float64(c.tw), float64(b.Dy())/float64(c.th))
		prescalefactor := 1.0
		if f := opts.PrescaleMin / math.Min(float64(b.Dx()), float64(b.Dy())); f < 1.0 {
			prescalefactor = f
		}
		w := uint(float64(b.Dx()) * prescalefactor)
		low := toRGBA(resizer.Resize(img, w, 0))
		fx := float64(low.Bounds().Dx()) / float64(b.Dx())
		fy := float64(low.Bounds().Dy()) / float64(b.Dy())

		cropW := chop(float64(c.tw) * scale * fx)
		cropH := chop(float64(c.th) * scale * fy)
		realMinScale := math.Min(opts.MaxScale, math.Max(1.0/scale, opts.MinScale))

		aRect, err := analyse(logger, low, cropW, cropH, realMinScale, opts)
		if err != nil {
			t.Fatal(err)
		}
		want := image.Rect(
			int(chop(float64(aRect.Min.X)/fx)), int(chop(float64(aRect.Min.Y)/fy)),
			int(chop(float64(aRect.Max.X)/fx)), int(chop(float64(aRect.Max.Y)/fy)),
		).Canon().Intersect(image.Rect(0, 0, b.Dx(), b.Dy())).Add(b.Min)

		got, err := testAnalyzer().FindBestCropWithOptions(img, c.tw, c.th, opts)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%dx%d target %dx%d: mapped window %v, want %v (analysis window %v, fx=%v fy=%v)",
				c.w, c.h, c.tw, c.th, got, want, aRect, fx, fy)
		}
		if !got.In(b) {
			t.Fatalf("%dx%d: mapped window %v escapes bounds %v", c.w, c.h, got, b)
		}
	}
}

func TestExtremeAspectRatio(t *testing.T) {
	// extreme 8:1 aspect ratio with prescaling engaged, subject at the edge
	img := blobImage(4000, 500, 3900, 250, 60)
	analyzer := testAnalyzer()

	rOn, err := analyzer.FindBestCropWithOptions(img, 100, 100, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	rOff, err := analyzer.FindBestCropWithOptions(img, 100, 100, noPrescaleOptions())
	if err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]image.Rectangle{"prescale": rOn, "full": rOff} {
		if r.Empty() || !r.In(img.Bounds()) {
			t.Fatalf("%s: invalid crop %v for bounds %v", name, r, img.Bounds())
		}
		if !image.Pt(3900, 250).In(r) {
			t.Fatalf("%s: crop %v does not contain the subject at (3900,250)", name, r)
		}
	}
	if d := centerDistance(rOn, rOff); d > 80 {
		t.Fatalf("extreme aspect: prescale shifted window by %v (on %v, off %v)", d, rOn, rOff)
	}
}

func TestNonZeroOriginAndStride(t *testing.T) {
	// parent image, sub-image with non-zero origin and parent stride
	parent := blobImage(1200, 900, 700, 400, 50)
	sub := parent.SubImage(image.Rect(100, 80, 1100, 780)).(*image.RGBA)
	if sub.Bounds().Min != image.Pt(100, 80) {
		t.Fatalf("unexpected sub-image bounds %v", sub.Bounds())
	}

	// zero-origin copy with identical pixels
	zero := image.NewRGBA(image.Rect(0, 0, 1000, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 1000; x++ {
			zero.SetRGBA(x, y, sub.RGBAAt(100+x, 80+y))
		}
	}

	analyzer := testAnalyzer()
	for _, opts := range []Options{DefaultOptions(), noPrescaleOptions()} {
		rSub, err := analyzer.FindBestCropWithOptions(sub, 100, 100, opts)
		if err != nil {
			t.Fatal(err)
		}
		rZero, err := analyzer.FindBestCropWithOptions(zero, 100, 100, opts)
		if err != nil {
			t.Fatal(err)
		}

		// the origin of the input must be recoverable from the result
		if want := rZero.Add(sub.Bounds().Min); rSub != want {
			t.Fatalf("prescale=%v: non-zero origin result %v, want %v", opts.Prescale, rSub, want)
		}
		if !rSub.In(sub.Bounds()) {
			t.Fatalf("prescale=%v: crop %v escapes sub-image bounds %v", opts.Prescale, rSub, sub.Bounds())
		}
	}
}

func TestCallerBufferUntouched(t *testing.T) {
	img := noiseImage(120, 90, 11)
	pix := make([]uint8, len(img.Pix))
	copy(pix, img.Pix)
	stride, bounds := img.Stride, img.Bounds()

	analyzer := testAnalyzer()
	for _, opts := range []Options{DefaultOptions(), noPrescaleOptions()} {
		if _, err := analyzer.FindBestCropWithOptions(img, 32, 32, opts); err != nil {
			t.Fatal(err)
		}
	}

	if img.Stride != stride || img.Bounds() != bounds {
		t.Fatal("image metadata changed")
	}
	for i := range pix {
		if pix[i] != img.Pix[i] {
			t.Fatalf("caller buffer modified at byte %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// determinism: repeated runs, concurrency, tied candidates
// ---------------------------------------------------------------------------

func TestDeterministicConcurrent(t *testing.T) {
	img := noiseImage(300, 200, 5)
	// add a feature so the winner is not arbitrary
	for y := 60; y < 100; y++ {
		for x := 140; x < 180; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}

	analyzer := testAnalyzer()
	for _, opts := range []Options{DefaultOptions(), noPrescaleOptions()} {
		want, err := analyzer.FindBestCropWithOptions(img, 64, 64, opts)
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		results := make([]image.Rectangle, 8*5)
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 5; i++ {
					r, err := analyzer.FindBestCropWithOptions(img, 64, 64, opts)
					if err != nil {
						t.Error(err)
						return
					}
					results[g*5+i] = r
				}
			}(g)
		}
		wg.Wait()

		for i, r := range results {
			if r != want {
				t.Fatalf("prescale=%v run %d: got %v, want %v", opts.Prescale, i, r, want)
			}
		}
	}
}

func TestTiedCandidatesStable(t *testing.T) {
	// solid color: every candidate scores exactly 0, so the winner is decided
	// purely by deterministic enumeration order (largest scale, then lowest
	// coordinates first)
	img := solidImage(200, 100, color.RGBA{128, 128, 128, 255})
	want := image.Rect(0, 0, 100, 100)

	analyzer := testAnalyzer()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := analyzer.FindBestCropWithOptions(img, 50, 50, noPrescaleOptions())
			if err != nil {
				t.Error(err)
				return
			}
			if r != want {
				t.Errorf("tied candidates: got %v, want %v", r, want)
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// score channels: independent per-pixel verification
// ---------------------------------------------------------------------------

func TestDetectionChannelsAgainstReference(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	// sharp vertical edge: left black, right white
	for y := 0; y < 24; y++ {
		for x := 0; x < 24; x++ {
			if x < 12 {
				img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	img.SetRGBA(5, 5, color.RGBA{199, 145, 112, 255}) // skin tone
	img.SetRGBA(18, 18, color.RGBA{255, 0, 0, 255})   // saturated red

	w, h := 24, 24

	edge := image.NewRGBA(img.Bounds())
	EdgeDetect(img, edge)
	refEdge := refEdgeImage(img)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if got := edge.RGBAAt(x, y).G; got != refEdge[y*w+x] {
				t.Fatalf("edge channel at (%d,%d): got %d, want %d", x, y, got, refEdge[y*w+x])
			}
		}
	}

	skin := image.NewRGBA(img.Bounds())
	SkinDetect(img, skin)
	refSkin := refSkinImage(img)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if got := skin.RGBAAt(x, y).R; got != refSkin[y*w+x] {
				t.Fatalf("skin channel at (%d,%d): got %d, want %d", x, y, got, refSkin[y*w+x])
			}
		}
	}

	sat := image.NewRGBA(img.Bounds())
	SaturationDetect(img, sat)
	refSat := refSaturationImage(img)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if got := sat.RGBAAt(x, y).B; got != refSat[y*w+x] {
				t.Fatalf("saturation channel at (%d,%d): got %d, want %d", x, y, got, refSat[y*w+x])
			}
		}
	}
}

func TestImportanceAgainstReference(t *testing.T) {
	crops := []image.Rectangle{
		image.Rect(0, 0, 100, 100),
		image.Rect(7, 13, 107, 63),
		image.Rect(0, 0, 1, 1),
	}
	for _, c := range crops {
		for y := c.Min.Y - 4; y < c.Max.Y+4; y += 3 {
			for x := c.Min.X - 4; x < c.Max.X+4; x += 3 {
				got := Importance(c, x, y)
				want := refImportance(c, x, y)
				if got != want {
					t.Fatalf("importance(%v, %d, %d): got %v, want %v", c, x, y, got, want)
				}
				if math.IsNaN(got) {
					t.Fatalf("importance(%v, %d, %d) is NaN", c, x, y)
				}
			}
		}
	}
}

func TestScoreRegionAgainstReference(t *testing.T) {
	img := noiseImage(48, 40, 9)
	det := detectionImage(img)

	for _, r := range []image.Rectangle{
		image.Rect(0, 0, 32, 32),
		image.Rect(8, 8, 40, 24),
		image.Rect(0, 0, 48, 40),
	} {
		got := ScoreRegion(det, r)
		want := refScoreRegion(det, r)
		if got != want {
			t.Fatalf("score of %v: got %+v, want %+v", r, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// NaN safety on degenerate images
// ---------------------------------------------------------------------------

func TestDegenerateImagesNoNaN(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 64, 64)) // all alpha 0

	imgs := map[string]image.Image{
		"black":       solidImage(64, 64, color.RGBA{0, 0, 0, 255}),
		"white":       solidImage(64, 64, color.RGBA{255, 255, 255, 255}),
		"gray":        solidImage(64, 64, color.RGBA{128, 128, 128, 255}),
		"transparent": transparent,
		"corner blob": blobImage(64, 64, 0, 0, 20),
	}

	analyzer := testAnalyzer()
	opts := DefaultOptions()
	for name, img := range imgs {
		for _, o := range []Options{DefaultOptions(), noPrescaleOptions()} {
			r, err := analyzer.FindBestCropWithOptions(img, 32, 32, o)
			if err != nil {
				t.Fatalf("%s prescale=%v: %v", name, o.Prescale, err)
			}
			if r.Empty() {
				t.Fatalf("%s prescale=%v: empty crop", name, o.Prescale)
			}
			if !r.In(img.Bounds()) {
				t.Fatalf("%s prescale=%v: crop %v escapes bounds %v", name, o.Prescale, r, img.Bounds())
			}

			det := detectionImage(toRGBA(img))
			s := ScoreRegion(det, r)
			for ch, v := range map[string]float64{"skin": s.Skin, "detail": s.Detail, "saturation": s.Saturation} {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("%s prescale=%v: %s score is %v", name, o.Prescale, ch, v)
				}
			}
			if ts := s.TotalScore(r, opts); math.IsNaN(ts) || math.IsInf(ts, 0) {
				t.Fatalf("%s prescale=%v: total score is %v", name, o.Prescale, ts)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// parity with the historical prescale-disabled code path
// ---------------------------------------------------------------------------

func TestOldPathParity(t *testing.T) {
	fixtures := map[string]*image.RGBA{
		"gradient":   gradientImage(120, 90),
		"noise":      noiseImage(120, 90, 7),
		"blob":       blobImage(150, 100, 90, 40, 20),
		"solid":      solidImage(80, 60, color.RGBA{77, 150, 20, 255}),
		"black":      solidImage(64, 64, color.RGBA{0, 0, 0, 255}),
		"white":      solidImage(64, 64, color.RGBA{255, 255, 255, 255}),
		"wide":       gradientImage(200, 30),
		"tall":       gradientImage(30, 200),
		"small":      noiseImage(40, 30, 13),
		"zero-area":  gradientImage(100, 20), // triggers zero-area candidates in the old path with target width 1
		"skinDetail": skinDetailImage(),
	}
	targets := [][2]int{{20, 20}, {16, 9}, {0, 20}, {25, 0}, {1, 15}, {37, 13}, {64, 64}}

	analyzer := testAnalyzer()
	opts := noPrescaleOptions()

	for fname, img := range fixtures {
		for _, tgt := range targets {
			w, h := tgt[0], tgt[1]
			if (w > 0 && w > img.Bounds().Dx()) || (h > 0 && h > img.Bounds().Dy()) {
				continue
			}
			want := oldFindBestCropNoPrescale(img, w, h)
			got, err := analyzer.FindBestCropWithOptions(img, w, h, opts)
			if err != nil {
				t.Fatalf("%s %dx%d: %v", fname, w, h, err)
			}
			if got != want {
				t.Fatalf("%s target %dx%d: got %v, old path %v", fname, w, h, got, want)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// caller-supplied scoring parameters
// ---------------------------------------------------------------------------

func TestCallerSuppliedWeights(t *testing.T) {
	img := skinDetailImage()
	analyzer := testAnalyzer()

	skinOpts := noPrescaleOptions()
	skinOpts.DetailWeight = 0
	skinOpts.SaturationWeight = 0
	skinOpts.SkinWeight = 50

	detailOpts := noPrescaleOptions()
	detailOpts.DetailWeight = 50
	detailOpts.SaturationWeight = 0
	detailOpts.SkinWeight = 0

	rSkin, err := analyzer.FindBestCropWithOptions(img, 100, 100, skinOpts)
	if err != nil {
		t.Fatal(err)
	}
	rDetail, err := analyzer.FindBestCropWithOptions(img, 100, 100, detailOpts)
	if err != nil {
		t.Fatal(err)
	}

	if rSkin == rDetail {
		t.Fatalf("caller-supplied weights had no effect: both %v", rSkin)
	}
	if cx, _ := rectCenter(rSkin); cx >= 200 {
		t.Fatalf("skin-weighted crop %v not over the skin region", rSkin)
	}
	if cx, _ := rectCenter(rDetail); cx <= 200 {
		t.Fatalf("detail-weighted crop %v not over the checkerboard", rDetail)
	}
}

func TestConfigurableAnalysisScale(t *testing.T) {
	img := blobImage(2000, 480, 1500, 240, 40)
	analyzer := testAnalyzer()

	// smaller analysis scale than the default 400
	o := DefaultOptions()
	o.PrescaleMin = 100
	rSmall, err := analyzer.FindBestCropWithOptions(img, 200, 200, o)
	if err != nil {
		t.Fatal(err)
	}
	rDefault, err := analyzer.FindBestCropWithOptions(img, 200, 200, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !rSmall.In(img.Bounds()) || rSmall.Empty() {
		t.Fatalf("invalid crop %v", rSmall)
	}
	// different analysis scales must still find the same subject
	for name, r := range map[string]image.Rectangle{"small": rSmall, "default": rDefault} {
		if !image.Pt(1500, 240).In(r) {
			t.Fatalf("%s analysis scale: crop %v does not contain the subject", name, r)
		}
	}

	// NewAnalyzerWithOptions must honor its options in FindBestCrop
	np := NewAnalyzerWithOptions(nfnt.NewDefaultResizer(), noPrescaleOptions())
	r1, err := np.FindBestCrop(img, 200, 200)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := analyzer.FindBestCropWithOptions(img, 200, 200, noPrescaleOptions())
	if err != nil {
		t.Fatal(err)
	}
	if r1 != r2 {
		t.Fatalf("NewAnalyzerWithOptions: FindBestCrop %v != FindBestCropWithOptions %v", r1, r2)
	}
}

// ---------------------------------------------------------------------------
// reusable crop entry: CropImage + FitAspect
// ---------------------------------------------------------------------------

func TestCropImage(t *testing.T) {
	img := noiseImage(100, 80, 17)

	out, err := CropImage(img, image.Rect(10, 10, 50, 40))
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds() != image.Rect(0, 0, 40, 30) {
		t.Fatalf("unexpected crop bounds %v", out.Bounds())
	}
	// pixel-exact copy of the source region
	cropped := out.(*image.RGBA)
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			if cropped.RGBAAt(x, y) != img.RGBAAt(10+x, 10+y) {
				t.Fatalf("pixel (%d,%d) mismatch", x, y)
			}
		}
	}
	// the output must not alias the caller's buffer
	cropped.SetRGBA(0, 0, color.RGBA{1, 2, 3, 255})
	if img.RGBAAt(10, 10) == (color.RGBA{1, 2, 3, 255}) {
		t.Fatal("CropImage output aliases the source image")
	}

	// partially out of bounds: intersected
	out2, err := CropImage(img, image.Rect(90, 70, 200, 200))
	if err != nil {
		t.Fatal(err)
	}
	if out2.Bounds() != image.Rect(0, 0, 10, 10) {
		t.Fatalf("unexpected intersected bounds %v", out2.Bounds())
	}

	// fully out of bounds / empty / nil
	if _, err := CropImage(img, image.Rect(200, 200, 300, 300)); err != ErrInvalidCrop {
		t.Fatalf("expected ErrInvalidCrop, got %v", err)
	}
	if _, err := CropImage(img, image.Rect(5, 5, 5, 5)); err != ErrInvalidCrop {
		t.Fatalf("expected ErrInvalidCrop, got %v", err)
	}
	if _, err := CropImage(nil, image.Rect(0, 0, 10, 10)); err != ErrEmptyImage {
		t.Fatalf("expected ErrEmptyImage, got %v", err)
	}
}

func TestFitAspect(t *testing.T) {
	bounds := image.Rect(0, 0, 500, 400)
	cases := []struct {
		name string
		r    image.Rectangle
		w, h int
	}{
		{"already exact", image.Rect(0, 0, 255, 255), 250, 250},
		{"too wide", image.Rect(10, 10, 266, 112), 250, 100},
		{"too tall", image.Rect(10, 10, 110, 262), 250, 100},
		{"wide ratio", image.Rect(0, 0, 300, 200), 16, 9},
		{"tall ratio", image.Rect(0, 0, 200, 300), 9, 16},
		{"at edge", image.Rect(400, 300, 503, 399), 100, 50},
		{"no ratio", image.Rect(10, 10, 110, 60), 0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FitAspect(c.r, c.w, c.h, bounds)
			if got.Empty() {
				t.Fatal("empty result")
			}
			if !got.In(bounds) {
				t.Fatalf("result %v escapes bounds %v", got, bounds)
			}
			// only shrinking is allowed
			if !got.In(c.r.Canon().Intersect(bounds).Union(got)) || got.Dx() > c.r.Dx() || got.Dy() > c.r.Dy() {
				t.Fatalf("result %v grew beyond input %v", got, c.r)
			}
			if c.w > 0 && c.h > 0 && got.Dx()*c.h != got.Dy()*c.w {
				t.Fatalf("aspect ratio not exact: %v for %d:%d", got, c.w, c.h)
			}
			if c.w <= 0 || c.h <= 0 {
				want := c.r.Canon().Intersect(bounds)
				if got != want {
					t.Fatalf("no-ratio case: got %v, want %v", got, want)
				}
			}
			// center preserved within one pixel
			icx, icy := rectCenter(c.r.Canon().Intersect(bounds))
			gcx, gcy := rectCenter(got)
			if math.Abs(icx-gcx) > 1 || math.Abs(icy-gcy) > 1 {
				t.Fatalf("center moved from (%v,%v) to (%v,%v)", icx, icy, gcx, gcy)
			}
		})
	}
}

// TestCropEntryConsistency verifies that the reusable crop entry agrees with
// direct scoring on the original-size image: the cropped pixels are identical
// to the source region, the detection channels computed on the crop match the
// full-image channels in the interior, and the window's score on the
// original-size detection image matches an independent reference.
func TestCropEntryConsistency(t *testing.T) {
	img := blobImage(160, 120, 100, 60, 25)
	analyzer := testAnalyzer()

	r, err := analyzer.FindBestCropWithOptions(img, 64, 48, noPrescaleOptions())
	if err != nil {
		t.Fatal(err)
	}
	rf := FitAspect(r, 64, 48, img.Bounds())
	if rf.Dx()*48 != rf.Dy()*64 {
		t.Fatalf("FitAspect did not settle the target ratio: %v", rf)
	}
	if !rf.In(img.Bounds()) {
		t.Fatalf("fitted rect %v escapes bounds", rf)
	}

	croppedImg, err := CropImage(img, rf)
	if err != nil {
		t.Fatal(err)
	}
	cropped := croppedImg.(*image.RGBA)

	// pixels identical to the source region
	for y := 0; y < rf.Dy(); y++ {
		for x := 0; x < rf.Dx(); x++ {
			if cropped.RGBAAt(x, y) != img.RGBAAt(rf.Min.X+x, rf.Min.Y+y) {
				t.Fatalf("cropped pixel (%d,%d) differs from source", x, y)
			}
		}
	}

	// detection channels computed on the crop match the full-image channels
	// in the interior (border pixels legitimately differ: the Laplacian is
	// zeroed on image borders)
	full := detectionImage(toRGBA(img))
	part := detectionImage(cropped)
	for y := 1; y < rf.Dy()-1; y++ {
		for x := 1; x < rf.Dx()-1; x++ {
			if part.RGBAAt(x, y) != full.RGBAAt(rf.Min.X+x, rf.Min.Y+y) {
				t.Fatalf("detection channel mismatch at crop (%d,%d)", x, y)
			}
		}
	}

	// score of the window on the original-size detection image matches the
	// independent per-pixel reference
	got := ScoreRegion(full, rf)
	want := refScoreRegion(full, rf)
	if got != want {
		t.Fatalf("score mismatch: got %+v, want %+v", got, want)
	}
}
