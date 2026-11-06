package omr

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"

	"github.com/codypowpow-afk/piano-player/score"
)

// Result is the outcome of reading a whole document.
type Result struct {
	Pages  []*PageResult
	Songs  []*score.Song
	Pieces []PieceInfo
}

// PieceInfo records where each piece starts and its detected key.
type PieceInfo struct {
	FirstPage, FirstSystem int
	Key                    int
	Systems                int
}

// Progress is called with (done, total) while pages are analysed.
type Progress func(done, total int, msg string)

// ReadPDF reads the page images of a PDF and recognises the music. Pages are
// decoded and analysed a few at a time to keep memory use modest.
func ReadPDF(data []byte, title string, prog Progress) (*Result, error) {
	if prog != nil {
		prog(0, 1, "Reading PDF pages…")
	}
	ps, err := OpenPDF(data)
	if err != nil {
		return nil, err
	}
	var firstErr error
	var emu sync.Mutex
	res := readWith(ps.Len(), func(i int) *Bitmap {
		bm, err := ps.Page(i)
		if err != nil {
			emu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			emu.Unlock()
			return nil
		}
		return bm
	}, title, prog)
	if len(res.Songs) == 0 && firstErr != nil {
		return nil, firstErr
	}
	if len(res.Songs) == 0 {
		return nil, errors.New("no music staves were found in this PDF")
	}
	return res, nil
}

// ReadPages recognises music on already-decoded page bitmaps.
func ReadPages(pages []*Bitmap, title string, prog Progress) *Result {
	return readWith(len(pages), func(i int) *Bitmap {
		p := pages[i]
		pages[i] = nil
		return p
	}, title, prog)
}

func readWith(n int, page func(i int) *Bitmap, title string, prog Progress) *Result {
	res := &Result{Pages: make([]*PageResult, n)}
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	sem := make(chan struct{}, max(1, min(runtime.NumCPU(), 2)))
	old := debug.SetGCPercent(40)
	defer debug.SetGCPercent(old)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			var pr *PageResult
			if bm := page(i); bm != nil {
				pr = AnalyzePage(bm)
				pr.Img, pr.closed, pr.filled, pr.closedLab, pr.closedComps = nil, nil, nil, nil, nil
			}
			debug.FreeOSMemory()
			<-sem
			mu.Lock()
			res.Pages[i] = pr
			done++
			if prog != nil {
				prog(done, n, fmt.Sprintf("Reading music: page %d of %d", done, n))
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	res.assemble(title)
	return res
}

type sysRef struct {
	page, idx int
	pr        *PageResult
	staves    []int
	key       int
	indent    bool
}

func (res *Result) assemble(title string) {
	var systems []*sysRef
	// typical staff start x per page, to spot indented (first) systems
	for pi, pr := range res.Pages {
		if pr == nil || len(pr.Systems) == 0 {
			continue
		}
		var x0s []float64
		for _, s := range pr.Staves {
			x0s = append(x0s, float64(s.X0))
		}
		medX0 := median(x0s)
		for si, sys := range pr.Systems {
			sr := &sysRef{page: pi, idx: si, pr: pr, staves: sys}
			sr.indent = float64(pr.Staves[sys[0]].X0) > medX0+pr.Space*8
			// fuse staff key readings: the strongest agreeing reading
			best := 0
			for k, st := range sys {
				v := pr.KeySig[st]
				if abs(v) > abs(best) && (best == 0 || sign(v) == sign(best) || k == 0) {
					best = v
				}
			}
			sr.key = best
			systems = append(systems, sr)
		}
	}
	if len(systems) == 0 {
		return
	}
	// split into pieces at indented systems
	var pieces [][]*sysRef
	for i, s := range systems {
		if i == 0 || s.indent {
			pieces = append(pieces, nil)
		}
		pieces[len(pieces)-1] = append(pieces[len(pieces)-1], s)
	}
	for pn, sys := range pieces {
		smoothKeys(sys)
		name := title
		if len(pieces) > 1 {
			name = fmt.Sprintf("%s — No. %d", title, pn+1)
		}
		song := buildSong(sys, name)
		res.Songs = append(res.Songs, song)
		res.Pieces = append(res.Pieces, PieceInfo{FirstPage: sys[0].page, FirstSystem: sys[0].idx, Key: sys[0].key, Systems: len(sys)})
	}
}

func sign(v int) int {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}

// smoothKeys repairs undercounted key signatures: readings usually fail by
// stopping early, so take the largest value seen at least twice nearby.
func smoothKeys(sys []*sysRef) {
	raw := make([]int, len(sys))
	for i, s := range sys {
		raw[i] = s.key
	}
	for i := range sys {
		cnt := map[int]int{}
		for j := max(0, i-3); j <= min(len(sys)-1, i+3); j++ {
			if raw[j] != 0 {
				cnt[raw[j]]++
			}
		}
		// the piece's opening systems decide its sign
		best, bc := raw[i], 0
		for v, c := range cnt {
			if c >= 2 && (abs(v) > abs(best) || c > bc+1) {
				best, bc = v, c
			}
		}
		if bc == 0 {
			// nothing repeated: fall back to the most common value in the piece
			all := map[int]int{}
			for _, v := range raw {
				if v != 0 {
					all[v]++
				}
			}
			for v, c := range all {
				if c > bc {
					best, bc = v, c
				}
			}
		}
		sys[i].key = best
	}
}

var semis = [7]int{0, 2, 4, 5, 7, 9, 11}
var sharpOrder = [7]int{3, 0, 4, 1, 5, 2, 6} // F C G D A E B
var flatOrder = [7]int{6, 2, 5, 1, 4, 0, 3}  // B E A D G C F

func keyAlter(step, fifths int) int {
	if fifths > 0 {
		for i := 0; i < fifths && i < 7; i++ {
			if sharpOrder[i] == step {
				return 1
			}
		}
	} else {
		for i := 0; i < -fifths && i < 7; i++ {
			if flatOrder[i] == step {
				return -1
			}
		}
	}
	return 0
}

type column struct {
	x     float64
	heads []*Head
	t     float64
}

func buildSong(sys []*sysRef, title string) *score.Song {
	song := &score.Song{Title: title, Tempo: 72, BeatsPer: 4}
	if len(sys) > 0 {
		song.KeyFifths = sys[0].key
	}
	type measure struct {
		s      *sysRef
		xL, xR float64
		cols   []*column
	}
	var ms []*measure
	for _, s := range sys {
		pr := s.pr
		sp := pr.Space
		start := 0.0
		for _, st := range s.staves {
			start = math.Max(start, pr.KeyEndX[st])
		}
		end := math.Inf(1)
		for _, st := range s.staves {
			end = math.Min(end, float64(pr.Staves[st].X1))
		}
		bounds := []float64{start}
		for _, b := range pr.Barlines[s.idx] {
			if b > start+sp*1.5 && b < end-sp*0.5 {
				bounds = append(bounds, b)
			}
		}
		if end-bounds[len(bounds)-1] > sp*1.5 {
			bounds = append(bounds, end)
		}
		var heads []*Head
		for _, h := range pr.Heads {
			if inSys(h.Staff, s.staves) && h.OnsetX > start-sp*0.3 {
				heads = append(heads, h)
			}
		}
		sort.Slice(heads, func(i, j int) bool { return heads[i].OnsetX < heads[j].OnsetX })
		for k := 0; k+1 < len(bounds); k++ {
			m := &measure{s: s, xL: bounds[k], xR: bounds[k+1]}
			for _, h := range heads {
				if h.OnsetX >= m.xL && h.OnsetX < m.xR {
					if n := len(m.cols); n > 0 && h.OnsetX-m.cols[n-1].x <= sp*0.55 {
						m.cols[n-1].heads = append(m.cols[n-1].heads, h)
						continue
					}
					m.cols = append(m.cols, &column{x: h.OnsetX, heads: []*Head{h}})
				}
			}
			if len(m.cols) > 0 {
				ms = append(ms, m)
			}
		}
	}
	if len(ms) == 0 {
		return song
	}
	var counts []float64
	for _, m := range ms {
		counts = append(counts, float64(len(m.cols)))
	}
	med := median(counts)
	L := song.BeatsPer
	t0 := 0.0
	for mi, m := range ms {
		pr := m.s.pr
		k := float64(len(m.cols))
		Lm := L
		if k < med*0.5 && mi == 0 {
			Lm = L * math.Max(k/med, 0.125) // pickup
		} else if k > med*1.7 {
			Lm = L * math.Min(k/med, 4) // cadenza-like run
		}
		xFirst := m.cols[0].x
		span := m.xR - xFirst
		for _, c := range m.cols {
			c.t = Lm * (c.x - xFirst) / span
			if Lm < L && mi == 0 { // pickup: keep notes evenly spaced
				c.t = Lm * (c.x - xFirst) / span
			}
		}
		song.Measures = append(song.Measures, t0)
		// pitches with measure-scoped accidentals
		acc := map[[2]int]int{}
		var all []*Head
		for _, c := range m.cols {
			for _, h := range c.heads {
				all = append(all, h)
			}
		}
		sort.Slice(all, func(i, j int) bool { return all[i].X < all[j].X })
		pitch := map[*Head]int{}
		for _, h := range all {
			clef := pr.clefAt(h.Staff, h.X)
			base := 30 // E4 on the treble bottom line
			if clef == ClefBass {
				base = 18 // G2
			}
			d := base + h.Pos
			step := ((d % 7) + 7) % 7
			oct := int(math.Floor(float64(d) / 7))
			key := [2]int{h.Staff, d}
			alter := keyAlter(step, m.s.key)
			if v, ok := acc[key]; ok {
				alter = v
			}
			switch h.Acc {
			case 1:
				alter = 1
				acc[key] = 1
			case -1:
				alter = -1
				acc[key] = -1
			case 2:
				alter = 0
				acc[key] = 0
			}
			pitch[h] = 12*(oct+1) + semis[step] + alter + 12*h.Octave
		}
		// durations: until the next onset in the same staff and stem voice
		for ci, c := range m.cols {
			for _, h := range c.heads {
				end := Lm
				for _, c2 := range m.cols[ci+1:] {
					found := false
					for _, h2 := range c2.heads {
						if h2.Staff == h.Staff && (h2.StemDir == h.StemDir || h.StemDir == 0 || h2.StemDir == 0) {
							found = true
							break
						}
					}
					if found {
						end = c2.t
						break
					}
				}
				dur := math.Max(end-c.t, 0.1)
				hand := score.Right
				if len(m.s.staves) > 1 && h.Staff == m.s.staves[len(m.s.staves)-1] {
					hand = score.Left
				} else if len(m.s.staves) == 1 && pr.clefAt(h.Staff, 0) == ClefBass {
					hand = score.Left
				}
				p := pitch[h]
				if p < 21 || p > 108 {
					continue
				}
				song.Notes = append(song.Notes, score.Note{
					Pitch: p, Start: t0 + c.t, Dur: dur, Hand: hand, Measure: len(song.Measures) - 1,
					Vel: 0.7, Page: m.s.page + 1, X: float32(h.X), Y: float32(h.Y),
				})
			}
		}
		t0 += Lm
	}
	dedupe(song)
	song.Sort()
	return song
}

// dedupe removes identical notes at the same time (double detections).
func dedupe(s *score.Song) {
	seen := map[[2]int]bool{}
	out := s.Notes[:0]
	for _, n := range s.Notes {
		k := [2]int{n.Pitch, int(math.Round(n.Start * 96))}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, n)
	}
	s.Notes = out
}
