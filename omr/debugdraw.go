package omr

import (
	"image"
	"image/color"
	"math"
)

// DebugCanvas draws recognition results over a page for inspection.
type DebugCanvas struct{ *image.RGBA }

func NewDebugCanvas(b *Bitmap) *DebugCanvas {
	img := image.NewRGBA(image.Rect(0, 0, b.W, b.H))
	for i, p := range b.Pix {
		v := uint8(255)
		if p == 1 {
			v = 150
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = v, v, v, 255
	}
	return &DebugCanvas{img}
}

func (d *DebugCanvas) Dot(x, y float64, r int, c color.RGBA) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				d.SetRGBA(int(math.Round(x))+dx, int(math.Round(y))+dy, c)
			}
		}
	}
}

func (d *DebugCanvas) Line(x0, y0, x1, y1 float64, c color.RGBA) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		d.SetRGBA(int(x0+t*(x1-x0)), int(y0+t*(y1-y0)), c)
	}
}

func (d *DebugCanvas) Rect(x0, y0, x1, y1 float64, c color.RGBA) {
	d.Line(x0, y0, x1, y0, c)
	d.Line(x0, y1, x1, y1, c)
	d.Line(x0, y0, x0, y1, c)
	d.Line(x1, y0, x1, y1, c)
}
