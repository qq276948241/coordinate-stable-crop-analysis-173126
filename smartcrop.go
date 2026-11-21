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
	"errors"
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
	// ErrInvalidDimensions gets returned when the supplied dimensions are invalid
	ErrInvalidDimensions = errors.New("Expect either a height or width")

	// ErrEmptyImage gets returned when the supplied image has no pixels at all
	ErrEmptyImage = errors.New("smartcrop: image has no pixels")

	// ErrImageTooSmall gets returned when the supplied image is too small to
	// be analyzed (e.g. a single row or column of pixels)
	ErrImageTooSmall = errors.New("smartcrop: image is too small to analyze")

	// ErrCropTooLarge gets returned when the requested crop dimensions exceed
	// the bounds of the supplied image
	ErrCropTooLarge = errors.New("smartcrop: crop dimensions exceed image bounds")

	// ErrPrescaleFailed gets returned when prescaling the image would produce
	// (or produced) an empty image
	ErrPrescaleFailed = errors.New("smartcrop: prescaling produced an empty image")

	// ErrNoCandidates gets returned when no viable crop candidate could be
	// determined for the supplied image and dimensions
	ErrNoCandidates = errors.New("smartcrop: no viable crop candidate found")

	// ErrInvalidCrop gets returned when a crop rectangle is empty or lies
	// entirely outside the bounds of the image it should be applied to
	ErrInvalidCrop = errors.New("smartcrop: crop rectangle is empty or outside image bounds")

	skinColor = [3]float64{0.78, 0.57, 0.44}
)

const (
	detailWeight            = 0.2
	skinBias                = 0.01
	skinBrightnessMin       = 0.2
	skinBrightnessMax       = 1.0
	skinThreshold           = 0.8
	skinWeight              = 1.8
	saturationBrightnessMin = 0.05
	saturationBrightnessMax = 0.9
	saturationThreshold     = 0.4
	saturationBias          = 0.2
	saturationWeight        = 0.3
	scoreDownSample         = 8 // step * minscale rounded down to the next power of two should be good
	step                    = 8
	scaleStep               = 0.1
	minScale                = 0.9
	maxScale                = 1.0
	edgeRadius              = 0.4
	edgeWeight              = -20.0
	outsideImportance       = -0.5
	ruleOfThirds            = true
	prescale                = true
	prescaleMin             = 400.00
)

// Analyzer interface analyzes its struct and returns the best possible crop with the given
// width and height returns an error if invalid
type Analyzer interface {
	FindBestCrop(img image.Image, width, height int) (image.Rectangle, error)
}

// OptionsAnalyzer extends Analyzer with a method that accepts per-call
// analysis options.
type OptionsAnalyzer interface {
	Analyzer
	FindBestCropWithOptions(img image.Image, width, height int, o Options) (image.Rectangle, error)
}

// Options contains the tunable parameters of the crop analysis. Use
// DefaultOptions as a starting point and adjust the fields you care about;
// degenerate loop-control values (Step, ScaleStep, ScoreDownSample <= 0)
// fall back to the library defaults to keep the analysis finite.
type Options struct {
	// Prescale enables downscaling the image before analyzing it. The
	// resulting crop window is mapped back to the original coordinate space.
	Prescale bool
	// PrescaleMin is the target size of the smaller image dimension when
	// prescaling. Values <= 0 disable prescaling.
	PrescaleMin float64

	// MinScale and MaxScale bound the scale of the crop window relative to
	// the requested dimensions.
	MinScale float64
	MaxScale float64
	// ScaleStep is the decrement applied to the scale while enumerating
	// candidate windows.
	ScaleStep float64
	// Step is the pixel stride between candidate windows.
	Step int
	// ScoreDownSample is the pixel stride used when scoring a window.
	ScoreDownSample int

	// RuleOfThirds enables the rule-of-thirds term in the importance function.
	RuleOfThirds bool

	// Weights of the individual score channels.
	DetailWeight     float64
	SkinWeight       float64
	SaturationWeight float64

	// EdgeRadius, EdgeWeight and OutsideImportance tune the importance
	// function applied to each pixel of a candidate window.
	EdgeRadius        float64
	EdgeWeight        float64
	OutsideImportance float64
}

// DefaultOptions returns the Options matching the library's historical
// behavior.
func DefaultOptions() Options {
	return Options{
		Prescale:          prescale,
		PrescaleMin:       prescaleMin,
		MinScale:          minScale,
		MaxScale:          maxScale,
		ScaleStep:         scaleStep,
		Step:              step,
		ScoreDownSample:   scoreDownSample,
		RuleOfThirds:      ruleOfThirds,
		DetailWeight:      detailWeight,
		SkinWeight:        skinWeight,
		SaturationWeight:  saturationWeight,
		EdgeRadius:        edgeRadius,
		EdgeWeight:        edgeWeight,
		OutsideImportance: outsideImportance,
	}
}

// withDefaults replaces degenerate loop-control values with the library
// defaults so the analysis cannot degenerate into infinite or empty loops.
func (o Options) withDefaults() Options {
	if o.Step <= 0 {
		o.Step = step
	}
	if o.ScoreDownSample <= 0 {
		o.ScoreDownSample = scoreDownSample
	}
	if o.ScaleStep <= 0 {
		o.ScaleStep = scaleStep
	}
	if o.MaxScale <= 0 {
		o.MaxScale = maxScale
	}
	if o.MinScale <= 0 {
		o.MinScale = minScale
	}
	return o
}

// Score contains values that classify matches
type Score struct {
	Detail     float64
	Saturation float64
	Skin       float64
}

// TotalScore combines the individual score channels using the weights from o,
// normalized by the area of r. An empty rectangle yields a score of 0, so the
// result is always free of NaN and Inf.
func (s Score) TotalScore(r image.Rectangle, o Options) float64 {
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return 0
	}
	return (s.Detail*o.DetailWeight + s.Skin*o.SkinWeight + s.Saturation*o.SaturationWeight) / float64(r.Dx()) / float64(r.Dy())
}

// Crop contains results
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
	options Options
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
	return &smartcropAnalyzer{Resizer: resizer, logger: logger, options: DefaultOptions()}
}

// NewAnalyzerWithOptions returns a new analyzer with the given Resizer and
// analysis Options. Its FindBestCrop behaves like FindBestCropWithOptions
// with the supplied Options.
func NewAnalyzerWithOptions(resizer options.Resizer, o Options) OptionsAnalyzer {
	return &smartcropAnalyzer{
		Resizer: resizer,
		logger: Logger{
			DebugMode: false,
			Log:       log.New(ioutil.Discard, "", 0),
		},
		options: o.withDefaults(),
	}
}

func (o smartcropAnalyzer) FindBestCrop(img image.Image, width, height int) (image.Rectangle, error) {
	return o.FindBestCropWithOptions(img, width, height, o.options)
}

// FindBestCropWithOptions analyzes img and returns the best crop window for
// the given target dimensions, using the supplied analysis options. The
// returned rectangle is expressed in the coordinate space of img (a non-zero
// bounds origin is preserved) and always lies within img's bounds.
func (o smartcropAnalyzer) FindBestCropWithOptions(img image.Image, width, height int, opts Options) (image.Rectangle, error) {
	if err := validateInput(img, width, height); err != nil {
		return image.Rectangle{}, err
	}
	opts = opts.withDefaults()

	b := img.Bounds()

	// resize image for faster processing
	scale := math.Min(float64(b.Dx())/float64(width), float64(b.Dy())/float64(height))
	var lowimg *image.RGBA

	// fx and fy map analysis coordinates back to the original image. They
	// are derived from the actual prescaled image, not the theoretical
	// prescale factor, so rounding inside the resizer cannot shift the
	// resulting window.
	fx, fy := 1.0, 1.0

	if opts.Prescale && opts.PrescaleMin > 0 {
		prescalefactor := 1.0
		if f := opts.PrescaleMin / math.Min(float64(b.Dx()), float64(b.Dy())); f < 1.0 {
			prescalefactor = f
		}
		o.logger.Log.Println(prescalefactor)

		w := uint(float64(b.Dx()) * prescalefactor)
		if w == 0 || float64(b.Dy())*float64(w)/float64(b.Dx()) < 0.5 {
			return image.Rectangle{}, ErrPrescaleFailed
		}

		// Resizers may assume a zero bounds origin; normalize first.
		src := img
		if src.Bounds().Min != (image.Point{}) {
			src = toRGBA(src)
		}

		smallimg := o.Resize(src, w, 0)
		lowimg = toRGBA(smallimg)
		if lowimg.Bounds().Dx() == 0 || lowimg.Bounds().Dy() == 0 {
			return image.Rectangle{}, ErrPrescaleFailed
		}

		fx = float64(lowimg.Bounds().Dx()) / float64(b.Dx())
		fy = float64(lowimg.Bounds().Dy()) / float64(b.Dy())
	} else {
		lowimg = toRGBA(img)
	}

	if o.logger.DebugMode {
		_ = writeImage("png", lowimg, "./smartcrop_prescale.png")
	}

	cropWidth, cropHeight := chop(float64(width)*scale*fx), chop(float64(height)*scale*fy)
	realMinScale := math.Min(opts.MaxScale, math.Max(1.0/scale, opts.MinScale))

	o.logger.Log.Printf("original resolution: %dx%d\n", b.Dx(), b.Dy())
	o.logger.Log.Printf("scale: %f, cropw: %f, croph: %f, minscale: %f\n", scale, cropWidth, cropHeight, realMinScale)

	topCrop, err := analyse(o.logger, lowimg, cropWidth, cropHeight, realMinScale, opts)
	if err != nil {
		return image.Rectangle{}, err
	}

	// Map the window back to the original coordinate space. With prescaling
	// disabled fx and fy are 1 and this is the identity.
	topCrop.Min.X = int(chop(float64(topCrop.Min.X) / fx))
	topCrop.Min.Y = int(chop(float64(topCrop.Min.Y) / fy))
	topCrop.Max.X = int(chop(float64(topCrop.Max.X) / fx))
	topCrop.Max.Y = int(chop(float64(topCrop.Max.Y) / fy))

	r := topCrop.Canon().Intersect(image.Rect(0, 0, b.Dx(), b.Dy()))
	if r.Empty() {
		return image.Rectangle{}, ErrNoCandidates
	}

	// Re-apply the original bounds origin so callers can map the result
	// straight back onto their input image.
	return r.Add(b.Min), nil
}

// validateInput rejects images and target dimensions that cannot yield a
// stable crop result.
func validateInput(img image.Image, width, height int) error {
	if img == nil {
		return ErrEmptyImage
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return ErrEmptyImage
	}
	if b.Dx() < 2 || b.Dy() < 2 {
		return ErrImageTooSmall
	}
	if width < 0 || height < 0 || (width == 0 && height == 0) {
		return ErrInvalidDimensions
	}
	if (width > 0 && width > b.Dx()) || (height > 0 && height > b.Dy()) {
		return ErrCropTooLarge
	}
	return nil
}

// FitAspect adjusts r so its aspect ratio matches width:height exactly
// (r.Dx()*height == r.Dy()*width). The over-proportional dimension is shrunk
// towards round-half-away-from-zero of the matching size; the rectangle keeps
// its center (within integer rounding) and, because it only ever shrinks,
// always stays inside bounds. If width or height is not positive, or the
// ratio cannot be reached with positive dimensions, the best-effort result
// is returned. r is intersected with bounds first.
func FitAspect(r image.Rectangle, width, height int, bounds image.Rectangle) image.Rectangle {
	r = r.Canon().Intersect(bounds)
	if r.Empty() || width <= 0 || height <= 0 {
		return r
	}

	for r.Dx()*height != r.Dy()*width {
		if r.Dx()*height > r.Dy()*width {
			// too wide: shrink width towards Dy*width/height
			nw := int(math.Floor(float64(r.Dy())*float64(width)/float64(height) + 0.5))
			if nw < 1 {
				nw = 1
			}
			if nw >= r.Dx() {
				nw = r.Dx() - 1
			}
			if nw < 1 {
				break
			}
			cx := (r.Min.X + r.Max.X) / 2
			r.Min.X = cx - nw/2
			r.Max.X = r.Min.X + nw
		} else {
			// too tall: shrink height towards Dx*height/width
			nh := int(math.Floor(float64(r.Dx())*float64(height)/float64(width) + 0.5))
			if nh < 1 {
				nh = 1
			}
			if nh >= r.Dy() {
				nh = r.Dy() - 1
			}
			if nh < 1 {
				break
			}
			cy := (r.Min.Y + r.Max.Y) / 2
			r.Min.Y = cy - nh/2
			r.Max.Y = r.Min.Y + nh
		}
	}

	return r
}

// CropImage cuts r out of img and returns it as a newly allocated, zero-origin
// image that shares no memory with img. r is intersected with img's bounds
// first; an empty intersection yields ErrInvalidCrop. Combine with FitAspect
// to snap the rectangle to an exact target aspect ratio before cropping.
func CropImage(img image.Image, r image.Rectangle) (image.Image, error) {
	if img == nil {
		return nil, ErrEmptyImage
	}
	r = r.Canon().Intersect(img.Bounds())
	if r.Empty() {
		return nil, ErrInvalidCrop
	}

	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Copy(out, image.Point{}, img, r, draw.Src, nil)
	return out, nil
}

func (c Crop) totalScore(o Options) float64 {
	return c.Score.TotalScore(c.Rectangle, o)
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

// Importance returns the per-pixel importance of position (x, y) within crop
// using the default options: an edge-distance term plus the rule-of-thirds
// bonus, or the outside-importance penalty for points outside the window.
func Importance(crop image.Rectangle, x, y int) float64 {
	return importanceOpts(crop, x, y, DefaultOptions())
}

func importanceOpts(crop image.Rectangle, x, y int, o Options) float64 {
	if crop.Min.X > x || x >= crop.Max.X || crop.Min.Y > y || y >= crop.Max.Y {
		return o.OutsideImportance
	}

	xf := float64(x-crop.Min.X) / float64(crop.Dx())
	yf := float64(y-crop.Min.Y) / float64(crop.Dy())

	px := math.Abs(0.5-xf) * 2.0
	py := math.Abs(0.5-yf) * 2.0

	dx := math.Max(px-1.0+o.EdgeRadius, 0.0)
	dy := math.Max(py-1.0+o.EdgeRadius, 0.0)
	d := (dx*dx + dy*dy) * o.EdgeWeight

	s := 1.41 - math.Sqrt(px*px+py*py)
	if o.RuleOfThirds {
		s += (math.Max(0.0, s+d+0.5) * 1.2) * (thirds(px) + thirds(py))
	}

	return s + d
}

// ScoreRegion computes the detail, saturation and skin scores of the given
// crop window over an image previously processed by EdgeDetect, SkinDetect
// and SaturationDetect, using the default options.
func ScoreRegion(output *image.RGBA, crop image.Rectangle) Score {
	return scoreWithOpts(output, Crop{Rectangle: crop}, DefaultOptions())
}

func scoreWithOpts(output *image.RGBA, crop Crop, o Options) Score {
	width := output.Bounds().Dx()
	height := output.Bounds().Dy()
	score := Score{}

	// same loops but with downsampling
	//for y := 0; y < height; y++ {
	//for x := 0; x < width; x++ {
	for y := 0; y <= height-o.ScoreDownSample; y += o.ScoreDownSample {
		for x := 0; x <= width-o.ScoreDownSample; x += o.ScoreDownSample {
			c := output.RGBAAt(x, y)
			r8 := float64(c.R)
			g8 := float64(c.G)
			b8 := float64(c.B)

			imp := importanceOpts(crop.Rectangle, x, y, o)
			det := g8 / 255.0

			score.Skin += r8 / 255.0 * (det + skinBias) * imp
			score.Detail += det * imp
			score.Saturation += b8 / 255.0 * (det + saturationBias) * imp
		}
	}

	return score
}

func analyse(logger Logger, img *image.RGBA, cropWidth, cropHeight, realMinScale float64, opts Options) (image.Rectangle, error) {
	o := image.NewRGBA(img.Bounds())

	now := time.Now()
	EdgeDetect(img, o)
	logger.Log.Println("Time elapsed edge:", time.Since(now))
	debugOutput(logger.DebugMode, o, "edge")

	now = time.Now()
	SkinDetect(img, o)
	logger.Log.Println("Time elapsed skin:", time.Since(now))
	debugOutput(logger.DebugMode, o, "skin")

	now = time.Now()
	SaturationDetect(img, o)
	logger.Log.Println("Time elapsed sat:", time.Since(now))
	debugOutput(logger.DebugMode, o, "saturation")

	now = time.Now()
	var topCrop Crop
	topScore := -1.0
	cs := crops(o, cropWidth, cropHeight, realMinScale, opts)
	logger.Log.Println("Time elapsed crops:", time.Since(now), len(cs))

	if len(cs) == 0 {
		return image.Rectangle{}, ErrNoCandidates
	}

	now = time.Now()
	for _, crop := range cs {
		nowIn := time.Now()
		crop.Score = scoreWithOpts(o, crop, opts)
		logger.Log.Println("Time elapsed single-score:", time.Since(nowIn))
		// Strictly greater: the first candidate in enumeration order wins
		// ties, keeping the result deterministic and independent of any
		// later reordering of equal-scored candidates.
		if s := crop.totalScore(opts); s > topScore {
			topCrop = crop
			topScore = s
		}
	}
	logger.Log.Println("Time elapsed score:", time.Since(now))

	if logger.DebugMode {
		drawDebugCrop(topCrop, o, opts)
		debugOutput(true, o, "final")
	}

	return topCrop.Rectangle, nil
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

func skinCol(c color.RGBA) float64 {
	r8, g8, b8 := float64(c.R), float64(c.G), float64(c.B)

	mag := math.Sqrt(r8*r8 + g8*g8 + b8*b8)
	if mag == 0 {
		// black (or fully transparent) pixel: no color direction, no skin
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

// EdgeDetect writes the edge (lightness Laplacian) channel of i into the
// green channel of o. Both images must have the same bounds.
func EdgeDetect(i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()
	cies := makeCies(i)

	var lightness float64
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x == 0 || x >= width-1 || y == 0 || y >= height-1 {
				//lightness = cie((*i).At(x, y))
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

// SkinDetect writes the skin-color channel of i into the red channel of o,
// preserving the other channels of o. Both images must have the same bounds.
func SkinDetect(i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			lightness := cie(i.RGBAAt(x, y)) / 255.0
			skin := skinCol(i.RGBAAt(x, y))

			c := o.RGBAAt(x, y)
			if skin > skinThreshold && lightness >= skinBrightnessMin && lightness <= skinBrightnessMax {
				r := (skin - skinThreshold) * (255.0 / (1.0 - skinThreshold))
				nc := color.RGBA{uint8(bounds(r)), c.G, c.B, 255}
				o.SetRGBA(x, y, nc)
			} else {
				nc := color.RGBA{0, c.G, c.B, 255}
				o.SetRGBA(x, y, nc)
			}
		}
	}
}

// SaturationDetect writes the saturation channel of i into the blue channel
// of o, preserving the other channels of o. Both images must have the same
// bounds.
func SaturationDetect(i *image.RGBA, o *image.RGBA) {
	width := i.Bounds().Dx()
	height := i.Bounds().Dy()

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			lightness := cie(i.RGBAAt(x, y)) / 255.0
			saturation := saturation(i.RGBAAt(x, y))

			c := o.RGBAAt(x, y)
			if saturation > saturationThreshold && lightness >= saturationBrightnessMin && lightness <= saturationBrightnessMax {
				b := (saturation - saturationThreshold) * (255.0 / (1.0 - saturationThreshold))
				nc := color.RGBA{c.R, c.G, uint8(bounds(b)), 255}
				o.SetRGBA(x, y, nc)
			} else {
				nc := color.RGBA{c.R, c.G, 0, 255}
				o.SetRGBA(x, y, nc)
			}
		}
	}
}

func crops(i image.Image, cropWidth, cropHeight, realMinScale float64, o Options) []Crop {
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

	for scale := o.MaxScale; scale >= realMinScale; scale -= o.ScaleStep {
		for y := 0; float64(y)+cropH*scale <= float64(height); y += o.Step {
			for x := 0; float64(x)+cropW*scale <= float64(width); x += o.Step {
				w, h := int(cropW*scale), int(cropH*scale)
				if w <= 0 || h <= 0 {
					// never emit zero-area candidates: they cannot be
					// scored (division by zero) and can never win
					continue
				}
				res = append(res, Crop{
					Rectangle: image.Rect(x, y, x+w, y+h),
				})
			}
		}
	}

	return res
}

// toRGBA converts an image.Image to a newly allocated, zero-origin
// *image.RGBA. It never aliases the input, so analysis can neither mutate
// the caller's buffer nor be confused by a non-zero origin or custom stride.
func toRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Copy(out, image.Point{}, img, b, draw.Src, nil)
	return out
}
