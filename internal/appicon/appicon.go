package appicon

import (
	"image"
	"image/color"
	"math"
)

// Image draws the app icon (three piano keys under a gold bar) at the given size.
func Image(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)/s, float64(y)/s
			c := color.RGBA{0x24, 0x21, 0x1f, 0xff}
			if fy > 0.18 && fy < 0.86 && fx > 0.1 && fx < 0.9 {
				// three white keys
				kx := (fx - 0.1) / 0.8 * 3
				if kx-math.Floor(kx) > 0.06 {
					c = color.RGBA{0xf6, 0xf0, 0xe4, 0xff}
				}
				// two black keys
				if fy < 0.6 {
					for _, b := range []float64{1, 2} {
						if math.Abs(kx-b) < 0.28 {
							c = color.RGBA{0x14, 0x12, 0x11, 0xff}
						}
					}
				}
			}
			if fy > 0.1 && fy < 0.18 && fx > 0.1 && fx < 0.9 {
				c = color.RGBA{0xe0, 0xa9, 0x58, 0xff}
			}
			img.Set(x, y, c)
		}
	}
	return img
}
