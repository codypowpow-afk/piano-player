package synth

import "math"

// fxChain is the per-preset master effects: EQ, filters, saturation, compression,
// tape wobble, crackle, tremolo, stereo width and reverb.
type fxChain struct {
	p *Preset

	hp, lp, ls, pk, hs [2]biquad
	useHP, useLP       bool
	useLS, usePK       bool
	useHS              bool

	compEnv  float64
	compGain float64

	// wobble: modulated delay line
	dl      [2][]float64
	dlPos   int
	phWow   float64
	phFlut  float64
	phTrem  float64
	crackle uint32
	hissLP  float64
	pop     float64

	rev *reverb
}

func newFX() *fxChain {
	n := SampleRate / 20 // 50 ms
	return &fxChain{dl: [2][]float64{make([]float64, n), make([]float64, n)}, rev: newReverb(), crackle: 12345, compGain: 1}
}

func (f *fxChain) configure(p *Preset) {
	f.p = p
	f.useHP, f.useLP = p.HighPass > 0, p.LowPass > 0
	f.useLS, f.usePK, f.useHS = p.LowShelf != 0, p.PeakDB != 0, p.HighShelf != 0
	for c := 0; c < 2; c++ {
		if f.useHP {
			f.hp[c].highPass(p.HighPass, 0.707)
		}
		if f.useLP {
			f.lp[c].lowPass(p.LowPass, 0.707)
		}
		if f.useLS {
			f.ls[c].lowShelf(180, p.LowShelf)
		}
		if f.usePK {
			q := p.PeakQ
			if q == 0 {
				q = 1
			}
			f.pk[c].peaking(p.PeakHz, q, p.PeakDB)
		}
		if f.useHS {
			f.hs[c].highShelf(4000, p.HighShelf)
		}
	}
	f.rev.set(p.RoomSize, p.RoomDamp)
}

func (f *fxChain) rnd() float64 {
	x := f.crackle
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	f.crackle = x
	return float64(x)/4294967296.0*2 - 1
}

func (f *fxChain) process(bl, br *[blockSize]float64, reverbAmt float64, pedal bool) {
	p := f.p
	wet := p.RoomWet * reverbAmt
	if pedal {
		wet *= 1.15 // open strings ring along with the pedal down
	}
	n := len(f.dl[0])
	for j := 0; j < blockSize; j++ {
		l, r := bl[j], br[j]
		if f.useHP {
			l, r = f.hp[0].run(l), f.hp[1].run(r)
		}
		if f.useLS {
			l, r = f.ls[0].run(l), f.ls[1].run(r)
		}
		if f.usePK {
			l, r = f.pk[0].run(l), f.pk[1].run(r)
		}
		if f.useHS {
			l, r = f.hs[0].run(l), f.hs[1].run(r)
		}
		if f.useLP {
			l, r = f.lp[0].run(l), f.lp[1].run(r)
		}
		if p.Comp > 0 {
			lvl := math.Max(math.Abs(l), math.Abs(r))
			if lvl > f.compEnv {
				f.compEnv += (lvl - f.compEnv) * 0.01
			} else {
				f.compEnv += (lvl - f.compEnv) * 0.0002
			}
			thr := 0.12
			g := 1.0
			if f.compEnv > thr {
				ratio := 1 + 4*p.Comp
				g = math.Pow(f.compEnv/thr, 1/ratio-1)
			}
			mk := 1 + p.Comp*1.2
			l, r = l*g*mk, r*g*mk
		}
		if p.Drive > 0 {
			d := 1 + p.Drive*4
			l, r = math.Tanh(l*d)/d*(1+p.Drive), math.Tanh(r*d)/d*(1+p.Drive)
		}
		if p.Wobble > 0 {
			f.dl[0][f.dlPos], f.dl[1][f.dlPos] = l, r
			f.phWow += 2 * math.Pi * 0.55 / SampleRate
			f.phFlut += 2 * math.Pi * 6.3 / SampleRate
			delay := 0.008*SampleRate + p.Wobble*(0.0035*SampleRate*math.Sin(f.phWow)+0.0004*SampleRate*math.Sin(f.phFlut))
			rp := float64(f.dlPos) - delay
			for rp < 0 {
				rp += float64(n)
			}
			i := int(rp)
			t := rp - float64(i)
			i2 := (i + 1) % n
			l = f.dl[0][i]*(1-t) + f.dl[0][i2]*t
			r = f.dl[1][i]*(1-t) + f.dl[1][i2]*t
			f.dlPos = (f.dlPos + 1) % n
		}
		if p.TremDepth > 0 {
			f.phTrem += 2 * math.Pi * p.TremHz / SampleRate
			g := 1 - p.TremDepth*0.5*(1+math.Sin(f.phTrem))
			gr := 1 - p.TremDepth*0.5*(1+math.Sin(f.phTrem+0.6))
			l, r = l*g, r*gr
		}
		if p.Width != 1 {
			m, s := (l+r)*0.5, (l-r)*0.5*p.Width
			l, r = m+s, m-s
		}
		if p.Crackle > 0 {
			// hiss
			f.hissLP += (f.rnd() - f.hissLP) * 0.25
			h := f.hissLP * 0.004 * p.Crackle
			// pops and clicks
			if f.rnd() > 1-0.00025*p.Crackle*2 {
				f.pop = (f.rnd()*0.5 + 0.5) * 0.12 * p.Crackle
			}
			c := f.pop * f.rnd()
			f.pop *= 0.85
			l += h + c
			r += h + c
		}
		if wet > 0 {
			rl, rr := f.rev.process(l, r)
			l += rl * wet * 3
			r += rr * wet * 3
		}
		bl[j], br[j] = l, r
	}
}
