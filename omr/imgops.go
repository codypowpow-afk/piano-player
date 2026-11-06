package omr

import "sort"

// Downscale shrinks a bitmap by a real factor (>1) using area coverage.
func Downscale(b *Bitmap, f float64) *Bitmap {
	if f <= 1.05 {
		return b
	}
	w, h := int(float64(b.W)/f), int(float64(b.H)/f)
	out := &Bitmap{W: w, H: h, Pix: make([]uint8, w*h)}
	// column sums per source row band
	x0s := make([]int, w+1)
	for x := 0; x <= w; x++ {
		x0s[x] = min(b.W, int(float64(x)*f+0.5))
	}
	acc := make([]int, w)
	for y := 0; y < h; y++ {
		sy0, sy1 := int(float64(y)*f+0.5), min(b.H, int(float64(y+1)*f+0.5))
		for i := range acc {
			acc[i] = 0
		}
		for sy := sy0; sy < sy1; sy++ {
			row := b.Pix[sy*b.W : (sy+1)*b.W]
			for x := 0; x < w; x++ {
				for sx := x0s[x]; sx < x0s[x+1]; sx++ {
					acc[x] += int(row[sx])
				}
			}
		}
		for x := 0; x < w; x++ {
			area := (sy1 - sy0) * (x0s[x+1] - x0s[x])
			if area > 0 && acc[x]*100 >= area*30 {
				out.Pix[y*w+x] = 1
			}
		}
	}
	return out
}

func (b *Bitmap) Clone() *Bitmap {
	return &Bitmap{W: b.W, H: b.H, Pix: append([]uint8(nil), b.Pix...)}
}

// runStats returns the most common vertical black run (line thickness) and
// the most common black+white period (staff space), sampled over columns.
func runStats(b *Bitmap) (thick, space int) {
	black := map[int]int{}
	period := map[int]int{}
	for x := b.W / 10; x < b.W*9/10; x += max(1, b.W/400) {
		y := 0
		var lastBlackStart = -1
		for y < b.H {
			if b.Pix[y*b.W+x] == 1 {
				s := y
				for y < b.H && b.Pix[y*b.W+x] == 1 {
					y++
				}
				black[y-s]++
				if lastBlackStart >= 0 {
					period[s-lastBlackStart]++
				}
				lastBlackStart = s
			} else {
				y++
			}
		}
	}
	thick = argmax(black)
	// staff period: most common period larger than 2*thick
	best, bv := 0, 0
	for p, c := range period {
		if p > 2*thick+2 && c > bv {
			best, bv = p, c
		}
	}
	return thick, best
}

func argmax(m map[int]int) int {
	best, bv := 0, -1
	for k, v := range m {
		if v > bv || (v == bv && k < best) {
			best, bv = k, v
		}
	}
	return best
}

// integral image of ink
type integral struct {
	w, h int
	s    []int32
}

func newIntegral(b *Bitmap) *integral {
	w, h := b.W+1, b.H+1
	s := make([]int32, w*h)
	for y := 1; y < h; y++ {
		var row int32
		for x := 1; x < w; x++ {
			row += int32(b.Pix[(y-1)*b.W+x-1])
			s[y*w+x] = s[(y-1)*w+x] + row
		}
	}
	return &integral{w, h, s}
}

// sum of ink in [x0,x1) x [y0,y1)
func (it *integral) sum(x0, y0, x1, y1 int) int {
	x0, y0 = clamp(x0, 0, it.w-1), clamp(y0, 0, it.h-1)
	x1, y1 = clamp(x1, 0, it.w-1), clamp(y1, 0, it.h-1)
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	return int(it.s[y1*it.w+x1] - it.s[y0*it.w+x1] - it.s[y1*it.w+x0] + it.s[y0*it.w+x0])
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Component is a connected group of ink pixels.
type Component struct {
	ID             int
	X0, Y0, X1, Y1 int // inclusive-exclusive bbox
	Area           int
}

func (c *Component) W() int { return c.X1 - c.X0 }
func (c *Component) H() int { return c.Y1 - c.Y0 }

// label computes 8-connected components. lab[i] = component index+1.
func label(b *Bitmap) ([]int32, []*Component) {
	lab := make([]int32, len(b.Pix))
	var comps []*Component
	stack := make([]int32, 0, 4096)
	for i, p := range b.Pix {
		if p == 0 || lab[i] != 0 {
			continue
		}
		id := int32(len(comps) + 1)
		c := &Component{ID: int(id), X0: b.W, Y0: b.H}
		lab[i] = id
		stack = append(stack[:0], int32(i))
		for len(stack) > 0 {
			k := int(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
			x, y := k%b.W, k/b.W
			c.Area++
			if x < c.X0 {
				c.X0 = x
			}
			if x+1 > c.X1 {
				c.X1 = x + 1
			}
			if y < c.Y0 {
				c.Y0 = y
			}
			if y+1 > c.Y1 {
				c.Y1 = y + 1
			}
			for dy := -1; dy <= 1; dy++ {
				ny := y + dy
				if ny < 0 || ny >= b.H {
					continue
				}
				for dx := -1; dx <= 1; dx++ {
					nx := x + dx
					if nx < 0 || nx >= b.W {
						continue
					}
					j := ny*b.W + nx
					if b.Pix[j] == 1 && lab[j] == 0 {
						lab[j] = id
						stack = append(stack, int32(j))
					}
				}
			}
		}
		comps = append(comps, c)
	}
	return lab, comps
}

// fillHoles returns a copy where enclosed background regions smaller than
// maxArea are filled.
func fillHoles(b *Bitmap, maxArea int) *Bitmap { return fillHolesShaped(b, maxArea, 0, 1<<30) }

// fillHolesShaped fills only holes at least minH tall and at most maxW wide
// (the inside of a hollow notehead, not a thin sliver between head and beam).
func fillHolesShaped(b *Bitmap, maxArea, minH, maxW int) *Bitmap {
	out := b.Clone()
	seen := make([]bool, len(b.Pix))
	var stack []int
	var region []int
	for i, p := range b.Pix {
		if p == 1 || seen[i] {
			continue
		}
		region = region[:0]
		touches := false
		seen[i] = true
		stack = append(stack[:0], i)
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			region = append(region, k)
			x, y := k%b.W, k/b.W
			if x == 0 || y == 0 || x == b.W-1 || y == b.H-1 {
				touches = true
			}
			// 4-connectivity for background
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= b.W || ny >= b.H {
					continue
				}
				j := ny*b.W + nx
				if b.Pix[j] == 0 && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
			if len(region) > maxArea {
				touches = true // too big; keep exploring to mark seen
			}
		}
		if !touches {
			x0, y0, x1, y1 := b.W, b.H, 0, 0
			for _, k := range region {
				x, y := k%b.W, k/b.W
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
			if y1-y0+1 >= minH && x1-x0+1 <= maxW {
				for _, k := range region {
					out.Pix[k] = 1
				}
			}
		}
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}
