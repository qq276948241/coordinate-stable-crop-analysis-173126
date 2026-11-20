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

import "errors"

// Scoring weights and detection thresholds. Every value is a plain number so
// the exact same Settings always produce the exact same crop, regardless of
// how many goroutines analyze the same image concurrently.
const (
	// DefaultDetailWeight is the weight of the edge/detail contribution.
	DefaultDetailWeight = 0.2
	// DefaultSkinBias is added to the normalized green channel before the
	// skin contribution is accumulated.
	DefaultSkinBias   = 0.01
	skinBrightnessMin = 0.2
	skinBrightnessMax = 1.0
	// DefaultSkinThreshold is the minimum normalized skin-color distance
	// for a pixel to be considered skin.
	DefaultSkinThreshold = 0.8
	// DefaultSkinWeight is the weight of the skin contribution.
	DefaultSkinWeight       = 1.8
	saturationBrightnessMin = 0.05
	saturationBrightnessMax = 0.9
	// DefaultSaturationThreshold is the minimum saturation for a pixel to
	// contribute to the saturation score.
	DefaultSaturationThreshold = 0.4
	// DefaultSaturationBias is added to the normalized green channel before
	// the saturation contribution is accumulated.
	DefaultSaturationBias = 0.2
	// DefaultSaturationWeight is the weight of the saturation contribution.
	DefaultSaturationWeight = 0.3

	// DefaultScoreDownSample is the stride in pixels used while scoring a
	// candidate crop.
	DefaultScoreDownSample = 8
	// DefaultStep is the distance in analysis pixels between neighboring
	// candidate crops.
	DefaultStep = 8
	// DefaultScaleStep is the decrement applied between candidate scales.
	DefaultScaleStep = 0.1
	// DefaultMinScale is the smallest candidate scale relative to the
	// requested crop window.
	DefaultMinScale = 0.9
	// DefaultMaxScale is the largest candidate scale.
	DefaultMaxScale = 1.0
	// DefaultEdgeRadius controls how close to the crop border the edge
	// penalty starts to apply.
	DefaultEdgeRadius = 0.4
	// DefaultEdgeWeight is the (negative) weight of the edge penalty.
	DefaultEdgeWeight = -20.0
	// DefaultOutsideImportance is the importance assigned to pixels outside
	// of the evaluated crop.
	DefaultOutsideImportance = -0.5
	// DefaultRuleOfThirds toggles the rule-of-thirds bonus.
	DefaultRuleOfThirds = true

	// DefaultPrescale enables pre-scaling of large images to the analysis
	// scale before the detectors run.
	DefaultPrescale = true
	// DefaultPrescaleMin is the smallest dimension, in source pixels, at or
	// below which an image is pre-scaled for analysis.
	DefaultPrescaleMin = 400.0
)

// Sentinel errors returned by the analyzer. They are stable values so callers
// can inspect them with errors.Is / a direct equality check.
var (
	// ErrInvalidDimensions gets returned when the supplied dimensions are
	// invalid, for example a negative width or height or both zero.
	ErrInvalidDimensions = errors.New("expect either a positive height or width")

	// ErrEmptyImage gets returned when the source image has no pixels.
	ErrEmptyImage = errors.New("source image has empty bounds")

	// ErrTargetTooLarge gets returned when the requested crop window cannot
	// fit inside the source image.
	ErrTargetTooLarge = errors.New("requested crop dimensions exceed image bounds")

	// ErrZeroSizeAnalysis gets returned when the configured analysis scale
	// collapses the image to a zero-width or zero-height analysis buffer.
	ErrZeroSizeAnalysis = errors.New("analysis image has zero size after scaling")

	// ErrNoCropCandidate gets returned when no legal, positive-area candidate
	// window can be built. This keeps the analyzer from returning an
	// uninitialized rectangle.
	ErrNoCropCandidate = errors.New("no crop candidate could be generated")

	// ErrCropOutOfBounds gets returned when a crop rectangle does not lie
	// inside the source image bounds.
	ErrCropOutOfBounds = errors.New("crop rectangle exceeds image bounds")

	// ErrNilResizer gets returned when a resizing entry point is called with
	// a nil Resizer.
	ErrNilResizer = errors.New("resizer must not be nil")
)

// Settings configures a crop analysis. Zero-valued numeric fields are replaced
// by the package defaults through DefaultSettings, so callers only need to set
// the fields they want to change. Settings values are read by the analyzer and
// never mutated, which makes concurrent use safe.
type Settings struct {
	// AnalysisScale, when greater than zero, overrides the automatic
	// pre-scaling factor. It is interpreted as the ratio between analysis
	// pixels and source pixels, e.g. 0.5 analyzes a half-size image. A value
	// of zero keeps the Prescale/PrescaleMin behavior.
	AnalysisScale float64
	// TargetWidth and TargetHeight describe the requested crop window in
	// source pixels. A zero value is derived from the other dimension using
	// the source image's aspect ratio; both zero is invalid.
	TargetWidth  int
	TargetHeight int

	// Prescale toggles pre-scaling of large source images.
	Prescale bool
	// PrescaleMin is the smallest source dimension at or below which
	// pre-scaling kicks in.
	PrescaleMin float64

	// ScoreDownSample is the pixel stride of the scoring loops.
	ScoreDownSample int
	// Step is the distance between neighboring candidate windows.
	Step int
	// ScaleStep is the decrement between candidate scales.
	ScaleStep float64
	// MinScale is the smallest candidate scale.
	MinScale float64
	// MaxScale is the largest candidate scale.
	MaxScale float64
	// EdgeRadius controls the onset of the edge penalty.
	EdgeRadius float64
	// EdgeWeight is the (negative) weight of the edge penalty.
	EdgeWeight float64
	// OutsideImportance is the importance of pixels outside a crop.
	OutsideImportance float64
	// RuleOfThirds toggles the rule-of-thirds bonus.
	RuleOfThirds bool

	// DetailWeight weights the edge/detail contribution.
	DetailWeight float64
	// SkinBias biases the skin contribution.
	SkinBias float64
	// SkinThreshold is the minimum normalized skin-color distance.
	SkinThreshold float64
	// SkinWeight weights the skin contribution.
	SkinWeight float64
	// SaturationThreshold is the minimum saturation for a scored pixel.
	SaturationThreshold float64
	// SaturationBias biases the saturation contribution.
	SaturationBias float64
	// SaturationWeight weights the saturation contribution.
	SaturationWeight float64
}

// DefaultSettings returns the settings used by FindBestCrop. The values match
// the original smartcrop constants, so the legacy entry point keeps its
// historical behavior.
func DefaultSettings() Settings {
	return Settings{
		Prescale:            DefaultPrescale,
		PrescaleMin:         DefaultPrescaleMin,
		ScoreDownSample:     DefaultScoreDownSample,
		Step:                DefaultStep,
		ScaleStep:           DefaultScaleStep,
		MinScale:            DefaultMinScale,
		MaxScale:            DefaultMaxScale,
		EdgeRadius:          DefaultEdgeRadius,
		EdgeWeight:          DefaultEdgeWeight,
		OutsideImportance:   DefaultOutsideImportance,
		RuleOfThirds:        DefaultRuleOfThirds,
		DetailWeight:        DefaultDetailWeight,
		SkinBias:            DefaultSkinBias,
		SkinThreshold:       DefaultSkinThreshold,
		SkinWeight:          DefaultSkinWeight,
		SaturationThreshold: DefaultSaturationThreshold,
		SaturationBias:      DefaultSaturationBias,
		SaturationWeight:    DefaultSaturationWeight,
	}
}

// withDefaults fills every unset field with the package default. It returns a
// value copy so the caller's Settings are never modified.
func (s Settings) withDefaults() Settings {
	d := DefaultSettings()

	if s.AnalysisScale != 0 {
		d.AnalysisScale = s.AnalysisScale
	}
	if s.TargetWidth != 0 {
		d.TargetWidth = s.TargetWidth
	}
	if s.TargetHeight != 0 {
		d.TargetHeight = s.TargetHeight
	}

	// Boolean fields cannot distinguish an explicit false from an unset
	// value, so they are taken verbatim from the caller. Always build a
	// custom Settings from DefaultSettings() (as FindBestCrop does) rather
	// than from a zero-value literal.
	d.Prescale = s.Prescale
	if s.PrescaleMin != 0 {
		d.PrescaleMin = s.PrescaleMin
	}
	if s.ScoreDownSample != 0 {
		d.ScoreDownSample = s.ScoreDownSample
	}
	if s.Step != 0 {
		d.Step = s.Step
	}
	if s.ScaleStep != 0 {
		d.ScaleStep = s.ScaleStep
	}
	if s.MinScale != 0 {
		d.MinScale = s.MinScale
	}
	if s.MaxScale != 0 {
		d.MaxScale = s.MaxScale
	}
	if s.EdgeRadius != 0 {
		d.EdgeRadius = s.EdgeRadius
	}
	if s.EdgeWeight != 0 {
		d.EdgeWeight = s.EdgeWeight
	}
	if s.OutsideImportance != 0 {
		d.OutsideImportance = s.OutsideImportance
	}
	d.RuleOfThirds = s.RuleOfThirds
	if s.DetailWeight != 0 {
		d.DetailWeight = s.DetailWeight
	}
	if s.SkinBias != 0 {
		d.SkinBias = s.SkinBias
	}
	if s.SkinThreshold != 0 {
		d.SkinThreshold = s.SkinThreshold
	}
	if s.SkinWeight != 0 {
		d.SkinWeight = s.SkinWeight
	}
	if s.SaturationThreshold != 0 {
		d.SaturationThreshold = s.SaturationThreshold
	}
	if s.SaturationBias != 0 {
		d.SaturationBias = s.SaturationBias
	}
	if s.SaturationWeight != 0 {
		d.SaturationWeight = s.SaturationWeight
	}

	return d
}
