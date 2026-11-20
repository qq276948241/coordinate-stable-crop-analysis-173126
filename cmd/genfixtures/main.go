/*
Fixture generator for the offline pixel tests.

It writes a set of deterministic PNG images into testdata/fixtures. The
generator is checked in so the fixtures can be reproduced at any time, while
the committed PNGs let the test suite run fully offline without executing code
from the samples directory.

Run with:

	go run ./cmd/genfixtures
*/
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const fixtureDir = "testdata/fixtures"

func main() {
	if err := os.MkdirAll(fixtureDir, 0755); err != nil {
		panic(err)
	}

	fixtures := map[string]*image.RGBA{
		"solid_black.png":     solid(80, 80, color.RGBA{0, 0, 0, 255}),
		"solid_white.png":     solid(80, 80, color.RGBA{255, 255, 255, 255}),
		"transparent.png":     solid(80, 80, color.RGBA{0, 0, 0, 0}),
		"gradient.png":        gradient(80, 80),
		"skin_blob.png":       skinBlob(80, 80),
		"edge_band.png":       edgeBand(80, 80),
		"subject_on_edge.png": subjectOnEdge(80, 80),
		"wide_320x40.png":     gradient(320, 40),
		"tall_40x320.png":     gradient(40, 320),
		"offset_canvas.png":   offsetCanvas(120, 100),
		"tie_grid.png":        tieGrid(96, 96),
	}

	for name, img := range fixtures {
		path := filepath.Join(fixtureDir, name)
		f, err := os.Create(path)
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, img); err != nil {
			panic(err)
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// gradient produces a smooth diagonal color gradient, including fully
// transparent rows and columns along the border.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			alpha := uint8(255)
			if x == 0 || y == 0 || x == w-1 || y == h-1 {
				alpha = 0
			}
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 255 / w),
				G: uint8(y * 255 / h),
				B: uint8((x + y) * 255 / (w + h)),
				A: alpha,
			})
		}
	}
	return img
}

// skinBlob places a saturated, skin-toned disk away from the borders so the
// analyzer has an unambiguous salient region.
func skinBlob(w, h int) *image.RGBA {
	img := solid(w, h, color.RGBA{30, 30, 30, 255})
	cx, cy, radius := float64(w)*0.7, float64(h)*0.35, math.Min(float64(w), float64(h))*0.18
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			if dx*dx+dy*dy <= radius*radius {
				img.SetRGBA(x, y, color.RGBA{R: 220, G: 170, B: 130, A: 255})
			}
		}
	}
	return img
}

// edgeBand creates a sharp vertical stripe which generates strong edge energy.
func edgeBand(w, h int) *image.RGBA {
	img := solid(w, h, color.RGBA{20, 20, 20, 255})
	for y := 0; y < h; y++ {
		for x := w / 3; x < 2*w/3; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 230, G: 230, B: 230, A: 255})
		}
	}
	return img
}

// subjectOnEdge puts the only high-contrast region flush against the left
// image border, exercising edge-window handling.
func subjectOnEdge(w, h int) *image.RGBA {
	img := solid(w, h, color.RGBA{10, 10, 10, 255})
	for y := 0; y < h; y++ {
		for x := 0; x < w/6; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 240, G: 120, B: 60, A: 255})
		}
	}
	return img
}

// offsetCanvas embeds a gradient inside an RGBA image whose bounds do not start
// at the origin, which the coordinate round-trip tests rely on.
func offsetCanvas(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(25, 40, 25+w, 40+h))
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x - 25) * 255 / w),
				G: uint8((y - 40) * 255 / h),
				B: 128,
				A: 255,
			})
		}
	}
	return img
}

// tieGrid draws four identical symmetric quadrants so multiple candidate
// windows score exactly equal, exercising the deterministic tie breaker.
func tieGrid(w, h int) *image.RGBA {
	img := solid(w, h, color.RGBA{40, 40, 40, 255})
	mark := func(x0, y0, x1, y1 int) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.SetRGBA(x, y, color.RGBA{R: 210, G: 160, B: 120, A: 255})
			}
		}
	}
	mw, mh := w/8, h/8
	mark(w/4-mw/2, h/4-mh/2, w/4+mw/2, h/4+mh/2)
	mark(3*w/4-mw/2, h/4-mh/2, 3*w/4+mw/2, h/4+mh/2)
	mark(w/4-mw/2, 3*h/4-mh/2, w/4+mw/2, 3*h/4+mh/2)
	mark(3*w/4-mw/2, 3*h/4-mh/2, 3*w/4+mw/2, 3*h/4+mh/2)
	return img
}
