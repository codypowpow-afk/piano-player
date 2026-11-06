// omrdebug draws what the sheet-music reader found on one PDF page:
//
//	go run ./tools/omrdebug -o page5.png score.pdf 5
//
// red = notehead, orange = hollow notehead, green tick = stem side,
// blue line = barline, green line = end of key signature, boxes = accidentals,
// magenta bar = 8va range.
package main

import (
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"strconv"

	"github.com/codypowpow-afk/piano-player/omr"
)

func main() {
	out := flag.String("o", "omr_debug.png", "output PNG")
	flag.Parse()
	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		panic(err)
	}
	pages, err := omr.ExtractPages(data)
	if err != nil {
		panic(err)
	}
	pg, _ := strconv.Atoi(flag.Arg(1))
	if pg < 1 || pg > len(pages) {
		fmt.Println("page out of range")
		os.Exit(1)
	}
	pr := omr.AnalyzePage(pages[pg-1])
	fmt.Println("space", pr.Space, "staves", len(pr.Staves), "heads", len(pr.Heads), "accs", len(pr.Accs))
	c := omr.NewDebugCanvas(pr.Clean)
	for si, s := range pr.Staves {
		for l := 0; l < 5; l++ {
			for x := s.X0; x <= s.X1; x += 3 {
				c.SetRGBA(x, int(s.LineY(l, float64(x))+0.5), color.RGBA{255, 180, 180, 255})
			}
		}
		fmt.Printf("staff %d clefs %v key %d keyEnd %.0f\n", si, pr.Clefs[si], pr.KeySig[si], pr.KeyEndX[si])
		c.Line(pr.KeyEndX[si], s.Top(pr.KeyEndX[si]), pr.KeyEndX[si], s.Bottom(pr.KeyEndX[si]), color.RGBA{0, 160, 0, 255})
	}
	for si, sys := range pr.Systems {
		top := pr.Staves[sys[0]]
		bot := pr.Staves[sys[len(sys)-1]]
		for _, bx := range pr.Barlines[si] {
			c.Line(bx+2, top.Top(bx)-10, bx+2, bot.Bottom(bx)+10, color.RGBA{0, 0, 255, 255})
		}
		fmt.Printf("system %d bars %d\n", si, len(pr.Barlines[si]))
	}
	for _, h := range pr.Heads {
		col := color.RGBA{255, 0, 0, 255}
		if h.Hollow {
			col = color.RGBA{255, 140, 0, 255}
		}
		if h.Small {
			col = color.RGBA{200, 0, 200, 255}
		}
		if h.Pass == 2 {
			col = color.RGBA{0, 120, 255, 255}
		}
		if h.Pass == 3 {
			col = color.RGBA{0, 200, 100, 255}
		}
		c.Dot(h.X, h.Y, 4, col)
		if h.StemDir != 0 {
			c.Line(h.OnsetX, h.Y-8, h.OnsetX, h.Y+8, color.RGBA{0, 150, 0, 255})
		}
		if h.Acc != 0 {
			c.Dot(h.X-14, h.Y, 3, color.RGBA{0, 0, 255, 255})
		}
	}
	for _, a := range pr.Accs {
		col := map[int]color.RGBA{1: {0, 200, 200, 255}, -1: {0, 0, 255, 255}, 2: {150, 0, 255, 255}}[a.Kind]
		c.Rect(float64(a.X0), a.AnchorY-6, float64(a.X1), a.AnchorY+6, col)
	}
	for k, r := range pr.Ottava {
		st := pr.Staves[pr.OttavaSt[k]]
		y := st.Top(r[0]) - 30
		c.Line(r[0], y, r[1], y, color.RGBA{255, 0, 255, 255})
		c.Line(r[0], y+1, r[1], y+1, color.RGBA{255, 0, 255, 255})
		fmt.Printf("8va staff %d x %.0f-%.0f\n", pr.OttavaSt[k], r[0], r[1])
	}
	f, _ := os.Create(*out)
	png.Encode(f, c)
	f.Close()
}
