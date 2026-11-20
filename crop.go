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

	"github.com/muesli/smartcrop/options"

	"golang.org/x/image/draw"
)

// SubImager is implemented by the standard library's in-place image types.
// Crop uses it directly when available and falls back to a pixel copy.
type SubImager interface {
	SubImage(r image.Rectangle) image.Image
}

// CropImage is the reusable, actual cropping entry point. It validates that
// rect is a canonical, positive-area rectangle contained in img.Bounds() and
// returns the corresponding sub-image. The source image is never modified; when
// the image type does not support SubImage a fresh RGBA copy is returned.
func CropImage(img image.Image, rect image.Rectangle) (image.Image, error) {
	b := img.Bounds()
	rect = rect.Canon()
	if rect.Empty() || !rect.In(b) {
		return nil, ErrCropOutOfBounds
	}

	if si, ok := img.(SubImager); ok {
		return si.SubImage(rect), nil
	}

	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Copy(out, image.Pt(0, 0), img, rect, draw.Src, nil)
	return out, nil
}

// CropImageToSize crops rect from img and resizes the result to exactly width
// by height pixels using resizer. The intermediate crop is guaranteed to stay
// inside the source bounds and the returned image always has the requested
// size. A nil resizer returns ErrNilResizer.
func CropImageToSize(img image.Image, rect image.Rectangle, width, height int, resizer options.Resizer) (image.Image, error) {
	if width <= 0 || height <= 0 {
		return nil, ErrInvalidDimensions
	}
	if resizer == nil {
		return nil, ErrNilResizer
	}

	cropped, err := CropImage(img, rect)
	if err != nil {
		return nil, err
	}

	return resizer.Resize(cropped, uint(width), uint(height)), nil
}

// ScoreCrop runs the skin, saturation and edge detectors on a copy of img and
// returns the three score components accumulated for rect on the
// original-resolution image. It exposes the exact scoring the analyzer uses,
// so a crop window returned at analysis scale can be verified against a direct,
// full-size score. The source image is never modified.
func ScoreCrop(img image.Image, rect image.Rectangle, s Settings) (Score, error) {
	s = s.withDefaults()

	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return Score{}, ErrEmptyImage
	}
	rect = rect.Canon()
	if rect.Empty() || !rect.In(b) {
		return Score{}, ErrCropOutOfBounds
	}

	rgba := toZeroOriginRGBA(img)
	local := image.Rect(
		rect.Min.X-b.Min.X, rect.Min.Y-b.Min.Y,
		rect.Max.X-b.Min.X, rect.Max.Y-b.Min.Y)

	o := image.NewRGBA(rgba.Bounds())
	edgeDetect(rgba, o)
	skinDetect(s, rgba, o)
	saturationDetect(s, rgba, o)

	return score(s, o, Crop{Rectangle: local}), nil
}
