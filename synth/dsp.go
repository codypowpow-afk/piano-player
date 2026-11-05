package synth

import "math"

// biquad filter (RBJ cookbook), direct form I.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func (f *biquad) run(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

func (f *biquad) set(b0, b1, b2, a0, a1, a2 float64) {
	f.b0, f.b1, f.b2, f.a1, f.a2 = b0/a0, b1/a0, b2/a0, a1/a0, a2/a0
}

func (f *biquad) peaking(fc, q, db float64) {
	A := math.Pow(10, db/40)
	w := 2 * math.Pi * fc / SampleRate
	al := math.Sin(w) / (2 * q)
	c := math.Cos(w)
	f.set(1+al*A, -2*c, 1-al*A, 1+al/A, -2*c, 1-al/A)
}

func (f *biquad) highShelf(fc, db float64) {
	A := math.Pow(10, db/40)
	w := 2 * math.Pi * fc / SampleRate
	c, s := math.Cos(w), math.Sin(w)
	al := s / 2 * math.Sqrt2
	sa := 2 * math.Sqrt(A) * al
	f.set(A*((A+1)+(A-1)*c+sa), -2*A*((A-1)+(A+1)*c), A*((A+1)+(A-1)*c-sa),
		(A+1)-(A-1)*c+sa, 2*((A-1)-(A+1)*c), (A+1)-(A-1)*c-sa)
}

// Freeverb-style stereo reverb.
type comb struct {
	buf      []float64
	i        int
	fb, damp float64
	store    float64
}

func (c *comb) process(x float64) float64 {
	y := c.buf[c.i]
	c.store = y*(1-c.damp) + c.store*c.damp
	c.buf[c.i] = x + c.store*c.fb
	c.i++
	if c.i == len(c.buf) {
		c.i = 0
	}
	return y
}

type allpass struct {
	buf []float64
	i   int
}

func (a *allpass) process(x float64) float64 {
	b := a.buf[a.i]
	y := -x + b
	a.buf[a.i] = x + b*0.5
	a.i++
	if a.i == len(a.buf) {
		a.i = 0
	}
	return y
}

type reverb struct {
	combL, combR []comb
	apL, apR     []allpass
	pre          []float64
	preI         int
}

func newReverb() *reverb {
	combT := []int{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617}
	apT := []int{556, 441, 341, 225}
	scale := float64(SampleRate) / 44100
	r := &reverb{pre: make([]float64, int(0.012*SampleRate))}
	for _, t := range combT {
		r.combL = append(r.combL, comb{buf: make([]float64, int(float64(t)*scale))})
		r.combR = append(r.combR, comb{buf: make([]float64, int(float64(t+23)*scale))})
	}
	for _, t := range apT {
		r.apL = append(r.apL, allpass{buf: make([]float64, int(float64(t)*scale))})
		r.apR = append(r.apR, allpass{buf: make([]float64, int(float64(t+23)*scale))})
	}
	return r
}

func (r *reverb) set(room, damp float64) {
	fb := 0.7 + 0.28*room
	for i := range r.combL {
		r.combL[i].fb, r.combL[i].damp = fb, damp
		r.combR[i].fb, r.combR[i].damp = fb, damp
	}
}

func (r *reverb) process(l, rr float64) (float64, float64) {
	// Short pre-delay keeps the dry attack clear.
	in := r.pre[r.preI]
	r.pre[r.preI] = (l + rr) * 0.015
	r.preI++
	if r.preI == len(r.pre) {
		r.preI = 0
	}
	var ol, or float64
	for i := range r.combL {
		ol += r.combL[i].process(in)
		or += r.combR[i].process(in)
	}
	for i := range r.apL {
		ol = r.apL[i].process(ol)
		or = r.apR[i].process(or)
	}
	return ol, or
}
