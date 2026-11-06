package omr

import (
	"fmt"
	"math"
	"sort"
)

// Clef kinds
const (
	ClefTreble = iota
	ClefBass
)

// Head is one recognised notehead.
type Head struct {
	X, Y    float64 // centre (working resolution)
	Staff   int     // staff index on the page
	Pos     int     // staff position: half-spaces above the bottom line
	Hollow  bool
	Small   bool
	StemX   float64
	StemDir int // +1 up, -1 down, 0 none
	Acc     int // explicit accidental: 0 none, 1 sharp, -1 flat, 2 natural
	OnsetX  float64
	W       float64
	Octave  int // 8va shift in octaves
	merged  bool
	Pass    int
	TiedIn  bool
}

// Accidental glyph found on the page.
type Accidental struct {
	Kind    int // 1 sharp, -1 flat, 2 natural
	X0, X1  int
	AnchorY float64
	Staff   int
	Pos     int
	Used    bool
}

// ClefChange marks a clef that applies from X onward on one staff.
type ClefChange struct {
	X    float64
	Clef int
}

// PageResult is everything recognised on one page.
type PageResult struct {
	Img      *Bitmap // working-resolution page
	Clean    *Bitmap // staff lines removed
	Space    float64
	Thick    int
	Staves   []*Staff
	Systems  [][]int // staff indices per system (top to bottom)
	Clefs    [][]ClefChange
	KeySig   []int // per staff: +n sharps / -n flats
	KeyEndX  []float64
	Barlines [][]float64 // per system
	Heads    []*Head
	Accs     []*Accidental
	Ottava   [][2]float64 // x ranges of 8va lines
	OttavaSt []int        // staff each range applies to
	KeyFound []bool

	closed      *Bitmap
	filled      *Bitmap
	closedLab   []int32
	closedComps []*Component
}

const workSpace = 20.0

// experimentalPasses enables extra notehead passes for heads fused with beams
// (they currently add more false hits than they find).
const experimentalPasses = false

var debugKey = false

// AnalyzePage runs recognition on one full-resolution page bitmap.
func AnalyzePage(full *Bitmap) *PageResult {
	_, sp := runStats(full)
	if sp < 6 {
		return &PageResult{}
	}
	b := Downscale(full, float64(sp)/workSpace)
	thick, space := runStats(b)
	staves := findStaves(b, thick, space)
	pr := &PageResult{Img: b, Thick: thick, Staves: staves}
	if len(staves) == 0 {
		return pr
	}
	var spaces []float64
	for _, s := range staves {
		spaces = append(spaces, s.Space)
	}
	pr.Space = median(spaces)
	pr.Clean = removeStaffLines(b, staves, thick)
	pr.groupSystems()
	pr.findHeads()
	pr.findStems()
	pr.findBarlines()
	pr.findClefsAndKeys()
	pr.findAccidentals()
	pr.localAccidentals()
	pr.findOttava()
	pr.dropPreamble()
	pr.assignPositions()
	return pr
}

func (pr *PageResult) groupSystems() {
	n := len(pr.Staves)
	if n%2 == 0 {
		for i := 0; i < n; i += 2 {
			pr.Systems = append(pr.Systems, []int{i, i + 1})
		}
		return
	}
	// odd: pair staves whose gap is small, leave the rest single
	for i := 0; i < n; {
		if i+1 < n {
			gap := pr.Staves[i+1].Top(float64(pr.Staves[i+1].X0+pr.Staves[i+1].X1)/2) - pr.Staves[i].Bottom(float64(pr.Staves[i].X0+pr.Staves[i].X1)/2)
			if gap < pr.Space*7 {
				pr.Systems = append(pr.Systems, []int{i, i + 1})
				i += 2
				continue
			}
		}
		pr.Systems = append(pr.Systems, []int{i})
		i++
	}
}

// staffAt returns the staff whose vertical zone contains y (and x within it).
func (pr *PageResult) staffAt(x, y float64) int {
	best, bd := -1, math.Inf(1)
	for i, s := range pr.Staves {
		if x < float64(s.X0)-pr.Space || x > float64(s.X1)+pr.Space {
			continue
		}
		top, bot := s.Top(x), s.Bottom(x)
		var d float64
		if y < top {
			d = top - y
		} else if y > bot {
			d = y - bot
		}
		if d < bd {
			best, bd = i, d
		}
	}
	if bd > pr.Space*6.5 {
		return -1
	}
	return best
}

// keepRuns keeps only pixels lying in horizontal (or vertical) runs of at least k.
func keepRuns(b *Bitmap, k int, horizontal bool) *Bitmap {
	out := &Bitmap{W: b.W, H: b.H, Pix: make([]uint8, len(b.Pix))}
	if horizontal {
		for y := 0; y < b.H; y++ {
			row := y * b.W
			for x := 0; x < b.W; {
				if b.Pix[row+x] == 0 {
					x++
					continue
				}
				s := x
				for x < b.W && b.Pix[row+x] == 1 {
					x++
				}
				if x-s >= k {
					for i := s; i < x; i++ {
						out.Pix[row+i] = 1
					}
				}
			}
		}
	} else {
		for x := 0; x < b.W; x++ {
			for y := 0; y < b.H; {
				if b.Pix[y*b.W+x] == 0 {
					y++
					continue
				}
				s := y
				for y < b.H && b.Pix[y*b.W+x] == 1 {
					y++
				}
				if y-s >= k {
					for i := s; i < y; i++ {
						out.Pix[i*b.W+x] = 1
					}
				}
			}
		}
	}
	return out
}

func (pr *PageResult) findHeads() {
	pr.findHeadsPass(pr.holeFilled(), 0.38, passHollow)
	pr.findHeadsPass(pr.Clean, 0.38, passFilled)
	if experimentalPasses {
		pr.findHeadsPass(pr.Clean, 0.8, passSecond)
		pr.findMergedHeads()
	}
	// beams are not noteheads: reject blobs whose rows run on horizontally
	var out []*Head
	for _, h := range pr.Heads {
		if !pr.beamLike(h.X, h.Y) {
			out = append(out, h)
		}
	}
	pr.Heads = out
}

// hrun is the horizontal ink run through (x,y) in the clean image.
func (pr *PageResult) hrun(x, y int) int {
	b := pr.Clean
	if b.At(x, y) == 0 {
		return 0
	}
	s, e := x, x
	for s > 0 && b.At(s-1, y) == 1 {
		s--
	}
	for e < b.W-1 && b.At(e+1, y) == 1 {
		e++
	}
	return e - s + 1
}

// beamLike: every row through the centre band is a long horizontal run.
func (pr *PageResult) beamLike(x, y float64) bool {
	sp := pr.Space
	mn := 1 << 30
	for _, dy := range []float64{-0.28, 0, 0.28} {
		best := 0
		for dx := -3; dx <= 3; dx++ {
			best = max(best, pr.hrun(int(x)+dx, int(y+dy*sp)))
		}
		mn = min(mn, best)
	}
	return float64(mn) > sp*2.1
}

// findMergedHeads finds noteheads fused with the beam right under/over them
// (short stems in low bass notes): a tall vertical opening keeps only the
// head+beam overlap, and the head is the end that is not part of the beam.
func (pr *PageResult) findMergedHeads() {
	sp := pr.Space
	filled := pr.holeFilled()
	o := keepRuns(filled, int(sp*0.45+0.5), true)
	o = keepRuns(o, int(sp*1.15+0.5), false)
	o = keepRuns(o, int(sp*0.6+0.5), true)
	_, comps := label(o)
	for _, c := range comps {
		w, h := float64(c.W()), float64(c.H())
		if w < sp*0.75 || w > sp*1.8 || h < sp*1.15 || h > sp*2.3 {
			continue
		}
		cx := float64(c.X0+c.X1) / 2
		// which end continues sideways (the beam)?
		topRun := pr.hrun(int(cx), c.Y0+int(sp*0.25))
		botRun := pr.hrun(int(cx), c.Y1-1-int(sp*0.25))
		var cy float64
		switch {
		case float64(botRun) > sp*2 && float64(topRun) < sp*1.9:
			cy = float64(c.Y0) + sp*0.48
		case float64(topRun) > sp*2 && float64(botRun) < sp*1.9:
			cy = float64(c.Y1) - sp*0.48
		default:
			continue
		}
		st := pr.staffAt(cx, cy)
		if st < 0 {
			continue
		}
		dup := false
		for _, o := range pr.Heads {
			if math.Abs(o.X-cx) < sp*0.8 && math.Abs(o.Y-cy) < sp*0.7 {
				dup = true
				break
			}
		}
		if !dup {
			pr.Heads = append(pr.Heads, &Head{X: cx, Y: cy, Staff: st, W: w, merged: true, Pass: 3})
		}
	}
}

func (pr *PageResult) holeFilled() *Bitmap {
	if pr.filled == nil {
		sp := pr.Space
		pr.filled = fillHolesShaped(pr.Clean, int(sp*sp*0.9), int(sp*0.26+0.5), int(sp*1.4))
	}
	return pr.filled
}

const (
	passFilled = iota
	passHollow
	passSecond
)

func (pr *PageResult) findHeadsPass(src *Bitmap, vk float64, mode int) {
	sp := pr.Space
	second := mode == passSecond
	o := keepRuns(src, int(sp*0.32+0.5), true)
	o = keepRuns(o, int(sp*vk+0.5), false)
	o = keepRuns(o, int(sp*0.32+0.5), true)
	if second {
		o = keepRuns(o, int(sp*0.6+0.5), true)
	}
	lab, comps := label(o)
	cleanInt := newIntegral(pr.Clean)
	for _, c := range comps {
		w, h := float64(c.W()), float64(c.H())
		if w < sp*0.62 || h < sp*0.55 || w > sp*3.2 || h > sp*7 {
			continue
		}
		fill := float64(c.Area) / (w * h)
		if fill < 0.5 {
			continue
		}
		var parts [][2]int // x ranges
		if w > sp*1.95 {
			mid := c.X0 + c.W()/2
			parts = [][2]int{{c.X0, mid}, {mid, c.X1}}
		} else {
			parts = [][2]int{{c.X0, c.X1}}
		}
		for _, pt := range parts {
			// row width profile inside this part
			var prof []float64
			y0, y1 := c.Y1, c.Y0
			for y := c.Y0; y < c.Y1; y++ {
				n := 0
				for x := pt[0]; x < pt[1]; x++ {
					if lab[y*o.W+x] == int32(c.ID) {
						n++
					}
				}
				if n > 0 {
					y0 = min(y0, y)
					y1 = max(y1, y+1)
				}
				prof = append(prof, float64(n))
			}
			if y1 <= y0 {
				continue
			}
			ph := float64(y1 - y0)
			pw := float64(pt[1] - pt[0])
			if ph < sp*0.55 || pw < sp*0.62 {
				continue
			}
			small := ph < sp*0.78 && pw < sp*1.05
			if !small && pw < sp*0.85 {
				continue
			}
			unit := sp
			if small {
				unit = sp * 0.72
			}
			n := int(math.Round((ph-unit*0.95)/unit)) + 1
			if n < 1 {
				n = 1
			}
			var cys []float64
			if n == 1 {
				cys = []float64{float64(y0+y1) / 2}
			} else {
				// peaks in smoothed profile
				sm := make([]float64, len(prof))
				for i := range prof {
					s, k := 0.0, 0
					for j := max(0, i-2); j <= min(len(prof)-1, i+2); j++ {
						s += prof[j]
						k++
					}
					sm[i] = s / float64(k)
				}
				type pk struct {
					y float64
					v float64
				}
				var pks []pk
				for i := 1; i+1 < len(sm); i++ {
					if sm[i] >= sm[i-1] && sm[i] > sm[i+1] {
						pks = append(pks, pk{float64(c.Y0 + i), sm[i]})
					}
				}
				sort.Slice(pks, func(a, b int) bool { return pks[a].v > pks[b].v })
				for _, p := range pks {
					ok := p.y > float64(y0)+unit*0.3 && p.y < float64(y1)-unit*0.3
					for _, q := range cys {
						if math.Abs(q-p.y) < unit*0.7 {
							ok = false
						}
					}
					if ok {
						cys = append(cys, p.y)
					}
				}
				if len(cys) != n {
					cys = cys[:0]
					for i := 0; i < n; i++ {
						cys = append(cys, float64(y0)+unit*0.475+float64(i)*(ph-unit*0.95)/float64(n-1))
					}
				}
			}
			for _, cy := range cys {
				// centre x of the ink in this row band
				sx, cnt := 0.0, 0
				for y := int(cy - unit*0.25); y <= int(cy+unit*0.25); y++ {
					if y < 0 || y >= o.H {
						continue
					}
					for x := pt[0]; x < pt[1]; x++ {
						if lab[y*o.W+x] == int32(c.ID) {
							sx += float64(x)
							cnt++
						}
					}
				}
				if cnt == 0 {
					continue
				}
				cx := sx / float64(cnt)
				st := pr.staffAt(cx, cy)
				if st < 0 {
					continue
				}
				s := pr.Staves[st]
				if cx < float64(s.X0)+sp*0.5 || cx > float64(s.X1) {
					continue
				}
				// hollow test: ink in a small central box of the unfilled image
				r := int(unit * 0.16)
				box := (2*r + 1) * (2*r + 1)
				ink := cleanInt.sum(int(cx)-r, int(cy)-r, int(cx)+r+1, int(cy)+r+1)
				hollow := float64(ink) < float64(box)*0.55
				if small && hollow {
					continue
				}
				if mode == passFilled && hollow {
					continue
				}
				if mode == passHollow && !hollow {
					continue
				}
				{
					if second && (small || hollow || pw > sp*1.7 || ph > sp*1.3) {
						continue
					}
					dup := false
					for _, o := range pr.Heads {
						if math.Abs(o.X-cx) < sp*0.8 && math.Abs(o.Y-cy) < sp*0.7 {
							dup = true
							break
						}
					}
					if dup {
						continue
					}
				}
				pr.Heads = append(pr.Heads, &Head{X: cx, Y: cy, Staff: st, Hollow: hollow, Small: small, W: pw, Pass: mode})
			}
		}
	}
}

// vrun traces a vertical line through (x,y) in the clean image, tolerating
// 1-px sideways wobble and small gaps where staff lines were erased.
func (pr *PageResult) vrun(x, y int) (int, int) {
	b := pr.Clean
	if b.At(x, y) == 0 {
		return y, y - 1
	}
	maxGap := pr.Thick + 2
	trace := func(dir int) int {
		cx, cy, last := x, y, y
		gap := 0
		for {
			ny := cy + dir
			if ny < 0 || ny >= b.H {
				return last
			}
			switch {
			case b.At(cx, ny) == 1:
			case b.At(cx-1, ny) == 1:
				cx--
			case b.At(cx+1, ny) == 1:
				cx++
			default:
				gap++
				if gap > maxGap {
					return last
				}
				cy = ny
				continue
			}
			if abs(cx-x) > 3 {
				return last
			}
			gap = 0
			cy, last = ny, ny
		}
	}
	return trace(-1), trace(1)
}

func (pr *PageResult) findStems() {
	sp := pr.Space
	var kept []*Head
	for _, h := range pr.Heads {
		unit := sp
		small := h.Small
		if h.Small {
			unit = sp * 0.72
		}
		bestLen, bestX, bestTop, bestBot := 0, 0, 0, 0
		for x := int(h.X - unit*0.95); x <= int(h.X+unit*0.95); x++ {
			// the stem must touch the head's row band
			for y := int(h.Y - unit*0.45); y <= int(h.Y+unit*0.45); y += 2 {
				s, e := pr.vrun(x, y)
				if l := e - s + 1; l > bestLen {
					// stems are thin: check width at mid-run
					mid := (s + e) / 2
					wd := 0
					for dx := -4; dx <= 4; dx++ {
						if pr.Clean.At(x+dx, mid) == 1 {
							wd++
						}
					}
					if wd <= int(sp*0.3)+1 {
						bestLen, bestX, bestTop, bestBot = l, x, s, e
					}
				}
			}
		}
		minLen := unit * 2.2
		if h.merged {
			minLen = unit * 1.0
		}
		if float64(bestLen) >= unit*1.3 && float64(bestLen) < minLen {
			// short stem: fine if it ends in a beam (wide ink at the far end)
			farY := bestBot - 2
			if h.Y-float64(bestTop) > float64(bestBot)-h.Y {
				farY = bestTop + 2
			}
			wd := 0
			for dx := -int(sp * 2); dx <= int(sp*2); dx++ {
				if pr.Clean.At(bestX+dx, farY) == 1 {
					wd++
				}
			}
			if float64(wd) >= sp*1.5 {
				minLen = float64(bestLen)
			}
		}
		if float64(bestLen) > sp*12 {
			bestLen = 0 // a barline, not a stem
		}
		if float64(bestLen) >= minLen {
			h.StemX = float64(bestX)
			up := h.Y-float64(bestTop) > float64(bestBot)-h.Y
			stemLeft := h.StemX < h.X
			if (stemLeft && up) || (!stemLeft && !up) {
				// only valid for a chord head displaced to the other side of the
				// stem; otherwise this is a flag (or a beam end)
				partner := false
				for _, o := range pr.Heads {
					if o != h && o.Staff == h.Staff && math.Abs(o.Y-h.Y) < unit*0.85 &&
						((stemLeft && o.X < h.StemX) || (!stemLeft && o.X > h.StemX)) && math.Abs(o.X-h.X) < unit*2 {
						partner = true
						break
					}
				}
				if !partner {
					continue
				}
			}
			if small && math.Abs(h.StemX-h.X) < h.W*0.25 {
				continue // stem through the middle: not a notehead
			}
			if up {
				h.StemDir = 1
				h.OnsetX = h.StemX - unit*0.62
			} else {
				h.StemDir = -1
				h.OnsetX = h.StemX + unit*0.62
			}
		} else {
			if !h.Hollow {
				continue // filled head with no stem: not a note
			}
			// a whole note: wide, near its staff
			s := pr.Staves[h.Staff]
			pos := s.Pos(h.X, h.Y)
			if h.W < sp*1.3 || pos < -6 || pos > 14 {
				continue
			}
			h.OnsetX = h.X
		}
		kept = append(kept, h)
	}
	// remove duplicates (same place)
	sort.Slice(kept, func(i, j int) bool { return kept[i].X < kept[j].X })
	var out []*Head
	for _, h := range kept {
		dup := false
		for _, o := range out {
			if math.Abs(o.X-h.X) < sp*0.5 && math.Abs(o.Y-h.Y) < sp*0.4 {
				dup = true
			}
		}
		if !dup {
			out = append(out, h)
		}
	}
	pr.Heads = out
}

func (pr *PageResult) findBarlines() {
	sp := pr.Space
	pr.Barlines = make([][]float64, len(pr.Systems))
	for si, sys := range pr.Systems {
		first, last := pr.Staves[sys[0]], pr.Staves[sys[len(sys)-1]]
		x0 := max(first.X0, last.X0)
		x1 := min(first.X1, last.X1)
		coverage := func(x int, y0, y1 float64) float64 {
			n, tot := 0, 0
			for y := int(y0 + 1); y <= int(y1-1); y++ {
				tot++
				if pr.Clean.At(x, y) == 1 || pr.Clean.At(x-1, y) == 1 || pr.Clean.At(x+1, y) == 1 {
					n++
				}
			}
			if tot == 0 {
				return 0
			}
			return float64(n) / float64(tot)
		}
		type cand struct {
			x      int
			strong bool
		}
		var cols []cand
		for x := x0; x <= x1+2; x++ {
			ok := true
			for _, st := range sys {
				s := pr.Staves[st]
				if coverage(x, s.Top(float64(x)), s.Bottom(float64(x))) < 0.93 {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			// thin at mid-staff
			s0 := pr.Staves[sys[0]]
			y := int(s0.Top(float64(x)) + sp)
			wd := 0
			for dx := -int(sp); dx <= int(sp); dx++ {
				if pr.Clean.At(x+dx, y) == 1 {
					wd++
				}
			}
			if float64(wd) > sp*0.8 {
				continue
			}
			strong := len(sys) > 1 && coverage(x, first.Top(float64(x)), last.Bottom(float64(x))) > 0.97
			cols = append(cols, cand{x, strong})
		}
		var bars []float64
		for i := 0; i < len(cols); {
			j := i
			strong := cols[i].strong
			for j+1 < len(cols) && cols[j+1].x-cols[j].x <= 2 {
				j++
				strong = strong || cols[j].strong
			}
			bx := float64(cols[i].x+cols[j].x) / 2
			i = j + 1
			if !strong {
				near := false
				for _, h := range pr.Heads {
					if !inSys(h.Staff, sys) {
						continue
					}
					if math.Abs(h.StemX-bx) < 3 || math.Abs(h.X-bx) < sp*0.9 {
						near = true
						break
					}
				}
				if near {
					continue
				}
			}
			if len(bars) > 0 && bx-bars[len(bars)-1] < sp*1.5 {
				bars[len(bars)-1] = bx // double bar: keep the right one
				continue
			}
			bars = append(bars, bx)
		}
		pr.Barlines[si] = bars
	}
}

func inSys(st int, sys []int) bool {
	for _, s := range sys {
		if s == st {
			return true
		}
	}
	return false
}

// glyphAt collects the connected component of the clean image under (x,y)
// limited to a window, returning its bbox and pixel mask.
type glyph struct {
	x0, y0, x1, y1 int
	mask           map[int]bool
	w              int
}

func (pr *PageResult) components() ([]int32, []*Component) {
	if pr.closed == nil {
		pr.closed = closeVertical(pr.Clean, int(pr.Space*0.3+0.5))
		pr.closedLab, pr.closedComps = label(pr.closed)
	}
	return pr.closedLab, pr.closedComps
}

// closeVertical fills vertical gaps of up to k pixels between ink.
func closeVertical(b *Bitmap, k int) *Bitmap {
	out := b.Clone()
	for x := 0; x < b.W; x++ {
		last := -1
		for y := 0; y < b.H; y++ {
			if b.Pix[y*b.W+x] == 1 {
				if last >= 0 && y-last > 1 && y-last <= k+1 {
					for yy := last + 1; yy < y; yy++ {
						out.Pix[yy*b.W+x] = 1
					}
				}
				last = y
			}
		}
	}
	return out
}

func (pr *PageResult) findClefsAndKeys() {
	sp := pr.Space
	lab, comps := pr.components()
	pr.Clefs = make([][]ClefChange, len(pr.Staves))
	pr.KeySig = make([]int, len(pr.Staves))
	pr.KeyEndX = make([]float64, len(pr.Staves))
	pr.KeyFound = make([]bool, len(pr.Staves))
	for si, s := range pr.Staves {
		x0 := float64(s.X0)
		top, bot := s.Top(x0+sp*2), s.Bottom(x0+sp*2)
		// clef: the glyph pieces starting right at the staff's beginning
		var clef *Component
		for _, c := range comps {
			if float64(c.X0) < x0+sp*0.3 || float64(c.X0) > x0+sp*1.7 || float64(c.X1) > x0+sp*4 {
				continue
			}
			if float64(c.Y1) < top-sp*1.5 || float64(c.Y0) > bot+sp*1.5 || c.H() < int(sp*0.8) {
				continue
			}
			if clef == nil {
				cc := *c
				clef = &cc
			} else {
				clef.X0, clef.X1 = min(clef.X0, c.X0), max(clef.X1, c.X1)
				clef.Y0, clef.Y1 = min(clef.Y0, c.Y0), max(clef.Y1, c.Y1)
				clef.Area += c.Area
			}
		}
		kind := ClefTreble
		if si%2 == 1 {
			kind = ClefBass
		}
		if clef != nil {
			cy0, cy1 := float64(clef.Y0), float64(clef.Y1)
			if cy1-cy0 > sp*4.6 && cy0 < top && cy1 > bot+sp*0.6 {
				kind = ClefTreble
			} else if cy1-cy0 < sp*4.0 && cy0 > top-sp*0.6 && cy1 < bot-sp*0.3 {
				kind = ClefBass
			}
		}
		pr.Clefs[si] = []ClefChange{{X: 0, Clef: kind}}
		if debugKey && clef != nil {
			fmt.Printf("staff %d X0 %d clef x %d-%d y %d-%d (top %.0f bot %.0f)\n", si, s.X0, clef.X0, clef.X1, clef.Y0, clef.Y1, top, bot)
		}
		// key signature: column clusters after the clef
		key, keyEnd, found := pr.keyByProfile(si, kind, clef)
		pr.KeyEndX[si] = keyEnd
		pr.KeyFound[si] = found
		pr.KeySig[si] = key
	}
	_ = lab
	pr.findClefChanges(comps)
}

// keyPos gives the staff position of the i-th sharp/flat of a key signature.
func keyPos(clef, kind, i int) int {
	flats := []int{4, 7, 3, 6, 2, 5, 1}  // treble: Bb Eb Ab Db Gb Cb Fb
	sharps := []int{8, 5, 9, 6, 3, 7, 4} // treble: F# C# G# D# A# E# B#
	p := flats[i]
	if kind == 1 {
		p = sharps[i]
	}
	if clef == ClefBass {
		p -= 2
	}
	return p
}

// classifyAcc decides whether a component is a sharp (1), flat (-1) or
// natural (2); 0 if none. It also returns the y the accidental refers to.
func classifyAcc(b *Bitmap, lab []int32, c *Component, sp float64) (int, float64) {
	w, h := c.W(), c.H()
	if float64(h) < sp*1.5 || float64(h) > sp*3.9 || float64(w) > sp*1.5 || float64(w) < sp*0.35 {
		return 0, 0
	}
	id := int32(c.ID)
	type stroke struct{ x, top, bot int }
	var strokes []stroke
	for x := c.X0; x < c.X1; x++ {
		bs, bt, be := 0, 0, 0
		for y := c.Y0; y < c.Y1; {
			if lab[y*b.W+x] != id {
				y++
				continue
			}
			s := y
			for y < c.Y1 && lab[y*b.W+x] == id {
				y++
			}
			if y-s > bs {
				bs, bt, be = y-s, s, y
			}
		}
		if float64(bs) >= float64(h)*0.55 {
			if len(strokes) > 0 && x-strokes[len(strokes)-1].x <= 2 {
				st := &strokes[len(strokes)-1]
				if be-bt > st.bot-st.top {
					st.top, st.bot = bt, be
				}
				st.x = x
				continue
			}
			strokes = append(strokes, stroke{x, bt, be})
		}
	}
	// ink distribution: top half vs bottom half width
	widthAt := func(y0, y1 int) int {
		mx := 0
		for y := y0; y < y1; y++ {
			n := 0
			for x := c.X0; x < c.X1; x++ {
				if lab[y*b.W+x] == id {
					n++
				}
			}
			mx = max(mx, n)
		}
		return mx
	}
	switch len(strokes) {
	case 1:
		// flat: stroke on the left, bowl at the bottom right
		st := strokes[0]
		if float64(st.x-c.X0) > float64(w)*0.45 {
			return 0, 0
		}
		topW := widthAt(c.Y0, c.Y0+h/3)
		botW := widthAt(c.Y1-h/3, c.Y1)
		if float64(botW) >= float64(w)*0.6 && topW*10 < botW*7 {
			return -1, float64(c.Y1) - sp*0.55
		}
	case 2:
		a, bb := strokes[0], strokes[1]
		gap := bb.x - a.x
		if float64(gap) < sp*0.25 || float64(gap) > sp*1.0 {
			return 0, 0
		}
		dt := float64(bb.top - a.top)
		db := float64(bb.bot - a.bot)
		if dt > sp*0.45 && db > sp*0.45 {
			return 2, float64(c.Y0+c.Y1) / 2 // natural: right stroke lower
		}
		if math.Abs(dt) < sp*0.6 && math.Abs(db) < sp*0.6 {
			return 1, float64(c.Y0+c.Y1) / 2
		}
	}
	return 0, 0
}

func (pr *PageResult) findAccidentals() {
	sp := pr.Space
	lab, comps := pr.components()
	for _, c := range comps {
		kind, ay := classifyAcc(pr.closed, lab, c, sp)
		if kind == 0 {
			continue
		}
		cx := float64(c.X0+c.X1) / 2
		st := pr.staffAt(cx, ay)
		if st < 0 || cx < pr.KeyEndX[st]+1 {
			continue
		}
		s := pr.Staves[st]
		pos := int(math.Round(s.Pos(cx, ay)))
		pr.Accs = append(pr.Accs, &Accidental{Kind: kind, X0: c.X0, X1: c.X1, AnchorY: ay, Staff: st, Pos: pos})
	}
	// attach to the nearest head to the right on the same position
	for _, a := range pr.Accs {
		var best *Head
		bd := sp * 3.2
		for _, h := range pr.Heads {
			if h.Staff != a.Staff {
				continue
			}
			s := pr.Staves[h.Staff]
			hp := int(math.Round(s.Pos(h.X, h.Y)))
			if hp != a.Pos {
				continue
			}
			d := h.X - float64(a.X1)
			if d < -sp*0.2 || d > bd {
				continue
			}
			best, bd = h, d
		}
		if best != nil && best.Acc == 0 {
			best.Acc = a.Kind
			a.Used = true
		}
	}
}

// findClefChanges looks for clef glyphs in the middle of a staff.
func (pr *PageResult) findClefChanges(comps []*Component) {
	sp := pr.Space
	for si, s := range pr.Staves {
		for _, c := range comps {
			cx := float64(c.X0+c.X1) / 2
			if cx < pr.KeyEndX[si]+sp*2 || cx > float64(s.X1) {
				continue
			}
			top, bot := s.Top(cx), s.Bottom(cx)
			w, h := float64(c.W()), float64(c.H())
			fill := float64(c.Area) / (w * h)
			// treble clef: tall, spans above and below the staff, narrow-ish
			if h > sp*5 && h < sp*8.5 && w > sp*1.2 && w < sp*2.8 &&
				float64(c.Y0) < top-sp*0.5 && float64(c.Y1) > bot+sp*0.3 && fill > 0.18 && fill < 0.5 {
				if !pr.headsInside(c, si) {
					pr.Clefs[si] = append(pr.Clefs[si], ClefChange{X: cx, Clef: ClefTreble})
				}
			}
			// bass clef: within the staff's upper part
			if h > sp*2.2 && h < sp*3.6 && w > sp*1.6 && w < sp*3 &&
				math.Abs(float64(c.Y0)-top) < sp*0.8 && float64(c.Y1) < bot && fill > 0.2 && fill < 0.55 {
				if !pr.headsInside(c, si) {
					pr.Clefs[si] = append(pr.Clefs[si], ClefChange{X: cx, Clef: ClefBass})
				}
			}
		}
		sort.Slice(pr.Clefs[si], func(i, j int) bool { return pr.Clefs[si][i].X < pr.Clefs[si][j].X })
	}
}

func (pr *PageResult) headsInside(c *Component, st int) bool {
	for _, h := range pr.Heads {
		if h.Staff == st && h.X >= float64(c.X0)-2 && h.X <= float64(c.X1)+2 && h.Y >= float64(c.Y0)-2 && h.Y <= float64(c.Y1)+2 {
			return true
		}
	}
	return false
}

func (pr *PageResult) clefAt(st int, x float64) int {
	cl := pr.Clefs[st][0].Clef
	for _, c := range pr.Clefs[st][1:] {
		if x > c.X {
			cl = c.Clef
		}
	}
	return cl
}

func (pr *PageResult) assignPositions() {
	// heads between the two staves of a system: the stem says which staff
	// they belong to (stems point towards their own staff)
	for _, sys := range pr.Systems {
		if len(sys) < 2 {
			continue
		}
		up, lo := pr.Staves[sys[0]], pr.Staves[sys[1]]
		for _, h := range pr.Heads {
			if h.Staff != sys[0] && h.Staff != sys[1] {
				continue
			}
			if h.Y <= up.Bottom(h.X)+pr.Space*0.75 || h.Y >= lo.Top(h.X)-pr.Space*0.75 {
				continue
			}
			switch h.StemDir {
			case -1:
				h.Staff = sys[1]
			case 1:
				h.Staff = sys[0]
			}
		}
	}
	for _, h := range pr.Heads {
		s := pr.Staves[h.Staff]
		h.Pos = int(math.Round(s.Pos(h.X, h.Y)))
	}
}

// keyByProfile reads the key signature of one staff from the vertical strokes
// after the clef: one stroke per flat, a close pair per sharp. Each candidate
// must sit where the next accidental of the standard order belongs.
func (pr *PageResult) keyByProfile(si, clef int, clefComp *Component) (key int, end float64, found bool) {
	sp := pr.Space
	s := pr.Staves[si]
	img := pr.closed
	x0 := s.X0 + int(sp*0.5)
	if clefComp != nil {
		x0 = clefComp.X1 + 1
	}
	xEnd := min(s.X1, x0+int(sp*10))
	type stroke = struct{ x0, x1, top, bot int }
	var strokes []stroke
	var open []int // indices of strokes touched in the previous column
	stop := xEnd
	for x := x0; x < stop; x++ {
		zt := int(s.Top(float64(x)) - sp*2.6)
		zb := int(s.Bottom(float64(x)) + sp*1.6)
		var nowOpen []int
		for y := zt; y <= zb; {
			if img.At(x, y) == 0 {
				y++
				continue
			}
			st := y
			for y <= zb && img.At(x, y) == 1 {
				y++
			}
			l := y - st
			if float64(l) > sp*3.7 {
				if x > x0+int(sp*1.5) {
					stop = x // a note stem or barline ends the signature
				}
				continue
			}
			if float64(l) < sp*1.3 {
				continue
			}
			linked := -1
			for _, k := range open {
				o := strokes[k]
				ov := min(o.bot, y) - max(o.top, st)
				if ov > l/2 && ov > (o.bot-o.top)/2 {
					linked = k
					break
				}
			}
			if linked >= 0 {
				o := &strokes[linked]
				o.x1 = x
				if l > o.bot-o.top {
					o.top, o.bot = st, y
				}
				nowOpen = append(nowOpen, linked)
			} else {
				strokes = append(strokes, stroke{x, x, st, y})
				nowOpen = append(nowOpen, len(strokes)-1)
			}
		}
		open = nowOpen
	}
	// keep thin strokes only (a stroke wider than 0.35 sp is a clef/blob)
	var thin []stroke
	for _, st := range strokes {
		if float64(st.x1-st.x0) <= sp*0.35 {
			thin = append(thin, st)
		}
	}
	strokes = thin
	sort.Slice(strokes, func(i, j int) bool { return strokes[i].x0 < strokes[j].x0 })
	end = float64(x0)
	if len(strokes) == 0 {
		return 0, end, true
	}
	sharps := false
	if len(strokes) >= 2 {
		g := float64(strokes[1].x0 - strokes[0].x1)
		if g < sp*0.6 && math.Abs(float64(strokes[1].top-strokes[0].top)) < sp*0.8 {
			sharps = true
		}
	}
	if debugKey {
		for _, st := range strokes {
			fmt.Printf("  staff %d stroke x %d-%d y %d-%d h %.1fsp posMid %.1f\n", si, st.x0, st.x1, st.top, st.bot, float64(st.bot-st.top)/sp, s.Pos(float64(st.x0), float64(st.bot+st.top)/2))
		}
	}
	n := 0
	if sharps {
		used := make([]bool, len(strokes))
		lastX := strokes[0].x0
		for i := range strokes {
			if used[i] || n >= 7 {
				continue
			}
			a := strokes[i]
			if float64(a.x0-lastX) > sp*1.4 {
				break
			}
			// partner: the next stroke within 0.65 sp that overlaps vertically
			pj := -1
			for j := i + 1; j < len(strokes); j++ {
				b := strokes[j]
				if float64(b.x0-a.x1) > sp*0.65 {
					break
				}
				ov := min(a.bot, b.bot) - max(a.top, b.top)
				if !used[j] && float64(ov) > sp*0.6 {
					pj = j
					break
				}
			}
			if pj < 0 {
				break
			}
			b := strokes[pj]
			ay := float64(a.top+a.bot+b.top+b.bot) / 4
			pos := int(math.Round(s.Pos(float64(a.x0+b.x1)/2, ay)))
			if abs(pos-keyPos(clef, 1, n)) > 1 {
				break
			}
			used[i], used[pj] = true, true
			for k := i + 1; k < pj; k++ {
				used[k] = true
			}
			n++
			lastX = b.x1
			end = math.Max(end, float64(b.x1)+sp*0.3)
		}
		return n, end, true
	}
	strokes = longestRunStrokes(img, s, x0, stop, sp)
	for i, st := range strokes {
		if n >= 7 {
			break
		}
		if i > 0 && float64(st.x0-strokes[i-1].x1) > sp*1.4 {
			break
		}
		want := keyPos(clef, -1, n)
		pb := int(math.Round(s.Pos(float64(st.x0), float64(st.bot)-sp*0.6)))
		pt := int(math.Round(s.Pos(float64(st.x0), float64(st.top)+sp*2.0)))
		if abs(pb-want) > 1 && abs(pt-want) > 1 {
			break
		}
		n++
		end = float64(st.x1) + sp*0.6
	}
	return -n, end, true
}

func longestRunStrokes(img *Bitmap, s *Staff, x0, xEnd int, sp float64) []struct{ x0, x1, top, bot int } {
	var strokes []struct{ x0, x1, top, bot int }
	for x := x0; x < xEnd; x++ {
		zt := int(s.Top(float64(x)) - sp*2.6)
		zb := int(s.Bottom(float64(x)) + sp*1.6)
		best, bt, bb := 0, 0, 0
		for y := zt; y <= zb; {
			if img.At(x, y) == 0 {
				y++
				continue
			}
			st := y
			for y <= zb && img.At(x, y) == 1 {
				y++
			}
			if y-st > best {
				best, bt, bb = y-st, st, y
			}
		}
		if float64(best) >= sp*1.3 && float64(best) <= sp*3.7 {
			if len(strokes) > 0 && x-strokes[len(strokes)-1].x1 <= 2 {
				l := &strokes[len(strokes)-1]
				l.x1 = x
				if bb-bt > l.bot-l.top {
					l.top, l.bot = bt, bb
				}
				continue
			}
			strokes = append(strokes, struct{ x0, x1, top, bot int }{x, x, bt, bb})
		}
	}
	return strokes
}

// dropPreamble removes "notes" found inside the clef / key signature area.
func (pr *PageResult) dropPreamble() {
	sp := pr.Space
	limit := make([]float64, len(pr.Staves))
	for _, sys := range pr.Systems {
		m := 0.0
		for _, st := range sys {
			m = math.Max(m, math.Max(pr.KeyEndX[st], float64(pr.Staves[st].X0)+sp*3.2))
		}
		for _, st := range sys {
			limit[st] = m
		}
	}
	var out []*Head
	for _, h := range pr.Heads {
		if h.X > limit[h.Staff] {
			out = append(out, h)
		}
	}
	pr.Heads = out
}

// localAccidentals looks immediately left of each notehead for the strokes of
// a sharp, flat or natural. It works even when the accidental touches a
// neighbouring stem or rest, which defeats component-based classification.
func (pr *PageResult) localAccidentals() {
	sp := pr.Space
	type stroke struct {
		x        float64
		top, bot int
	}
	for _, h := range pr.Heads {
		if h.Acc != 0 || h.Small {
			continue
		}
		hw := math.Max(h.W/2, sp*0.6)
		xr := int(h.X - hw - sp*0.08)
		xl := int(h.X - hw - sp*1.9)
		// stop at the previous notehead in the same staff (do not read its parts)
		for _, o := range pr.Heads {
			if o != h && o.Staff == h.Staff && o.X < h.X-hw && math.Abs(o.Y-h.Y) < sp*2.5 {
				if ox := int(o.X + math.Max(o.W/2, sp*0.6)); ox > xl && ox < xr {
					xl = ox
				}
			}
		}
		var strokes []stroke
		for x := xr; x >= xl; x-- {
			best := [2]int{0, -1}
			for dy := -0.35; dy <= 0.35; dy += 0.1 {
				s, e := pr.vrun(x, int(h.Y+dy*sp))
				if e-s > best[1]-best[0] {
					best = [2]int{s, e}
				}
			}
			l := float64(best[1] - best[0] + 1)
			if l < sp*1.25 || l > sp*3.4 {
				continue
			}
			if n := len(strokes); n > 0 && strokes[n-1].x-float64(x) <= 2.5 {
				st := &strokes[n-1]
				st.x = float64(x)
				if best[1]-best[0] > st.bot-st.top {
					st.top, st.bot = best[0], best[1]
				}
				continue
			}
			strokes = append(strokes, stroke{float64(x), best[0], best[1]})
		}
		if len(strokes) == 0 {
			continue
		}
		// strokes are ordered right-to-left; the accidental's right stroke is first
		a := strokes[0]
		hy := h.Y
		if len(strokes) >= 2 {
			b := strokes[1] // left stroke
			gap := a.x - b.x
			if gap >= sp*0.22 && gap <= sp*0.75 {
				at, ab := float64(a.top), float64(a.bot)
				bt, bb := float64(b.top), float64(b.bot)
				// natural: left stroke from above down to about the head,
				// right stroke from about the head down below it
				if bt < hy-sp*0.6 && bb > hy-sp*0.5 && bb < hy+sp*0.95 &&
					at > hy-sp*0.95 && at < hy+sp*0.5 && ab > hy+sp*0.6 &&
					at-bt > sp*0.55 && ab-bb > sp*0.55 {
					h.Acc = 2
					continue
				}
				// sharp: two strokes spanning the head, similar extents
				if at < hy-sp*0.5 && bt < hy-sp*0.5 && ab > hy+sp*0.5 && bb > hy+sp*0.5 &&
					math.Abs(at-bt) < sp*0.6 && math.Abs(ab-bb) < sp*0.6 {
					h.Acc = 1
					continue
				}
			}
		}
		// flat: one stroke whose bottom is just below the head centre, with a
		// bowl to its right at head height but nothing at the top
		if float64(a.bot) > hy+sp*0.15 && float64(a.bot) < hy+sp*0.9 && float64(a.top) < hy-sp*1.2 {
			bowl, top := 0, 0
			for x := int(a.x) + 2; x <= int(a.x+sp*0.7); x++ {
				for y := int(hy - sp*0.1); y <= int(hy+sp*0.3); y++ {
					bowl += int(pr.Clean.At(x, y))
				}
				for y := int(hy - sp*1.3); y <= int(hy-sp*0.9); y++ {
					top += int(pr.Clean.At(x, y))
				}
			}
			if float64(bowl) > sp*sp*0.12 && top*3 < bowl {
				h.Acc = -1
			}
		}
	}
}
