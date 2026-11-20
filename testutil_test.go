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
	"os"
	"testing"
)

const exampleGopher = "./examples/gopher.jpg"

// loadExampleGopher decodes the bundled JPEG sample. It is used by the
// compatibility and natural-image round-trip tests; the synthetic fixtures in
// testdata/fixtures cover the deterministic offline cases.
func loadExampleGopher(t *testing.T) image.Image {
	t.Helper()

	f, err := os.Open(exampleGopher)
	if err != nil {
		t.Fatalf("open example image: %v", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode example image: %v", err)
	}
	return img
}
