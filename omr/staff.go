package omr

import (
	"math"
	"sort"
)

// Staff is one five-line staff, with line heights sampled along x so that
// skewed or slightly curved scans still read correctly.
type Staff struct {
	X0, X1 int
	xs     []float64
	ys     [5][]float64 // line 0 = top
	Space  float64
}

func (s *Staff) LineY(line int, x float64) float64 {
	xs, ys := s.xs, s.ys[line]
	if len(xs) == 1 {
		return ys[0]
	}
	i := sort.SearchFloat64s(xs, x)
	if i <= 0 {
		i = 1
	} else if i >= len(xs) {
		i = len(xs) - 1
	}
	t := (x - xs[i-1]) / (xs[i] - xs[i-1])
	return ys[i-1] + t*(ys[i]-ys[i-1])
}

func (s *Staff) Top(x float64) float64    { return s.LineY(0, x) }
func (s *Staff) Bottom(x float64) float64 { return s.LineY(4, x) }

// Pos returns the staff position of a point in half-spaces above the bottom
// line (0 = bottom line, 8 = top line, negative = below).
func (s *Staff) Pos(x, y float64) float64 {
	top, bot := s.Top(x), s.Bottom(x)
	sp := (bot - top) / 4
	return (bot - y) / (sp / 2)
}

// YOfPos is the inverse of Pos.
func (s *Staff) YOfPos(x, pos float64) float64 {
	top, bot := s.Top(x), s.Bottom(x)
	sp := (bot - top) / 4
	return bot - pos*sp/2
}

func (s *Staff) MidY() float64 { return s.LineY(2, float64(s.X0+s.X1)/2) }

type stripStaff struct {
	x  float64
	ys [5]float64
}

// findStaves locates five-line staves on a page.
func findStaves(b *Bitmap, thick, space int) []*Staff {
	sw := max(space*6, 40)
	var strips [][]stripStaff
	var stripX []float64
	for x0 := 0; x0+sw <= b.W; x0 += sw / 2 {
		cnt := make([]int, b.H)
		for y := 0; y < b.H; y++ {
			row := b.Pix[y*b.W+x0 : y*b.W+x0+sw]
			n := 0
			for _, p := range row {
				n += int(p)
			}
			cnt[y] = n
		}
		// line rows
		var centers []float64
		thr := sw * 55 / 100
		for y := 0; y < b.H; {
			if cnt[y] >= thr {
				s := y
				wsum, sum := 0.0, 0.0
				for y < b.H && cnt[y] >= thr {
					wsum += float64(cnt[y]) * float64(y)
					sum += float64(cnt[y])
					y++
				}
				if y-s <= thick*2+2 {
					centers = append(centers, wsum/sum)
				}
			} else {
				y++
			}
		}
		var cands []stripStaff
		var scores []float64
		sp := float64(space)
		for i := 0; i < len(centers); i++ {
			grp := []float64{centers[i]}
			last := centers[i]
			for j := i + 1; j < len(centers) && len(grp) < 5; j++ {
				d := centers[j] - last
				if d < sp*0.7 {
					continue
				}
				if d > sp*1.3 {
					break
				}
				grp = append(grp, centers[j])
				last = centers[j]
			}
			if len(grp) == 5 && evenGaps(grp) {
				var st stripStaff
				st.x = float64(x0) + float64(sw)/2
				copy(st.ys[:], grp)
				sc := 0.0
				for _, g := range grp {
					sc += float64(cnt[int(g+0.5)])
				}
				cands = append(cands, st)
				scores = append(scores, sc)
			}
		}
		order := make([]int, len(cands))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })
		var found []stripStaff
		for _, i := range order {
			c := cands[i]
			ok := true
			for _, f := range found {
				if c.ys[0] <= f.ys[4]+sp*0.5 && c.ys[4] >= f.ys[0]-sp*0.5 {
					ok = false
				}
			}
			if ok {
				found = append(found, c)
			}
		}
		sort.Slice(found, func(a, b int) bool { return found[a].ys[0] < found[b].ys[0] })
		strips = append(strips, found)
		stripX = append(stripX, float64(x0)+float64(sw)/2)
	}
	sp := float64(space)
	// chain detections across strips (left to right)
	type chain struct {
		pts  []stripStaff
		keys []int
	}
	var chains []*chain
	for k, found := range strips {
		used := map[*chain]bool{}
		for _, d := range found {
			var best *chain
			bd := sp * 0.6
			for _, c := range chains {
				if used[c] || k-c.keys[len(c.keys)-1] > 6 {
					continue
				}
				last := c.pts[len(c.pts)-1]
				pred := last.ys[2]
				if n := len(c.pts); n >= 2 {
					prev := c.pts[n-2]
					slope := (last.ys[2] - prev.ys[2]) / (last.x - prev.x)
					pred += slope * (d.x - last.x)
				}
				if dd := math.Abs(d.ys[2] - pred); dd < bd {
					best, bd = c, dd
				}
			}
			if best == nil {
				best = &chain{}
				chains = append(chains, best)
			}
			used[best] = true
			best.pts = append(best.pts, d)
			best.keys = append(best.keys, k)
		}
	}
	_ = stripX
	sort.Slice(chains, func(i, j int) bool { return len(chains[i].pts) > len(chains[j].pts) })
	var staves []*Staff
	minPts := 2
	for _, c := range chains {
		if len(c.pts) < minPts {
			continue
		}
		// reject if overlapping an accepted (longer) staff
		my := median(func() []float64 {
			var v []float64
			for _, p := range c.pts {
				v = append(v, p.ys[2])
			}
			return v
		}())
		var dup *Staff
		for _, s := range staves {
			if math.Abs(s.LineY(2, c.pts[len(c.pts)/2].x)-my) < sp*4.5 {
				dup = s
			}
		}
		if dup != nil {
			// a broken piece of the same staff: merge points outside its range
			if math.Abs(dup.LineY(2, c.pts[len(c.pts)/2].x)-my) < sp*0.8 {
				for _, p := range c.pts {
					if p.x < dup.xs[0]-1 || p.x > dup.xs[len(dup.xs)-1]+1 {
						dup.xs = append(dup.xs, p.x)
						for l := 0; l < 5; l++ {
							dup.ys[l] = append(dup.ys[l], p.ys[l])
						}
					}
				}
				sortStaffPts(dup)
				smoothStaff(dup)
				dup.X0 = min(dup.X0, int(dup.xs[0]))
				dup.X1 = max(dup.X1, int(dup.xs[len(dup.xs)-1]))
				refineExtent(b, dup, thick)
			}
			continue
		}
		st := &Staff{}
		var spaces []float64
		for _, p := range c.pts {
			st.xs = append(st.xs, p.x)
			for l := 0; l < 5; l++ {
				st.ys[l] = append(st.ys[l], p.ys[l])
			}
			spaces = append(spaces, (p.ys[4]-p.ys[0])/4)
		}
		smoothStaff(st)
		st.Space = median(spaces)
		st.X0 = int(st.xs[0])
		st.X1 = int(st.xs[len(st.xs)-1])
		refineExtent(b, st, thick)
		staves = append(staves, st)
	}
	sort.Slice(staves, func(i, j int) bool { return staves[i].MidY() < staves[j].MidY() })
	// drop duplicates / overlaps
	var out []*Staff
	for _, s := range staves {
		if len(out) > 0 && math.Abs(out[len(out)-1].MidY()-s.MidY()) < s.Space*3 {
			continue
		}
		out = append(out, s)
	}
	return out
}

// refineExtent tracks the five lines outward from the detected range, adding
// control points, to find where the staff really begins and ends.
func refineExtent(b *Bitmap, st *Staff, thick int) {
	step := max(4, int(st.Space/2))
	for dir := -1; dir <= 1; dir += 2 {
		var x int
		var ys [5]float64
		if dir < 0 {
			x = int(st.xs[0])
		} else {
			x = int(st.xs[len(st.xs)-1])
		}
		for l := 0; l < 5; l++ {
			ys[l] = st.LineY(l, float64(x))
		}
		last := x
		gap := 0
		sinceCP := 0
		for {
			x += dir
			if x < 0 || x >= b.W {
				break
			}
			hits := 0
			for l := 0; l < 5; l++ {
				yc := int(math.Round(ys[l]))
				bestDy, found := 0, false
				for r := 0; r <= thick+1 && !found; r++ {
					for _, dy := range []int{r, -r} {
						if b.At(x, yc+dy) == 1 {
							bestDy, found = dy, true
							break
						}
					}
				}
				if found {
					hits++
					// recentre only on a thin run (a plain line)
					y0 := yc + bestDy
					s0, e0 := y0, y0
					for s0 > 0 && b.At(x, s0-1) == 1 && y0-s0 < thick*2 {
						s0--
					}
					for b.At(x, e0+1) == 1 && e0-y0 < thick*2 {
						e0++
					}
					if e0-s0+1 <= thick+2 {
						ys[l] += 0.1 * ((float64(s0+e0) / 2) - ys[l])
					}
				}
			}
			if hits >= 4 {
				last = x
				gap = 0
			} else {
				gap++
				if gap > step {
					break
				}
			}
			sinceCP++
			if sinceCP >= step*4 && hits >= 4 {
				sinceCP = 0
				st.xs = append(st.xs, float64(x))
				for l := 0; l < 5; l++ {
					st.ys[l] = append(st.ys[l], ys[l])
				}
				sortStaffPts(st)
			}
		}
		if dir < 0 {
			st.X0 = last
		} else {
			st.X1 = last
		}
	}
}

// removeStaffLines erases staff-line pixels that are not part of a symbol.
func removeStaffLines(b *Bitmap, staves []*Staff, thick int) *Bitmap {
	out := b.Clone()
	maxRun := thick + 2
	for _, st := range staves {
		for x := st.X0; x <= st.X1; x++ {
			for l := 0; l < 5; l++ {
				yc := int(math.Round(st.LineY(l, float64(x))))
				// find black run touching yc +- thick
				y0 := -1
				for dy := -thick; dy <= thick; dy++ {
					if b.At(x, yc+dy) == 1 {
						y0 = yc + dy
						break
					}
				}
				if y0 < 0 {
					continue
				}
				s, e := y0, y0
				for s > 0 && b.At(x, s-1) == 1 {
					s--
				}
				for e < b.H-1 && b.At(x, e+1) == 1 {
					e++
				}
				if e-s+1 <= maxRun {
					for y := s; y <= e; y++ {
						out.Pix[y*b.W+x] = 0
					}
				}
			}
		}
	}
	return out
}

func evenGaps(g []float64) bool {
	m := (g[4] - g[0]) / 4
	for i := 1; i < 5; i++ {
		if d := g[i] - g[i-1]; d < m*0.85 || d > m*1.15 {
			return false
		}
	}
	return true
}

// smoothStaff removes outlier control points (a beam mistaken for a line in
// one strip) by comparing each point with the median of its neighbours.
func smoothStaff(st *Staff) {
	n := len(st.xs)
	if n < 3 {
		return
	}
	keep := make([]bool, n)
	for i := 0; i < n; i++ {
		var nb []float64
		for j := max(0, i-3); j <= min(n-1, i+3); j++ {
			if j != i {
				nb = append(nb, st.ys[2][j])
			}
		}
		keep[i] = math.Abs(st.ys[2][i]-median(nb)) < 3
	}
	var xs []float64
	var ys [5][]float64
	for i := 0; i < n; i++ {
		if keep[i] {
			xs = append(xs, st.xs[i])
			for l := 0; l < 5; l++ {
				ys[l] = append(ys[l], st.ys[l][i])
			}
		}
	}
	if len(xs) >= 2 {
		st.xs, st.ys = xs, ys
	}
}

func sortStaffPts(st *Staff) {
	idx := make([]int, len(st.xs))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return st.xs[idx[a]] < st.xs[idx[b]] })
	var xs []float64
	var ys [5][]float64
	for _, i := range idx {
		xs = append(xs, st.xs[i])
		for l := 0; l < 5; l++ {
			ys[l] = append(ys[l], st.ys[l][i])
		}
	}
	st.xs, st.ys = xs, ys
}
