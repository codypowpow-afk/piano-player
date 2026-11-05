// Package synth is a real-time acoustic piano synthesizer.
//
// Every note is built from scratch with modal (additive) synthesis, modelled on how
// piano strings behave:
//   - inharmonic partials (stiff strings: f_n = n·f0·sqrt(1+B·n²))
//   - two or three slightly detuned unison strings per note (beating / chorus)
//   - two-stage decay (fast "prompt sound", slow "aftersound")
//   - hammer strike position comb, velocity-dependent brightness
//   - soundboard radiation and body resonance
//   - hammer knock / thump noise, dampers on key release, no dampers in the top octave
//   - sustain pedal and a stereo room reverb
//
// No samples are used, so the program is a single small executable.
package synth

import (
	"math"
	"math/rand"
	"sync"
)

const (
	SampleRate = 48000
	maxVoices  = 72
	blockSize  = 64
	maxPartial = 64
)

// Preset describes one piano model.
type Preset struct {
	Name        string
	Short       string
	InharmMul   float64 // multiplies the string-stiffness curve
	BassInharm  float64 // extra stiffness in the bass (short upright bass strings)
	SustainMul  float64 // multiplies T60 of the slow decay
	PromptRatio float64 // prompt/aftersound amplitude split (0..1, higher = more prompt)
	PartialDamp float64 // how much faster upper partials decay
	StrikePos   float64 // hammer strike position along the string (fraction)
	Rolloff     float64 // spectral rolloff exponent 1/n^x
	Brightness  float64 // multiplies velocity brightness cutoff
	RadiationHz float64 // soundboard radiation high-pass corner
	DetuneCents float64 // spread of unison strings
	Strings3    bool    // three strings per note in the mid/treble
	BodyHz      float64 // body resonance center
	BodyGainDB  float64
	BodyQ       float64
	TrebleShelf float64 // dB above ~4 kHz
	KnockLevel  float64 // hammer/action knock
	NoiseLevel  float64 // hammer felt noise
	RoomSize    float64
	RoomDamp    float64
	RoomWet     float64
	StereoWidth float64 // pan spread by pitch
	Gain        float64
}

var Presets = []Preset{
	{
		Name: "Concert Grand", Short: "GRAND",
		InharmMul: 1.0, BassInharm: 0, SustainMul: 1.0, PromptRatio: 0.45, PartialDamp: 1.0,
		StrikePos: 0.118, Rolloff: 0.85, Brightness: 1.0, RadiationHz: 70,
		DetuneCents: 0.55, Strings3: true,
		BodyHz: 180, BodyGainDB: 1.5, BodyQ: 0.8, TrebleShelf: 0,
		KnockLevel: 0.6, NoiseLevel: 0.7,
		RoomSize: 0.84, RoomDamp: 0.35, RoomWet: 0.20, StereoWidth: 0.7, Gain: 1.0,
	},
	{
		Name: "Upright", Short: "UPRIGHT",
		InharmMul: 1.7, BassInharm: 2.5, SustainMul: 0.62, PromptRatio: 0.68, PartialDamp: 1.35,
		StrikePos: 0.135, Rolloff: 0.8, Brightness: 0.9, RadiationHz: 150,
		DetuneCents: 1.4, Strings3: true,
		BodyHz: 420, BodyGainDB: 4.0, BodyQ: 1.1, TrebleShelf: -3,
		KnockLevel: 1.3, NoiseLevel: 1.1,
		RoomSize: 0.58, RoomDamp: 0.55, RoomWet: 0.13, StereoWidth: 0.35, Gain: 1.35,
	},
	{
		Name: "Bright Grand", Short: "BRIGHT",
		InharmMul: 1.0, BassInharm: 0, SustainMul: 0.95, PromptRatio: 0.48, PartialDamp: 0.85,
		StrikePos: 0.108, Rolloff: 0.7, Brightness: 1.7, RadiationHz: 80,
		DetuneCents: 0.7, Strings3: true,
		BodyHz: 200, BodyGainDB: 0.5, BodyQ: 0.8, TrebleShelf: 2.5,
		KnockLevel: 0.7, NoiseLevel: 0.9,
		RoomSize: 0.78, RoomDamp: 0.30, RoomWet: 0.16, StereoWidth: 0.75, Gain: 1.0,
	},
	{
		Name: "Old Upright", Short: "OLD UPRIGHT",
		InharmMul: 2.0, BassInharm: 3.5, SustainMul: 0.5, PromptRatio: 0.72, PartialDamp: 1.5,
		StrikePos: 0.14, Rolloff: 0.75, Brightness: 1.05, RadiationHz: 170,
		DetuneCents: 7.0, Strings3: true,
		BodyHz: 500, BodyGainDB: 5.0, BodyQ: 1.3, TrebleShelf: -1,
		KnockLevel: 1.8, NoiseLevel: 1.4,
		RoomSize: 0.5, RoomDamp: 0.6, RoomWet: 0.12, StereoWidth: 0.3, Gain: 1.45,
	},
}

// osc is one damped sinusoid. It advances by complex rotation (cheap and exact).
type osc struct {
	re, im float64 // phasor
	c, s   float64 // rotation per sample
	amp    float64 // prompt-sound amplitude (fast decay)
	amp2   float64 // aftersound amplitude (slow decay)
	decay  float64 // per-sample multiplier, prompt
	decay2 float64 // per-sample multiplier, aftersound
	relDec float64 // multiplier once damped
}

type voice struct {
	active   bool
	note     int
	keyDown  bool
	damped   bool
	fading   bool // being replaced by a re-strike of the same key
	age      int64
	oscs     []osc
	gainL    float64
	gainR    float64
	attack   int // samples into the attack ramp
	noiseEnv float64
	noiseDec float64
	noiseLP  float64
	noiseLPa float64
	noiseAmp float64
	knock    osc
	knockOn  bool
	level    float64 // rough running peak, for voice stealing / ending
	hasDamp  bool
	relMul   float64
	fadeMul  float64
	rng      uint32
}

type event struct {
	kind int // 0 on, 1 off, 2 pedal, 3 preset, 4 allOff
	note int
	vel  float64
	on   bool
}

// Engine mixes all voices. Safe to call NoteOn/NoteOff from any goroutine.
type Engine struct {
	mu     sync.Mutex
	queue  []event
	voices []voice
	preset int
	pedal  bool
	clock  int64

	Volume float64 // master volume 0..1.5 (read without locking; plain float)
	Reverb float64 // reverb amount multiplier 0..2

	rev     *reverb
	bodyL   biquad
	bodyR   biquad
	shelfL  biquad
	shelfR  biquad
	bufL    [blockSize]float64
	bufR    [blockSize]float64
	pending []float32
	rnd     *rand.Rand
	peak    float64
	limGain float64
	readBuf []float32
}

func NewEngine() *Engine {
	e := &Engine{
		voices:  make([]voice, maxVoices),
		Volume:  0.8,
		Reverb:  1.0,
		rev:     newReverb(),
		rnd:     rand.New(rand.NewSource(7)),
		limGain: 1,
	}
	for i := range e.voices {
		e.voices[i].oscs = make([]osc, 0, maxPartial*2)
	}
	e.applyPreset(0)
	return e
}

func (e *Engine) push(ev event) {
	e.mu.Lock()
	e.queue = append(e.queue, ev)
	e.mu.Unlock()
}

// NoteOn starts a note. midi 21..108, vel 0..1.
func (e *Engine) NoteOn(midi int, vel float64) { e.push(event{kind: 0, note: midi, vel: vel}) }
func (e *Engine) NoteOff(midi int)             { e.push(event{kind: 1, note: midi}) }
func (e *Engine) SetPedal(down bool)           { e.push(event{kind: 2, on: down}) }
func (e *Engine) SetPreset(i int)              { e.push(event{kind: 3, note: i}) }
func (e *Engine) AllNotesOff()                 { e.push(event{kind: 4}) }

// Preset returns the current preset index (approximate, for display).
func (e *Engine) PresetIndex() int { return e.preset }

// ActiveVoices counts sounding voices.
func (e *Engine) ActiveVoices() int {
	n := 0
	for i := range e.voices {
		if e.voices[i].active {
			n++
		}
	}
	return n
}

// Peak returns and resets the recent output peak.
func (e *Engine) Peak() float64 { p := e.peak; e.peak *= 0.5; return p }

func (e *Engine) applyPreset(i int) {
	if i < 0 || i >= len(Presets) {
		return
	}
	e.preset = i
	p := &Presets[i]
	e.bodyL.peaking(p.BodyHz, p.BodyQ, p.BodyGainDB)
	e.bodyR = e.bodyL
	e.shelfL.highShelf(4000, p.TrebleShelf)
	e.shelfR = e.shelfL
	e.rev.set(p.RoomSize, p.RoomDamp)
}

func noteFreq(m int) float64 {
	d := float64(m - 69)
	stretch := d * d * d * 2.5e-5 // a touch of stretch tuning, in cents
	return 440 * math.Pow(2, (d+stretch/100)/12)
}

func (e *Engine) noteOn(m int, vel float64) {
	if m < 21 || m > 108 {
		return
	}
	if vel < 0.02 {
		vel = 0.02
	}
	if vel > 1 {
		vel = 1
	}
	p := &Presets[e.preset]
	// Re-strike: let the old voice of this key fade quickly.
	for i := range e.voices {
		v := &e.voices[i]
		if v.active && v.note == m && !v.fading {
			v.fading = true
			v.fadeMul = math.Exp(-1 / (0.06 * SampleRate))
		}
	}
	v := e.allocVoice()
	e.clock++
	*v = voice{oscs: v.oscs[:0], active: true, note: m, keyDown: true, age: e.clock, rng: uint32(e.rnd.Int31()) | 1}

	f0 := noteFreq(m)
	k := float64(m - 21) // 0..87
	// String stiffness: grows towards the treble; uprights also have stiff short bass strings.
	B := 1.0e-4 * math.Pow(2, k/14) * p.InharmMul
	if m < 45 {
		B += p.BassInharm * 1.6e-4 * float64(45-m) / 24
	}
	// Sustain (T60 seconds of the aftersound for the fundamental).
	t60 := 55 * math.Pow(2, -k/20) * p.SustainMul
	if t60 < 0.5 {
		t60 = 0.5
	}
	tauSlow := t60 / 6.9
	tauFast := tauSlow / 6
	// Brightness: harder hits excite more high partials.
	fc := (700 + 6500*math.Pow(vel, 1.6)) * p.Brightness * (1 + k/90)
	strings := 2
	if p.Strings3 && m >= 32 {
		strings = 3
	} else if m < 28 {
		strings = 1
	}
	detune := p.DetuneCents
	if m < 32 {
		detune *= 0.6
	}
	hasDamp := m <= 88
	// Damping time when released: bass dampers are slower.
	relTau := 0.07 + 0.35*math.Pow(2, -k/12)
	if !hasDamp {
		relTau = tauSlow
	}

	type part struct{ f, a float64 }
	parts := make([]part, 0, maxPartial)
	var sum2 float64
	for n := 1; n <= maxPartial; n++ {
		fn := float64(n) * f0 * math.Sqrt(1+B*float64(n*n))
		if fn > 15000 || fn > SampleRate*0.45 {
			break
		}
		nf := float64(n)
		a := 1 / math.Pow(nf, p.Rolloff)
		// Hammer strike position comb (never fully zero: hammer is not a point).
		a *= 0.18 + math.Abs(math.Sin(math.Pi*nf*p.StrikePos))
		// Felt hammer low-pass.
		a *= math.Exp(-fn / fc)
		// Soundboard radiation: low partials radiate poorly.
		r := p.RadiationHz
		a *= fn * fn / (fn*fn + r*r)
		// A little irregularity so partials don't sound synthetic.
		a *= 0.8 + 0.4*e.rnd.Float64()
		parts = append(parts, part{fn, a})
		sum2 += a * a
	}
	norm := 1 / math.Sqrt(sum2+1e-12)
	// Dynamics: about 40 dB from pp to ff.
	loud := 0.012 + 0.988*math.Pow(vel, 2.1)
	// Equal-loudness-ish: keep the extreme bass/treble from vanishing.
	keyGain := 1.0 + 0.25*math.Abs(k-44)/44
	g := norm * loud * keyGain * p.Gain * 0.27

	relDec := math.Exp(-1 / (relTau * SampleRate))
	amax := 0.0
	for _, pt := range parts {
		amax = math.Max(amax, pt.a)
	}
	for i, pt := range parts {
		if pt.a < amax*0.002 { // inaudible under its neighbours (-54 dB)
			continue
		}
		n := float64(i + 1)
		damp := 1 + p.PartialDamp*(0.055*(n-1)+0.18*math.Pow(pt.f/1000, 1.5))
		ts := tauSlow / damp
		tf := tauFast / damp
		// Unison beating matters most in the low partials; thin out the rest to save CPU.
		ns := strings
		if i >= 16 {
			ns = 1
		} else if i >= 7 && ns > 2 {
			ns = 2
		}
		for st := 0; st < ns; st++ {
			cents := 0.0
			if ns > 1 {
				cents = detune * (float64(st)/float64(ns-1)*2 - 1)
				cents += detune * 0.3 * (e.rnd.Float64() - 0.5)
			}
			f := pt.f * math.Pow(2, cents/1200)
			share := pt.a * g / float64(ns)
			w := 2 * math.Pi * f / SampleRate
			ph := e.rnd.Float64() * 0.4
			dS := math.Exp(-1 / (ts * SampleRate))
			v.oscs = append(v.oscs, osc{
				re: math.Cos(ph), im: math.Sin(ph),
				c: math.Cos(w), s: math.Sin(w),
				amp:    share * p.PromptRatio,
				amp2:   share * (1 - p.PromptRatio),
				decay:  math.Exp(-1 / (tf * SampleRate)),
				decay2: dS,
				relDec: math.Min(relDec, dS),
			})
		}
	}
	// Hammer felt noise.
	v.noiseAmp = 0.05 * p.NoiseLevel * math.Pow(vel, 1.5) * (0.4 + k/88)
	v.noiseEnv = 1
	v.noiseDec = math.Exp(-1 / ((0.004 + 0.01*(1-k/88)) * SampleRate))
	nfc := 1200 + 5000*vel + 20*k
	v.noiseLPa = 1 - math.Exp(-2*math.Pi*nfc/SampleRate)
	// Action / soundboard knock: a low damped thump, most audible in the treble.
	kf := 70 + 120*vel
	kw := 2 * math.Pi * kf / SampleRate
	v.knock = osc{re: 1, im: 0, c: math.Cos(kw), s: math.Sin(kw),
		amp:   0.06 * p.KnockLevel * math.Pow(vel, 1.3) * (0.3 + k/70),
		decay: math.Exp(-1 / (0.025 * SampleRate))}
	v.knockOn = true

	// Pan: player's perspective, bass left, treble right.
	pan := (k/87*2 - 1) * p.StereoWidth * 0.5
	v.gainL = math.Cos((pan + 1) * math.Pi / 4)
	v.gainR = math.Sin((pan + 1) * math.Pi / 4)
	v.hasDamp = hasDamp
	v.level = 1
}

func (e *Engine) allocVoice() *voice {
	for i := range e.voices {
		if !e.voices[i].active {
			return &e.voices[i]
		}
	}
	// Steal: prefer the quietest released voice, else the oldest.
	best, bestScore := 0, math.Inf(1)
	for i := range e.voices {
		v := &e.voices[i]
		score := v.level
		if v.keyDown {
			score += 10
		}
		score += float64(v.age) * 1e-9
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	return &e.voices[best]
}

func (e *Engine) noteOff(m int) {
	for i := range e.voices {
		v := &e.voices[i]
		if v.active && v.note == m && v.keyDown {
			v.keyDown = false
			if !e.pedal {
				v.damped = true
			}
		}
	}
}

func (e *Engine) setPedal(down bool) {
	e.pedal = down
	if !down {
		for i := range e.voices {
			v := &e.voices[i]
			if v.active && !v.keyDown {
				v.damped = true
			}
		}
	}
}

func (e *Engine) drain() {
	e.mu.Lock()
	q := e.queue
	e.queue = nil
	e.mu.Unlock()
	for _, ev := range q {
		switch ev.kind {
		case 0:
			e.noteOn(ev.note, ev.vel)
		case 1:
			e.noteOff(ev.note)
		case 2:
			e.setPedal(ev.on)
		case 3:
			e.applyPreset(ev.note)
		case 4:
			for i := range e.voices {
				v := &e.voices[i]
				if v.active {
					v.keyDown = false
					v.damped = true
				}
			}
			e.pedal = false
		}
	}
}

func (v *voice) render(outL, outR *[blockSize]float64) {
	var buf [blockSize]float64
	damped := v.damped && v.hasDamp
	peak := 0.0
	for i := range v.oscs {
		o := &v.oscs[i]
		if o.amp+o.amp2 < 1e-7 {
			continue
		}
		re, im, c, s, a, a2 := o.re, o.im, o.c, o.s, o.amp, o.amp2
		d, d2 := o.decay, o.decay2
		if damped {
			d, d2 = math.Min(d, o.relDec), o.relDec
		}
		for j := 0; j < blockSize; j++ {
			re, im = re*c-im*s, re*s+im*c
			buf[j] += (a + a2) * im
			a *= d
			a2 *= d2
		}
		// Renormalise the phasor so rounding never drifts the amplitude.
		inv := 1 / math.Sqrt(re*re+im*im)
		o.re, o.im, o.amp, o.amp2 = re*inv, im*inv, a, a2
		peak += a + a2
	}
	// Hammer noise.
	if v.noiseEnv > 1e-4 {
		x := v.rng
		lp := v.noiseLP
		for j := 0; j < blockSize; j++ {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			n := float64(int32(x)) / 2147483648.0
			lp += v.noiseLPa * (n - lp)
			buf[j] += lp * v.noiseAmp * v.noiseEnv
			v.noiseEnv *= v.noiseDec
		}
		v.rng, v.noiseLP = x, lp
	}
	if v.knockOn {
		k := &v.knock
		re, im, a := k.re, k.im, k.amp
		for j := 0; j < blockSize; j++ {
			re, im = re*k.c-im*k.s, re*k.s+im*k.c
			buf[j] += a * im
			a *= k.decay
		}
		k.re, k.im, k.amp = re, im, a
		if a < 1e-6 {
			v.knockOn = false
		}
	}
	// Attack ramp (1 ms) removes clicks; fade for re-struck keys.
	for j := 0; j < blockSize; j++ {
		if v.attack < 48 {
			buf[j] *= float64(v.attack) / 48
			v.attack++
		}
	}
	if v.fading {
		if v.relMul == 0 {
			v.relMul = 1
		}
		for j := 0; j < blockSize; j++ {
			buf[j] *= v.relMul
			v.relMul *= v.fadeMul
		}
		peak *= v.relMul
	}
	for j := 0; j < blockSize; j++ {
		outL[j] += buf[j] * v.gainL
		outR[j] += buf[j] * v.gainR
	}
	v.level = peak
	if peak < 2e-5 && !v.knockOn && v.noiseEnv < 1e-4 {
		v.active = false
	}
}

func (e *Engine) renderBlock() {
	e.drain()
	for j := range e.bufL {
		e.bufL[j], e.bufR[j] = 0, 0
	}
	for i := range e.voices {
		if e.voices[i].active {
			e.voices[i].render(&e.bufL, &e.bufR)
		}
	}
	p := &Presets[e.preset]
	wet := p.RoomWet * e.Reverb
	if e.pedal {
		wet *= 1.25 // open strings ring sympathetically with the pedal down
	}
	vol := e.Volume
	for j := 0; j < blockSize; j++ {
		l := e.shelfL.run(e.bodyL.run(e.bufL[j]))
		r := e.shelfR.run(e.bodyR.run(e.bufR[j]))
		rl, rr := e.rev.process(l, r)
		l = (l + rl*wet*3) * vol
		r = (r + rr*wet*3) * vol
		// Peak limiter: instant attack, ~200 ms release, so big chords never crunch.
		const thr = 0.72
		env := math.Max(math.Abs(l), math.Abs(r))
		target := 1.0
		if env > thr {
			target = thr / env
		}
		if target < e.limGain {
			e.limGain = target
		} else {
			e.limGain += (target - e.limGain) * 0.0001
		}
		l *= e.limGain
		r *= e.limGain
		e.bufL[j], e.bufR[j] = softClip(l), softClip(r)
		if a := math.Abs(e.bufL[j]); a > e.peak {
			e.peak = a
		}
		if a := math.Abs(e.bufR[j]); a > e.peak {
			e.peak = a
		}
	}
}

func softClip(x float64) float64 {
	const t = 0.85
	if x > t {
		return t + (1-t)*math.Tanh((x-t)/(1-t))
	}
	if x < -t {
		return -t - (1-t)*math.Tanh((-x-t)/(1-t))
	}
	return x
}

// RenderF32 fills interleaved stereo float32 frames.
func (e *Engine) RenderF32(out []float32) {
	for len(out) > 0 {
		if len(e.pending) == 0 {
			e.renderBlock()
			if cap(e.pending) < blockSize*2 {
				e.pending = make([]float32, 0, blockSize*2)
			}
			e.pending = e.pending[:blockSize*2]
			for j := 0; j < blockSize; j++ {
				e.pending[2*j] = float32(e.bufL[j])
				e.pending[2*j+1] = float32(e.bufR[j])
			}
		}
		n := copy(out, e.pending)
		out = out[n:]
		e.pending = e.pending[n:]
	}
}

// Read implements io.Reader producing little-endian float32 stereo (for ebiten's NewPlayerF32).
func (e *Engine) Read(b []byte) (int, error) {
	frames := len(b) / 8
	if frames == 0 {
		return 0, nil
	}
	if cap(e.readBuf) < frames*2 {
		e.readBuf = make([]float32, frames*2)
	}
	tmp := e.readBuf[:frames*2]
	e.RenderF32(tmp)
	for i, f := range tmp {
		u := math.Float32bits(f)
		b[4*i] = byte(u)
		b[4*i+1] = byte(u >> 8)
		b[4*i+2] = byte(u >> 16)
		b[4*i+3] = byte(u >> 24)
	}
	return frames * 8, nil
}
