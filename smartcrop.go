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
 *
 *	Authors:
 *		Christian Muehlhaeuser <muesli@gmail.com>
 *		Michael Wendland <michael@michiwend.com>
 *		Bjørn Erik Pedersen <bjorn.erik.pedersen@gmail.com>
 */

/*
Package smartcrop implements a content aware image cropping library based on
Jonas Wagner's smartcrop.js https://github.com/jwagner/smartcrop.js
*/
package smartcrop

import (
	"image"
	"image/color"
	"io/ioutil"
	"log"
	"math"
	"time"

	"github.com/muesli/smartcrop/options"

	"golang.org/x/image/draw"
)

var (
	skinColor = [3]float64{0.78, 0.57, 0.44}

	// floatEpsilon guards the candidate loops against floating point drift at
	// the image border.
	floatEpsilon = 1e-9
)

// Analyzer analyzes an image and returns the best possible crop with the given
// width and height. An error is returned for invalid dimensions or empty
// images.
type Analyzer interface {
	FindBestCrop(img image.Image, width, height int) (image.Rectangle, error)
}

// SettingsAnalyzer is the configurable counterpart of Analyzer. The supplied
// Settings fully determine the result, so repeated and concurrent calls with
// the same image and Settings return identical rectangles.
type SettingsAnalyzer interface {
	FindBestCropWithSettings(img image.Image, s Settings) (image.Rectangle, error)
}

// Score contains the independently verifiable contributions of a candidate
// crop. All fields are plain accumulations and are finite even for solid black,
// solid color or fully transparent images.
type Score struct {
	Detail     float64
	Saturation float64
	Skin       float64
}

// Crop contains a candidate rectangle together with its Score.
type Crop struct {
	image.Rectangle
	Score Score
}

// Logger contains a logger.
type Logger struct {
	DebugMode bool
	Log       *log.Logger
}

type smartcropAnalyzer struct {
	logger Logger
	options.Resizer
}

// NewAnalyzer returns a new Analyzer using the given Resizer.
func NewAnalyzer(resizer options.Resizer) Analyzer {
	logger := Logger{
		DebugMode: false,
	}

	return NewAnalyzerWithLogger(resizer, logger)
}

// NewAnalyzerWithLogger returns a new analyzer with the given Resizer and Logger.
func NewAnalyzerWithLogger(resizer options.Resizer, logger Logger) Analyzer {
	if logger.Log == nil {
		logger.Log = log.New(ioutil.Discard, "", 0)
	}
	return &smartcropAnalyzer{Resizer: resizer, logger: logger}
}

// NewSettingsAnalyzer returns a configurable analyzer using the given Resizer.
func NewSettingsAnalyzer(resizer options.Resizer) SettingsAnalyzer {
	return NewSettingsAnalyzerWithLogger(resizer, Logger{})
}

// NewSettingsAnalyzerWithLogger returns a configurable analyzer with the given
// Resizer and Logger.
func NewSettingsAnalyzerWithLogger(resizer options.Resizer, logger Logger) SettingsAnalyzer {
	if logger.Log == nil {
		logger.Log = log.New(ioutil.Discard, "", 0)
	}
	return &smartcropAnalyzer{Resizer: resizer, logger: logger}
}

// FindBestCrop keeps the historical entry point and its behavior. It delegates
// to FindBestCropWithSettings using the default settings and the requested
// target dimensions.
func (o smartcropAnalyzer) FindBestCrop(img image.Image, width, height int) (image.Rectangle, error) {
	s := DefaultSettings()
	s.TargetWidth = width
	s.TargetHeight = height
	return o.FindBestCropWithSettings(img, s)
}

// FindBestCropWithSettings runs the content aware analysis described by s and
// maps the winning analysis window back into the source image's coordinate
// system. It never mutates img and always either returns a positive-area
// rectangle contained in img.Bounds() or one of the package's stable errors.
func (o smartcropAnalyzer) FindBestCropWithSettings(img image.Image, s Settings) (image.Rectangle, error) {
	s = s.withDefaults()

	srcBounds := img.Bounds()
	if srcBounds.Dx() <= 0 || srcBounds.Dy() <= 0 {
		return image.Rectangle{}, ErrEmptyImage
	}

	targetW, targetH, err := resolveTarget(s, srcBounds)
	if err != nil {
		return image.Rectangle{}, err
	}

	// fitScale is the ratio between the source image and the requested crop
	// window. It is always >= 1 because targets larger than the source are
	// rejected above.
	fitScale := math.Min(
		float64(srcBounds.Dx())/float64(targetW),
		float64(srcBounds.Dy())/float64(targetH))

	// Determine the ratio between analysis pixels and source pixels. An
	// explicit AnalysisScale wins; otherwise the legacy PrescaleMin rule is
	// used while prescaling is enabled.
	analysisScale := 1.0
	switch {
	case s.AnalysisScale > 0:
		analysisScale = s.AnalysisScale
	case s.Prescale:
		if f := s.PrescaleMin / math.Min(float64(srcBounds.Dx()), float64(srcBounds.Dy())); f < 1.0 {
			analysisScale = f
		}
	}

	analysisW := int(chop(float64(srcBounds.Dx()) * analysisScale))
	analysisH := int(chop(float64(srcBounds.Dy()) * analysisScale))
	if analysisW <= 0 || analysisH <= 0 {
		return image.Rectangle{}, ErrZeroSizeAnalysis
	}

	// Copy the source into an owned, zero-origin RGBA buffer so the caller's
	// pixels are never modified. Resizing always allocates a fresh image; the
	// copy additionally normalizes any non-zero source origin.
	var lowimg *image.RGBA

	// Keep the per-axis scales used to return analysis coordinates to the
	// source. A resizer can round width and height differently, so a single
	// shared factor would drift on extreme aspect ratios.
	scaleX, scaleY := 1.0, 1.0
	if analysisScale < 1.0-floatEpsilon {
		resized := o.Resize(img, uint(analysisW), uint(analysisH))
		lowimg = toZeroOriginRGBA(resized)
		// The resizer is free to round the requested size; re-derive the
		// effective scales from the buffer it actually produced.
		if lowimg.Bounds().Dx() <= 0 || lowimg.Bounds().Dy() <= 0 {
			return image.Rectangle{}, ErrZeroSizeAnalysis
		}
		scaleX = float64(lowimg.Bounds().Dx()) / float64(srcBounds.Dx())
		scaleY = float64(lowimg.Bounds().Dy()) / float64(srcBounds.Dy())
		analysisScale = scaleX
	} else {
		lowimg = toZeroOriginRGBA(img)
	}

	cropWidth := chop(float64(targetW) * fitScale * scaleX)
	cropHeight := chop(float64(targetH) * fitScale * scaleY)
	if cropWidth <= 0 || cropHeight <= 0 {
		return image.Rectangle{}, ErrNoCropCandidate
	}
	realMinScale := math.Min(s.MaxScale, math.Max(1.0/fitScale, s.MinScale))

	o.logger.Log.Printf("original resolution: %dx%d\n", srcBounds.Dx(), srcBounds.Dy())
	o.logger.Log.Printf("analysis scale: x=%f y=%f, cropw: %f, croph: %f, minscale: %f\n",
		scaleX, scaleY, cropWidth, cropHeight, realMinScale)

	if o.logger.DebugMode {
		_ = writeImage("png", lowimg, "./smartcrop_prescale.png")
	}

	analysisCrop, err := analyse(o.logger, lowimg, s, cropWidth, cropHeight, realMinScale)
	if err != nil {
		return image.Rectangle{}, err
	}

	topCrop := mapToSource(analysisCrop, srcBounds, scaleX, scaleY)

	return topCrop.Canon(), nil
}

// resolveTarget validates the requested dimensions and derives a missing side
// from the source aspect ratio.
func resolveTarget(s Settings, b image.Rectangle) (int, int, error) {
	w := s.TargetWidth
	h := s.TargetHeight

	if w < 0 || h < 0 || (w == 0 && h == 0) {
		return 0, 0, ErrInvalidDimensions
	}

	switch {
	case w == 0:
		// Preserve the source aspect ratio and apply the same chop rounding
		// the rest of the pipeline uses.
		w = int(chop(float64(h) * float64(b.Dx()) / float64(b.Dy())))
		if w <= 0 {
			return 0, 0, ErrInvalidDimensions
		}
	case h == 0:
		h = int(chop(float64(w) * float64(b.Dy()) / float64(b.Dx())))
		if h <= 0 {
			return 0, 0, ErrInvalidDimensions
		}
	}

	if w > b.Dx() || h > b.Dy() {
		return 0, 0, ErrTargetTooLarge
	}

	return w, h, nil
}

// mapToSource converts a zero-origin analysis rectangle back into the source
// image's coordinate system. Min points use floor rounding toward the inside
// and Max points use ceil rounding toward the outside, which guarantees the
// mapped window fully contains the analysis window. Per-axis scales avoid
// drift when the resizer rounds width and height differently. The result is
// clamped to the source bounds and is guaranteed to have positive width and
// height.
func mapToSource(crop image.Rectangle, srcBounds image.Rectangle, scaleX, scaleY float64) image.Rectangle {
	invX := 1.0 / scaleX
	invY := 1.0 / scaleY

	minX := srcBounds.Min.X + int(math.Floor(float64(crop.Min.X)*invX))
	minY := srcBounds.Min.Y + int(math.Floor(float64(crop.Min.Y)*invY))
	maxX := srcBounds.Min.X + int(math.Ceil(float64(crop.Max.X)*invX))
	maxY := srcBounds.Min.Y + int(math.Ceil(float64(crop.Max.Y)*invY))

	rect := image.Rect(minX, minY, maxX, maxY)

	// Defensive clamping: a legal analysis rectangle must always map inside
	// the source bounds, but this keeps rounding surprises from escaping.
	if rect.Min.X < srcBounds.Min.X {
		rect.Min.X = srcBounds.Min.X
	}
	if rect.Min.Y < srcBounds.Min.Y {
		rect.Min.Y = srcBounds.Min.Y
	}
	if rect.Max.X > srcBounds.Max.X {
		rect.Max.X = srcBounds.Max.X
	}
	if rect.Max.Y > srcBounds.Max.Y {
		rect.Max.Y = srcBounds.Max.Y
	}
	if rect.Max.X <= rect.Min.X {
		rect.Max.X = rect.Min.X + 1
	}
	if rect.Max.Y <= rect.Min.Y {
		rect.Max.Y = rect.Min.Y + 1
	}
	if rect.Max.X > srcBounds.Max.X || rect.Max.Y > srcBounds.Max.Y {
		// Can only happen on degenerate one-pixel sources.
		rect = image.Rect(srcBounds.Min.X, srcBounds.Min.Y,
			srcBounds.Min.X+1, srcBounds.Min.Y+1)
	}

	return rect
}

// totalScore combines the weighted, area-normalized contributions. The formula
// matches the original implementation so existing crops stay reproducible.
func (c Crop) totalScore(s Settings) float64 {
	area := float64(c.Dx()) * float64(c.Dy())
	if area == 0 {
		return 0
	}
	return (c.Score.Detail*s.DetailWeight + c.Score.Skin*s.SkinWeight + c.Score.Saturation*s.SaturationWeight) / area
}

func chop(x float64) float64 {
	if x < 0 {
		return math.Ceil(x)
	}
	return math.Floor(x)
}

func thirds(x float64) float64 {
	x = (math.Mod(x-(1.0/3.0)+1.0, 2.0)*0.5 - 0.5) * 16.0
	return math.Max(1.0-x*x, 0.0)
}

func bounds(l float64) float64 {
	return math.Min(math.Max(l, 0.0), 255)
}

// importance returns the spatial importance of pixel (x, y) relative to crop.
// Every branch is finite for positive-area crops.
func importance(s Settings, crop Crop, x, y int) float64 {
	if crop.Min.X > x || x >= crop.Max.X || crop.Min.Y > y || y >= crop.Max.Y {
		return s.OutsideImportance
	}

	xf := float64(x-crop.Min.X) / float64(crop.Dx())
	yf := float64(y-crop.Min.Y) / float64(crop.Dy())

	px := math.Abs(0.5-xf) * 2.0
	py := math.Abs(0.5-yf) * 2.0

	dx := math.Max(px-1.0+s.EdgeRadius, 0.0)
	dy := math.Max(py-1.0+s.EdgeRadius, 0.0)
	d := (dx*dx + dy*dy) * s.EdgeWeight

	v := 1.41 - math.Sqrt(px*px+py*py)
	if s.RuleOfThirds {
		v += (math.Max(0.0, v+d+0.5) * 1.2) * (thirds(px) + thirds(py))
	}

	return v + d
}

// score accumulates the skin, detail and saturation contributions of one crop.
// It is deterministic: pixels are visited in row-major order and every
// intermediate value is finite, including on black, solid and transparent
// images.
func score(s Settings, output *image.RGBA, crop Crop) Score {
	width := output.Bounds().Dx()
	height := output.Bounds().Dy()
	result := Score{}

	step := s.ScoreDownSample
	maxX := width - step
	maxY := height - step
	if maxX < 0 || maxY < 0 {
		// Image smaller than the sampling stride: no legal sample position,
		// return a well-defined zero score instead of looping on negatives.
		return result
	}

	for y := 0; y <= maxY; y += step {
		for x := 0; x <= maxX; x += step {
			c := output.RGBAAt(x, y)
			r8 := float64(c.R)
			g8 := float64(c.G)
			b8 := float64(c.B)

			imp := importance(s, crop, x, y)
			det := g8 / 255.0

			result.Skin += r8 / 255.0 * (det + s.SkinBias) * imp
			result.Detail += det * imp
			result.Saturation += b8 / 255.0 * (det + s.SaturationBias) * imp
		}
	}

	return result
}

// analyse runs the detectors and returns the highest scoring candidate. The
// winner is selected with an order-independent tie breaker so the loop
// enumeration can never change the result.
func analyse(logger Logger, img *image.RGBA, s Settings, cropWidth, cropHeight, realMinScale float64) (image.Rectangle, error) {
	o := image.NewRGBA(img.Bounds())

	now := time.Now()
	edgeDetect(img, o)
	logger.Log.Println("Time elapsed edge:", time.Since(now))
	debugOutput(logger.DebugMode, o, "edge")

	now = time.Now()
	skinDetect(s, img, o)
	logger.Log.Println("Time elapsed skin:", time.Since(now))
	debugOutput(logger.DebugMode, o, "skin")

	now = time.Now()
	saturationDetect(s, img, o)
	logger.Log.Println("Time elapsed sat:", time.Since(now))
	debugOutput(logger.DebugMode, o, "saturation")

	now = time.Now()
	cs := crops(s, o, cropWidth, cropHeight, realMinScale)
	logger.Log.Println("Time elapsed crops:", time.Since(now), len(cs))
	if len(cs) == 0 {
		return image.Rectangle{}, ErrNoCropCandidate
	}

	now = time.Now()
	topIdx := 0
	topScore := math.Inf(-1)
	for i := range cs {
		cs[i].Score = score(s, o, cs[i])
		cur := cs[i].totalScore(s)
		if betterCandidate(cur, cs[i], topScore, cs[topIdx]) {
			topIdx = i
			topScore = cur
		}
	}
	logger.Log.Println("Time elapsed score:", time.Since(now))

	topCrop := cs[topIdx]
	if logger.DebugMode {
		drawDebugCrop(s, topCrop, o)
		debugOutput(true, o, "final")
	}

	return topCrop.Rectangle, nil
}

// betterCandidate encodes the deterministic winner rule. A candidate wins on a
// strictly larger total score; exact ties are resolved by area, then Min.Y and
// Min.X, so reordering the candidate slice cannot change the outcome.
func betterCandidate(curScore float64, cur Crop, bestScore float64, best Crop) bool {
	if curScore != bestScore {
		return curScore > bestScore
	}
	curArea := cur.Dx() * cur.Dy()
	bestArea := best.Dx() * best.Dy()
	if curArea != bestArea {
		return curArea > bestArea
	}
	if cur.Min.Y != best.Min.Y {
		return cur.Min.Y < best.Min.Y
	}
	return cur.Min.X < best.Min.X
}

func saturation(c color.RGBA) float64 {
	cMax, cMin := uint8(0), uint8(255)
	if c.R > cMax {
		cMax = c.R
	}
	if c.R < cMin {
		cMin = c.R
	}
	if c.G > cMax {
		cMax = c.G
	}
	if c.G < cMin {
		cMin = c.G
	}
	if c.B > cMax {
		cMax = c.B
	}
	if c.B < cMin {
		cMin = c.B
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

func cie(c color.RGBA) float64 {
	return 0.5126*float64(c.B) + 0.7152*float64(c.G) + 0.0722*float64(c.R)
}

// skinCol returns the normalized skin-color match. The magnitude guard keeps
// black pixels (the transparent or solid-black case) from dividing by zero and
// producing NaN.
func skinCol(c color.RGBA) float64 {
	r8, g8, b8 := float64(c.R), float64(c.G), float64(c.B)

	mag := math.Sqrt(r8*r8 + g8*g8 + b8*b8)
	if mag == 0 {
		return 0
	}
	rd := r8/mag - skinColor[0]
	gd := g8/mag - skinColor[1]
	bd := b8/mag - skinColor[2]

	d := math.Sqrt(rd*rd + gd*gd + bd*bd)
	return 1.0 - d
}

func makeCies(img *image.RGBA) []float64 {
	width := img.Bounds().Dx()
	height := img.Bounds().Dy()
	cies := make([]float64, width*height)
	i := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			cies[i] = cie(img.RGBAAt(x, y))
			i++
		}
	}

	return cies
}

func edgeDetect(i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()
	cies := makeCies(i)

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

			nc := color.RGBA{0, uint8(bounds(lightness)), 0, 255}
			o.SetRGBA(x, y, nc)
		}
	}
}

func skinDetect(s Settings, i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			lightness := cie(i.RGBAAt(x, y)) / 255.0
			skin := skinCol(i.RGBAAt(x, y))

			c := o.RGBAAt(x, y)
			if skin > s.SkinThreshold && lightness >= skinBrightnessMin && lightness <= skinBrightnessMax {
				r := (skin - s.SkinThreshold) * (255.0 / (1.0 - s.SkinThreshold))
				nc := color.RGBA{uint8(bounds(r)), c.G, c.B, 255}
				o.SetRGBA(x, y, nc)
			} else {
				nc := color.RGBA{0, c.G, c.B, 255}
				o.SetRGBA(x, y, nc)
			}
		}
	}
}

func saturationDetect(s Settings, i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			lightness := cie(i.RGBAAt(x, y)) / 255.0
			sat := saturation(i.RGBAAt(x, y))

			c := o.RGBAAt(x, y)
			if sat > s.SaturationThreshold && lightness >= saturationBrightnessMin && lightness <= saturationBrightnessMax {
				b := (sat - s.SaturationThreshold) * (255.0 / (1.0 - s.SaturationThreshold))
				nc := color.RGBA{c.R, c.G, uint8(bounds(b)), 255}
				o.SetRGBA(x, y, nc)
			} else {
				nc := color.RGBA{c.R, c.G, 0, 255}
				o.SetRGBA(x, y, nc)
			}
		}
	}
}

// crops enumerates candidate windows in a fixed scale, y, x order. The loop
// guards keep windows inside the analysis bounds even when floating point
// drift lands a candidate exactly on the border, and every returned rectangle
// has positive width and height.
func crops(s Settings, i image.Image, cropWidth, cropHeight, realMinScale float64) []Crop {
	res := []Crop{}
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()

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

	for scale := s.MaxScale; scale >= realMinScale-floatEpsilon; scale -= s.ScaleStep {
		w := cropW * scale
		h := cropH * scale
		intW := int(chop(w))
		intH := int(chop(h))
		if intW <= 0 || intH <= 0 || intW > width || intH > height {
			continue
		}

		for y := 0; float64(y)+h <= float64(height)+floatEpsilon; y += s.Step {
			if y+intH > height {
				continue
			}
			for x := 0; float64(x)+w <= float64(width)+floatEpsilon; x += s.Step {
				if x+intW > width {
					continue
				}
				res = append(res, Crop{
					Rectangle: image.Rect(x, y, x+intW, y+intH),
				})
			}
		}
	}

	return res
}

// toZeroOriginRGBA allocates a fresh, zero-origin RGBA buffer and copies img
// into it. Always copying (instead of returning an *image.RGBA input as-is)
// guarantees the caller's pixels are never mutated and that candidate
// coordinates computed against Bounds().Dx/Dy line up with the pixel data even
// when the source image has a non-zero Min point.
func toZeroOriginRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Copy(out, image.Pt(0, 0), img, b, draw.Src, nil)
	return out
}
