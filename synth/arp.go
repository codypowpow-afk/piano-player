package synth

import (
	"math"
	"math/rand"
	"sort"
	"sync/atomic"
)

// Arpeggiator patterns.
var ArpModes = []string{"Up", "Down", "Up-Down", "Down-Up", "Up & Down", "Random", "As Played", "Chord", "Alberti", "Converge", "Diverge", "Thumb"}

// Arpeggiator rates, as note values.
var ArpRates = []struct {
	Name  string
	Beats float64 // length of one step in quarter notes
}{
	{"1/4", 1}, {"1/8", 0.5}, {"1/8 T", 1.0 / 3}, {"1/16", 0.25}, {"1/16 T", 1.0 / 6}, {"1/32", 0.125},
}

// ArpSettings are the user-facing arpeggiator options.
type ArpSettings struct {
	On      bool
	Mode    int     // index into ArpModes
	Rate    int     // index into ArpRates
	BPM     float64 // 30..300
	Octaves int     // 1..4
	Gate    float64 // 0.1..2 (fraction of a step; >1 overlaps / legato)
	Swing   float64 // 0..0.6 (lengthens even steps, shortens odd ones)
	Latch   bool    // keep playing after the keys are released
	Accent  bool    // emphasise the downbeats
}

func DefaultArp() ArpSettings {
	return ArpSettings{Mode: 2, Rate: 3, BPM: 110, Octaves: 2, Gate: 0.8}
}

type heldNote struct {
	note  int
	vel   float64
	order int64
}

type arpOut struct {
	note int
	off  int64 // sample time to release
}

type arp struct {
	s        ArpSettings
	held     []heldNote // keys physically down
	latched  []heldNote // pool when latch is on
	order    int64
	running  bool
	step     int
	nextAt   int64
	now      int64
	out      []arpOut
	direct   [128]bool // notes that were playing before the arp took over
	rnd      *rand.Rand
	lastRand int
	beat     atomic.Int64 // counts quarter notes, for the UI beat light
	sounding [128]atomic.Bool
}

func newArp() *arp {
	return &arp{s: DefaultArp(), rnd: rand.New(rand.NewSource(3)), lastRand: -1}
}

func (a *arp) pool() []heldNote {
	if a.s.Latch {
		return a.latched
	}
	return a.held
}

func (a *arp) press(note int, vel float64) {
	a.order++
	if a.s.Latch && len(a.held) == 0 {
		a.latched = a.latched[:0] // a fresh chord replaces the latched one
	}
	h := heldNote{note, vel, a.order}
	a.held = appendNote(a.held, h)
	a.latched = appendNote(a.latched, h)
	if !a.running {
		a.running = true
		a.step = 0
		a.nextAt = a.now // first note right away, in time with your hand
	}
}

func appendNote(l []heldNote, h heldNote) []heldNote {
	for i := range l {
		if l[i].note == h.note {
			l[i] = h
			return l
		}
	}
	return append(l, h)
}

func removeNote(l []heldNote, n int) []heldNote {
	for i := range l {
		if l[i].note == n {
			return append(l[:i], l[i+1:]...)
		}
	}
	return l
}

func (a *arp) release(note int) {
	a.held = removeNote(a.held, note)
}

// sequence builds the notes (or chords) of one pass through the pattern.
func (a *arp) sequence() [][]heldNote {
	p := a.pool()
	if len(p) == 0 {
		return nil
	}
	byPitch := append([]heldNote(nil), p...)
	sort.Slice(byPitch, func(i, j int) bool { return byPitch[i].note < byPitch[j].note })
	played := append([]heldNote(nil), p...)
	sort.Slice(played, func(i, j int) bool { return played[i].order < played[j].order })

	oct := a.s.Octaves
	if oct < 1 {
		oct = 1
	}
	spread := func(src []heldNote) []heldNote {
		var r []heldNote
		for o := 0; o < oct; o++ {
			for _, h := range src {
				if n := h.note + 12*o; n <= 108 {
					r = append(r, heldNote{n, h.vel, h.order})
				}
			}
		}
		return r
	}
	one := func(l []heldNote) [][]heldNote {
		r := make([][]heldNote, len(l))
		for i := range l {
			r[i] = []heldNote{l[i]}
		}
		return r
	}
	rev := func(l []heldNote) []heldNote {
		r := make([]heldNote, len(l))
		for i := range l {
			r[len(l)-1-i] = l[i]
		}
		return r
	}
	up := spread(byPitch)
	switch ArpModes[a.s.Mode] {
	case "Down":
		return one(rev(up))
	case "Up-Down": // no repeated top/bottom
		if len(up) < 3 {
			return one(up)
		}
		return one(append(up, rev(up)[1:len(up)-1]...))
	case "Down-Up":
		if len(up) < 3 {
			return one(rev(up))
		}
		d := rev(up)
		return one(append(d, up[1:len(up)-1]...))
	case "Up & Down": // top and bottom played twice
		return one(append(up, rev(up)...))
	case "Random":
		return one(up)
	case "As Played":
		return one(spread(played))
	case "Chord":
		var r [][]heldNote
		for o := 0; o < oct; o++ {
			var c []heldNote
			for _, h := range byPitch {
				if n := h.note + 12*o; n <= 108 {
					c = append(c, heldNote{n, h.vel, h.order})
				}
			}
			r = append(r, c)
		}
		return r
	case "Alberti": // low, high, middle, high (classical left hand)
		var r []heldNote
		for o := 0; o < oct; o++ {
			l := byPitch
			sh := func(h heldNote) heldNote { return heldNote{h.note + 12*o, h.vel, h.order} }
			if len(l) <= 2 {
				for _, h := range l {
					r = append(r, sh(h))
				}
				continue
			}
			top := l[len(l)-1]
			for k := 1; k < len(l)-1; k++ {
				r = append(r, sh(l[0]), sh(top), sh(l[k]), sh(top))
			}
		}
		return one(r)
	case "Converge": // outside in
		var r []heldNote
		for i, j := 0, len(up)-1; i <= j; i, j = i+1, j-1 {
			r = append(r, up[i])
			if i != j {
				r = append(r, up[j])
			}
		}
		return one(r)
	case "Diverge": // inside out
		var r []heldNote
		for i, j := 0, len(up)-1; i <= j; i, j = i+1, j-1 {
			r = append(r, up[i])
			if i != j {
				r = append(r, up[j])
			}
		}
		return one(rev(r))
	case "Thumb": // lowest note alternates with each of the others
		if len(up) < 2 {
			return one(up)
		}
		var r []heldNote
		for _, h := range up[1:] {
			r = append(r, up[0], h)
		}
		return one(r)
	}
	return one(up)
}

func (a *arp) stepLen(step int) int64 {
	l := 60 / a.s.BPM * ArpRates[a.s.Rate].Beats * SampleRate
	if a.s.Swing > 0 {
		if step%2 == 0 {
			l *= 1 + a.s.Swing
		} else {
			l *= 1 - a.s.Swing
		}
	}
	return int64(l)
}

// tick advances the arpeggiator by n samples, calling on/off for the notes it plays.
func (a *arp) tick(n int, on func(int, float64), off func(int)) {
	end := a.now + int64(n)
	for {
		// Release notes whose gate has closed.
		k := 0
		for _, o := range a.out {
			if o.off <= end {
				off(o.note)
				a.sounding[o.note].Store(false)
			} else {
				a.out[k] = o
				k++
			}
		}
		a.out = a.out[:k]
		if !a.running || a.nextAt > end {
			break
		}
		seq := a.sequence()
		if len(seq) == 0 {
			a.running = false
			break
		}
		var chord []heldNote
		if ArpModes[a.s.Mode] == "Random" {
			i := a.rnd.Intn(len(seq))
			if len(seq) > 1 && i == a.lastRand {
				i = (i + 1 + a.rnd.Intn(len(seq)-1)) % len(seq)
			}
			a.lastRand = i
			chord = seq[i]
		} else {
			chord = seq[a.step%len(seq)]
		}
		sl := a.stepLen(a.step)
		beats := ArpRates[a.s.Rate].Beats
		onBeat := int(math.Round(float64(a.step)*beats*1000))%1000 == 0
		if onBeat {
			a.beat.Add(1)
		}
		for _, h := range chord {
			v := h.vel
			if a.s.Accent {
				if onBeat {
					v = v*1.15 + 0.05
				} else {
					v *= 0.85
				}
			}
			on(h.note, v)
			a.sounding[h.note].Store(true)
			a.out = append(a.out, arpOut{h.note, a.nextAt + int64(float64(sl)*a.s.Gate)})
		}
		a.nextAt += sl
		a.step++
	}
	a.now = end
}

// stop releases everything the arpeggiator is playing.
func (a *arp) stop(off func(int)) {
	for _, o := range a.out {
		off(o.note)
		a.sounding[o.note].Store(false)
	}
	a.out = a.out[:0]
	a.running = false
	a.held = a.held[:0]
	a.latched = a.latched[:0]
}
