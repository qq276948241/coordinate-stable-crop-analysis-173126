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

// This file contains offline pixel fixtures, an independent per-pixel
// reference implementation of the detection channels, and a faithful copy of
// the historical (pre-refactor) analysis path with prescaling disabled. The
// reference implementations exist only to verify the production code; they
// must not call into it.

import (
	"image"
	"image/color"
	"math"
	"math/rand"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func gradientImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				uint8(x * 255 / w),
				uint8(y * 255 / h),
				uint8((x + y) * 255 / (w + h)),
				255,
			})
		}
	}
	return img
}

func noiseImage(w, h int, seed int64) *image.RGBA {
	rnd := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				uint8(rnd.Intn(256)),
				uint8(rnd.Intn(256)),
				uint8(rnd.Intn(256)),
				255,
			})
		}
	}
	return img
}

// blobImage draws a filled white circle on a black background: a strong,
// localized detail/brightness feature at a known position.
func blobImage(w, h, cx, cy, r int) *image.RGBA {
	img := solidImage(w, h, color.RGBA{0, 0, 0, 255})
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	return img
}

// skinDetailImage is a 400x200 gray image with a skin-tone rectangle on the
// left and a high-frequency checkerboard (strong edges) on the right.
func skinDetailImage() *image.RGBA {
	img := solidImage(400, 200, color.RGBA{128, 128, 128, 255})
	for y := 40; y < 160; y++ {
		for x := 40; x < 160; x++ {
			img.SetRGBA(x, y, color.RGBA{199, 145, 112, 255}) // skin tone
		}
	}
	for y := 40; y < 160; y++ {
		for x := 240; x < 360; x++ {
			// checkerboard with 4px cells in the right half
			if ((x/4)+(y/4))%2 == 0 {
				img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	return img
}

// ---------------------------------------------------------------------------
// independent per-pixel reference of the detection channels
// ---------------------------------------------------------------------------

func refCie(c color.RGBA) float64 {
	return 0.5126*float64(c.B) + 0.7152*float64(c.G) + 0.0722*float64(c.R)
}

func refClamp255(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// refEdgeImage computes the edge channel independently: 4-neighbor Laplacian
// of the CIE lightness, zero on the border, clamped to [0,255].
func refEdgeImage(img *image.RGBA) []uint8 {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x == 0 || x >= w-1 || y == 0 || y >= h-1 {
				out[y*w+x] = 0
				continue
			}
			l := refCie(img.RGBAAt(x, y))*4.0 -
				refCie(img.RGBAAt(x, y-1)) -
				refCie(img.RGBAAt(x-1, y)) -
				refCie(img.RGBAAt(x+1, y)) -
				refCie(img.RGBAAt(x, y+1))
			out[y*w+x] = refClamp255(l)
		}
	}
	return out
}

func refSkinCol(c color.RGBA) float64 {
	r8, g8, b8 := float64(c.R), float64(c.G), float64(c.B)
	mag := math.Sqrt(r8*r8 + g8*g8 + b8*b8)
	if mag == 0 {
		return 0
	}
	rd := r8/mag - 0.78
	gd := g8/mag - 0.57
	bd := b8/mag - 0.44
	return 1.0 - math.Sqrt(rd*rd+gd*gd+bd*bd)
}

// refSkinImage computes the skin channel independently.
func refSkinImage(img *image.RGBA) []uint8 {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			lightness := refCie(c) / 255.0
			skin := refSkinCol(c)
			if skin > 0.8 && lightness >= 0.2 && lightness <= 1.0 {
				out[y*w+x] = refClamp255((skin - 0.8) * (255.0 / (1.0 - 0.8)))
			}
		}
	}
	return out
}

func refSaturation(c color.RGBA) float64 {
	cMax, cMin := c.R, c.G
	if c.G > cMax {
		cMax = c.G
	}
	if c.B > cMax {
		cMax = c.B
	}
	if c.R < cMin {
		cMin = c.R
	}
	if c.B < cMin {
		cMin = c.B
	}
	if c.G < cMin {
		cMin = c.G
	}
	if cMax == cMin {
		return 0
	}
	maximum := float64(cMax) / 255.0
	minimum := float64(cMin) / 255.0
	l := (maximum + minimum) / 2.0
	d := maximum - minimum
	if l > 0.5 {
		return d / (2.0 - maximum - minimum)
	}
	return d / (maximum + minimum)
}

// refSaturationImage computes the saturation channel independently.
func refSaturationImage(img *image.RGBA) []uint8 {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			lightness := refCie(c) / 255.0
			sat := refSaturation(c)
			if sat > 0.4 && lightness >= 0.05 && lightness <= 0.9 {
				out[y*w+x] = refClamp255((sat - 0.4) * (255.0 / (1.0 - 0.4)))
			}
		}
	}
	return out
}

func refThirds(x float64) float64 {
	x = (math.Mod(x-(1.0/3.0)+1.0, 2.0)*0.5 - 0.5) * 16.0
	return math.Max(1.0-x*x, 0.0)
}

// refImportance is an independent implementation of the default importance
// function (edge distance + rule of thirds).
func refImportance(crop image.Rectangle, x, y int) float64 {
	if crop.Min.X > x || x >= crop.Max.X || crop.Min.Y > y || y >= crop.Max.Y {
		return -0.5
	}
	xf := float64(x-crop.Min.X) / float64(crop.Dx())
	yf := float64(y-crop.Min.Y) / float64(crop.Dy())
	px := math.Abs(0.5-xf) * 2.0
	py := math.Abs(0.5-yf) * 2.0
	dx := math.Max(px-1.0+0.4, 0.0)
	dy := math.Max(py-1.0+0.4, 0.0)
	d := (dx*dx + dy*dy) * -20.0
	s := 1.41 - math.Sqrt(px*px+py*py)
	s += (math.Max(0.0, s+d+0.5) * 1.2) * (refThirds(px) + refThirds(py))
	return s + d
}

// refScoreRegion accumulates the score of a window over a detection image
// using the reference importance and the default downsample of 8.
func refScoreRegion(output *image.RGBA, crop image.Rectangle) Score {
	w, h := output.Bounds().Dx(), output.Bounds().Dy()
	var s Score
	for y := 0; y <= h-8; y += 8 {
		for x := 0; x <= w-8; x += 8 {
			c := output.RGBAAt(x, y)
			imp := refImportance(crop, x, y)
			det := float64(c.G) / 255.0
			s.Skin += float64(c.R) / 255.0 * (det + 0.01) * imp
			s.Detail += det * imp
			s.Saturation += float64(c.B) / 255.0 * (det + 0.2) * imp
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// faithful copy of the historical analysis path (prescale disabled)
// ---------------------------------------------------------------------------

func oldSkinCol(c color.RGBA) float64 {
	r8, g8, b8 := float64(c.R), float64(c.G), float64(c.B)

	mag := math.Sqrt(r8*r8 + g8*g8 + b8*b8)
	rd := r8/mag - skinColor[0]
	gd := g8/mag - skinColor[1]
	bd := b8/mag - skinColor[2]

	d := math.Sqrt(rd*rd + gd*gd + bd*bd)
	return 1.0 - d
}

func oldImportance(crop image.Rectangle, x, y int) float64 {
	if crop.Min.X > x || x >= crop.Max.X || crop.Min.Y > y || y >= crop.Max.Y {
		return -0.5
	}

	xf := float64(x-crop.Min.X) / float64(crop.Dx())
	yf := float64(y-crop.Min.Y) / float64(crop.Dy())

	px := math.Abs(0.5-xf) * 2.0
	py := math.Abs(0.5-yf) * 2.0

	dx := math.Max(px-1.0+0.4, 0.0)
	dy := math.Max(py-1.0+0.4, 0.0)
	d := (dx*dx + dy*dy) * -20.0

	s := 1.41 - math.Sqrt(px*px+py*py)
	s += (math.Max(0.0, s+d+0.5) * 1.2) * (refThirds(px) + refThirds(py))

	return s + d
}

func oldEdgeDetect(i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()
	cies := make([]float64, width*height)
	n := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			cies[n] = cie(i.RGBAAt(x, y))
			n++
		}
	}

	var lightness float64
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x == 0 || x >= width-1 || y == 0 || y >= height-1 {
				lightness = 0
			} else {
				lightness = cies[y*width+x]*4.0 -
					cies[x+(y-1)*width] -
					cies[x-1+y*width] -
					cies[x+1+y*width] -
					cies[x+(y+1)*width]
			}
			o.SetRGBA(x, y, color.RGBA{0, uint8(bounds(lightness)), 0, 255})
		}
	}
}

func oldSkinDetect(i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			lightness := cie(i.RGBAAt(x, y)) / 255.0
			skin := oldSkinCol(i.RGBAAt(x, y))

			c := o.RGBAAt(x, y)
			if skin > 0.8 && lightness >= 0.2 && lightness <= 1.0 {
				r := (skin - 0.8) * (255.0 / (1.0 - 0.8))
				o.SetRGBA(x, y, color.RGBA{uint8(bounds(r)), c.G, c.B, 255})
			} else {
				o.SetRGBA(x, y, color.RGBA{0, c.G, c.B, 255})
			}
		}
	}
}

func oldSaturationDetect(i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			lightness := cie(i.RGBAAt(x, y)) / 255.0
			sat := saturation(i.RGBAAt(x, y))

			c := o.RGBAAt(x, y)
			if sat > 0.4 && lightness >= 0.05 && lightness <= 0.9 {
				b := (sat - 0.4) * (255.0 / (1.0 - 0.4))
				o.SetRGBA(x, y, color.RGBA{c.R, c.G, uint8(bounds(b)), 255})
			} else {
				o.SetRGBA(x, y, color.RGBA{c.R, c.G, 0, 255})
			}
		}
	}
}

func oldScore(output *image.RGBA, crop image.Rectangle) Score {
	width := output.Bounds().Dx()
	height := output.Bounds().Dy()
	score := Score{}

	for y := 0; y <= height-8; y += 8 {
		for x := 0; x <= width-8; x += 8 {
			c := output.RGBAAt(x, y)
			r8 := float64(c.R)
			g8 := float64(c.G)
			b8 := float64(c.B)

			imp := oldImportance(crop, x, y)
			det := g8 / 255.0

			score.Skin += r8 / 255.0 * (det + 0.01) * imp
			score.Detail += det * imp
			score.Saturation += b8 / 255.0 * (det + 0.2) * imp
		}
	}

	return score
}

func oldTotalScore(s Score, r image.Rectangle) float64 {
	return (s.Detail*0.2 + s.Skin*1.8 + s.Saturation*0.3) / float64(r.Dx()) / float64(r.Dy())
}

func oldCrops(width, height int, cropWidth, cropHeight, realMinScale float64) []image.Rectangle {
	res := []image.Rectangle{}
	minDimension := math.Min(float64(width), float64(height))
	var cropW, cropH float64

	if cropWidth != 0.0 {
		cropW = cropWidth
	} else {
		cropW = minDimension
	}
	if cropHeight != 0.0 {
		cropH = cropHeight
	} else {
		cropH = minDimension
	}

	for scale := 1.0; scale >= realMinScale; scale -= 0.1 {
		for y := 0; float64(y)+cropH*scale <= float64(height); y += 8 {
			for x := 0; float64(x)+cropW*scale <= float64(width); x += 8 {
				res = append(res, image.Rect(x, y, x+int(cropW*scale), y+int(cropH*scale)))
			}
		}
	}

	return res
}

// oldFindBestCropNoPrescale replicates the historical FindBestCrop code path
// with the prescale feature disabled, including its lack of candidate
// filtering. Fixtures are zero-origin *image.RGBA, for which the historical
// toRGBA returned the input unchanged.
func oldFindBestCropNoPrescale(img *image.RGBA, width, height int) image.Rectangle {
	scale := math.Min(float64(img.Bounds().Dx())/float64(width), float64(img.Bounds().Dy())/float64(height))
	lowimg := img

	cropWidth, cropHeight := chop(float64(width)*scale*1.0), chop(float64(height)*scale*1.0)
	realMinScale := math.Min(1.0, math.Max(1.0/scale, 0.9))

	o := image.NewRGBA(lowimg.Bounds())
	oldEdgeDetect(lowimg, o)
	oldSkinDetect(lowimg, o)
	oldSaturationDetect(lowimg, o)

	var topCrop image.Rectangle
	topScore := -1.0
	for _, r := range oldCrops(o.Bounds().Dx(), o.Bounds().Dy(), cropWidth, cropHeight, realMinScale) {
		s := oldScore(o, r)
		if ts := oldTotalScore(s, r); ts > topScore {
			topCrop = r
			topScore = ts
		}
	}

	return topCrop.Canon()
}
