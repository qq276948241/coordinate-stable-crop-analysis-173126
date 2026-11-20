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
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/muesli/smartcrop/nfnt"
	"github.com/muesli/smartcrop/options"
)

const fixturePath = "testdata/fixtures"

func testResizer() options.Resizer {
	return nfnt.NewDefaultResizer()
}

func loadFixture(t *testing.T, name string) image.Image {
	t.Helper()

	f, err := os.Open(filepath.Join(fixturePath, name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return img
}

// snapshotPixels returns a copy of every RGBA pixel behind img, including
// images with non-zero origins. Tests compare against it to prove the analyzer
// never rewrites caller-owned buffers.
func snapshotPixels(img image.Image) []color.RGBA {
	b := img.Bounds()
	out := make([]color.RGBA, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			out = append(out, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(bl >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return out
}

func assertPixelsUntouched(t *testing.T, img image.Image, want []color.RGBA) {
	t.Helper()
	got := snapshotPixels(img)
	if len(got) != len(want) {
		t.Fatalf("pixel count changed: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("caller buffer mutated at pixel %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func assertFinite(t *testing.T, sc Score) {
	t.Helper()
	for _, v := range []float64{sc.Detail, sc.Saturation, sc.Skin} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("score contains non-finite value: %+v", sc)
		}
	}
}

// ---------------------------------------------------------------------------
// Independent per-pixel reference implementation.
//
// The following functions re-implement the detectors and scoring without
// calling any production helper, so a match validates the production code
// rather than echoing it. They operate on zero-origin RGBA buffers and visit
// every pixel, while the production scorer uses the configurable down-sample
// stride; referenceScore mirrors that stride deliberately.
// ---------------------------------------------------------------------------

func refCIE(c color.RGBA) float64 {
	return 0.5126*float64(c.B) + 0.7152*float64(c.G) + 0.0722*float64(c.R)
}

func refSaturation(c color.RGBA) float64 {
	hi := math.Max(math.Max(float64(c.R), float64(c.G)), float64(c.B))
	lo := math.Min(math.Min(float64(c.R), float64(c.G)), float64(c.B))
	if hi == lo {
		return 0
	}
	maximum := hi / 255.0
	minimum := lo / 255.0
	l := (maximum + minimum) / 2.0
	d := maximum - minimum
	if l > 0.5 {
		return d / (2.0 - maximum - minimum)
	}
	return d / (maximum + minimum)
}

func refSkinCol(c color.RGBA) float64 {
	r8, g8, b8 := float64(c.R), float64(c.G), float64(c.B)
	mag := math.Sqrt(r8*r8 + g8*g8 + b8*b8)
	if mag == 0 {
		return 0
	}
	d := math.Sqrt(
		math.Pow(r8/mag-skinColor[0], 2) +
			math.Pow(g8/mag-skinColor[1], 2) +
			math.Pow(b8/mag-skinColor[2], 2))
	return 1 - d
}

// refDetectors rebuilds the edge/skin/saturation buffer independently.
func refDetectors(s Settings, img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			in := img.RGBAAt(x, y)

			edge := 0.0
			if x > 0 && x < w-1 && y > 0 && y < h-1 {
				edge = refCIE(in)*4 -
					refCIE(img.RGBAAt(x, y-1)) -
					refCIE(img.RGBAAt(x-1, y)) -
					refCIE(img.RGBAAt(x+1, y)) -
					refCIE(img.RGBAAt(x, y+1))
			}

			skin := 0.0
			light := refCIE(in) / 255.0
			if m := refSkinCol(in); m > s.SkinThreshold && light >= 0.2 && light <= 1.0 {
				skin = (m - s.SkinThreshold) * (255.0 / (1.0 - s.SkinThreshold))
			}

			sat := 0.0
			if v := refSaturation(in); v > s.SaturationThreshold && light >= 0.05 && light <= 0.9 {
				sat = (v - s.SaturationThreshold) * (255.0 / (1.0 - s.SaturationThreshold))
			}

			out.SetRGBA(x, y, color.RGBA{
				R: uint8(math.Min(math.Max(skin, 0), 255)),
				G: uint8(math.Min(math.Max(edge, 0), 255)),
				B: uint8(math.Min(math.Max(sat, 0), 255)),
				A: 255,
			})
		}
	}
	return out
}

func refThirds(x float64) float64 {
	x = (math.Mod(x-(1.0/3.0)+1.0, 2.0)*0.5 - 0.5) * 16.0
	return math.Max(1.0-x*x, 0.0)
}

func refImportance(s Settings, c Crop, x, y int) float64 {
	if c.Min.X > x || x >= c.Max.X || c.Min.Y > y || y >= c.Max.Y {
		return s.OutsideImportance
	}
	xf := float64(x-c.Min.X) / float64(c.Dx())
	yf := float64(y-c.Min.Y) / float64(c.Dy())
	px := math.Abs(0.5-xf) * 2.0
	py := math.Abs(0.5-yf) * 2.0

	dx := math.Max(px-1.0+s.EdgeRadius, 0.0)
	dy := math.Max(py-1.0+s.EdgeRadius, 0.0)
	d := (dx*dx + dy*dy) * s.EdgeWeight

	v := 1.41 - math.Sqrt(px*px+py*py)
	if s.RuleOfThirds {
		v += math.Max(0.0, v+d+0.5) * 1.2 * (refThirds(px) + refThirds(py))
	}
	return v + d
}

// refScore walks the detector buffer with the same down-sampling as production
// but using the reference importance and explicit per-channel accumulation.
func refScore(s Settings, buf *image.RGBA, c Crop) Score {
	w, h := buf.Bounds().Dx(), buf.Bounds().Dy()
	var sc Score
	step := s.ScoreDownSample
	for y := 0; y <= h-step; y += step {
		for x := 0; x <= w-step; x += step {
			px := buf.RGBAAt(x, y)
			imp := refImportance(s, c, x, y)
			det := float64(px.G) / 255.0
			sc.Skin += float64(px.R) / 255.0 * (det + s.SkinBias) * imp
			sc.Detail += det * imp
			sc.Saturation += float64(px.B) / 255.0 * (det + s.SaturationBias) * imp
		}
	}
	return sc
}

// refTotal reproduces the area-normalized total-score formula.
func refTotal(s Settings, c Crop) float64 {
	area := float64(c.Dx()) * float64(c.Dy())
	if area == 0 {
		return 0
	}
	return (c.Score.Detail*s.DetailWeight + c.Score.Skin*s.SkinWeight + c.Score.Saturation*s.SaturationWeight) / area
}
