// Package synth is the sample-playback engine: real recorded instruments with velocity
// crossfades, pitch-shifted zones, dampers, sustain pedal and a per-preset effects chain.
package synth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"math/rand"
	"path"
	"runtime"
	"sort"
	"sync"

	"github.com/jfreymuth/oggvorbis"
)

const (
	SampleRate = 48000
	maxVoices  = 128
	blockSize  = 64
	maxParts   = 6
	storeGain  = 2.4 // samples are stored with 14 dB headroom
)

// Zone is one recorded note at one velocity layer.
type Zone struct {
	Root      int
	Layer     int
	Ch        int
	Frames    int
	LoopStart int
	LoopEnd   int
	Tune      float64 // cents the recording is off its root
	Data      []int16 // interleaved
}

// SampleSet is a whole instrument.
type SampleSet struct {
	Key    string
	Name   string
	Layers [][]*Zone // per layer, sorted by root
}

type indexFile struct {
	Name     string `json:"name"`
	Channels int    `json:"channels"`
	Layers   int    `json:"layers"`
	Zones    []struct {
		File      string  `json:"file"`
		Root      int     `json:"root"`
		Layer     int     `json:"layer"`
		Frames    int     `json:"frames"`
		LoopStart int     `json:"loopStart"`
		LoopEnd   int     `json:"loopEnd"`
		Tune      float64 `json:"tune"`
	} `json:"zones"`
}

// LoadSet decodes one sample set from fsys (uses all CPU cores).
func LoadSet(fsys fs.FS, key string) (*SampleSet, error) {
	raw, err := fs.ReadFile(fsys, path.Join(key, "index.json"))
	if err != nil {
		return nil, err
	}
	var idx indexFile
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, err
	}
	set := &SampleSet{Key: key, Name: idx.Name, Layers: make([][]*Zone, idx.Layers)}
	zones := make([]*Zone, len(idx.Zones))
	jobs := make(chan int)
	errs := make(chan error, len(idx.Zones))
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				zi := idx.Zones[i]
				b, err := fs.ReadFile(fsys, path.Join(key, zi.File))
				if err != nil {
					errs <- err
					continue
				}
				pcm, format, err := oggvorbis.ReadAll(bytes.NewReader(b))
				if err != nil {
					errs <- fmt.Errorf("%s/%s: %w", key, zi.File, err)
					continue
				}
				data := make([]int16, len(pcm))
				for j, v := range pcm {
					if v > 1 {
						v = 1
					} else if v < -1 {
						v = -1
					}
					data[j] = int16(v * 32767)
				}
				z := &Zone{Root: zi.Root, Layer: zi.Layer, Ch: format.Channels, Data: data,
					Frames: len(pcm) / format.Channels, LoopStart: zi.LoopStart, LoopEnd: zi.LoopEnd, Tune: zi.Tune}
				if z.LoopEnd > z.Frames-4 {
					z.LoopEnd = z.Frames - 4
				}
				zones[i] = z
			}
		}()
	}
	for i := range idx.Zones {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		return nil, err
	}
	for _, z := range zones {
		if z.Layer >= 0 && z.Layer < len(set.Layers) {
			set.Layers[z.Layer] = append(set.Layers[z.Layer], z)
		}
	}
	for _, l := range set.Layers {
		sort.Slice(l, func(a, b int) bool { return l[a].Root < l[b].Root })
	}
	return set, nil
}

// nearest picks the zone in a layer closest to the note (ties go to the zone below, so it is
// pitched up a little, which tends to sound more natural than pitching down).
func (s *SampleSet) nearest(layer, note int) *Zone {
	l := s.Layers[layer]
	if len(l) == 0 {
		return nil
	}
	i := sort.Search(len(l), func(i int) bool { return l[i].Root >= note })
	if i == len(l) {
		return l[len(l)-1]
	}
	if i == 0 || l[i].Root == note {
		return l[i]
	}
	if note-l[i-1].Root <= l[i].Root-note {
		return l[i-1]
	}
	return l[i]
}

type part struct {
	z    *Zone
	pos  float64
	inc  float64
	gain float64
	done bool
}

type voice struct {
	active   bool
	note     int
	keyDown  bool
	damped   bool
	fading   bool
	age      int64
	parts    [maxParts]part
	np       int
	env      float64
	relMul   float64
	holdMul  float64
	fadeMul  float64
	atk      float64
	atkInc   float64
	gainL    float64
	gainR    float64
	noDamper bool
}

type event struct {
	kind int // 0 on, 1 off, 2 pedal, 3 preset, 4 all off
	note int
	vel  float64
	on   bool
	pre  *Preset
	set  *SampleSet
}

// Engine mixes all voices. NoteOn etc. are safe from any goroutine.
type Engine struct {
	mu    sync.Mutex
	queue []event

	voices []voice
	pre    *Preset
	set    *SampleSet
	pedal  bool
	clock  int64

	Volume float64
	Reverb float64

	fx      *fxChain
	bufL    [blockSize]float64
	bufR    [blockSize]float64
	pending []float32
	readBuf []float32
	peak    float64
	limGain float64
	rnd     *rand.Rand

	loadMu sync.Mutex
	sets   map[string]*SampleSet
	lru    []string
	assets fs.FS
}

func NewEngine(assets fs.FS) *Engine {
	return &Engine{
		voices:  make([]voice, maxVoices),
		Volume:  0.8,
		Reverb:  1,
		fx:      newFX(),
		limGain: 1,
		rnd:     rand.New(rand.NewSource(1)),
		sets:    map[string]*SampleSet{},
		assets:  assets,
	}
}

// Load decodes the sample set needed by preset i (blocking; cached).
func (e *Engine) Load(i int) (*SampleSet, error) {
	key := Presets[i].Set
	e.loadMu.Lock()
	defer e.loadMu.Unlock()
	if s := e.sets[key]; s != nil {
		for j, k := range e.lru { // mark as most recently used
			if k == key {
				e.lru = append(append(e.lru[:j:j], e.lru[j+1:]...), key)
				break
			}
		}
		return s, nil
	}
	s, err := LoadSet(e.assets, key)
	if err != nil {
		return nil, err
	}
	e.sets[key] = s
	e.lru = append(e.lru, key)
	// Keep memory in check: hold at most 3 instruments decoded. Voices still playing an evicted
	// set keep their samples alive until they finish, so nothing cuts out.
	for len(e.lru) > 3 {
		delete(e.sets, e.lru[0])
		e.lru = e.lru[1:]
	}
	return s, nil
}

// Loaded reports whether preset i can be selected without waiting.
func (e *Engine) Loaded(i int) bool {
	e.loadMu.Lock()
	defer e.loadMu.Unlock()
	return e.sets[Presets[i].Set] != nil
}

func (e *Engine) push(ev event) {
	e.mu.Lock()
	e.queue = append(e.queue, ev)
	e.mu.Unlock()
}

func (e *Engine) NoteOn(midi int, vel float64) { e.push(event{kind: 0, note: midi, vel: vel}) }
func (e *Engine) NoteOff(midi int)             { e.push(event{kind: 1, note: midi}) }
func (e *Engine) SetPedal(down bool)           { e.push(event{kind: 2, on: down}) }
func (e *Engine) AllNotesOff()                 { e.push(event{kind: 4}) }

// SetPreset switches to preset i. Its sample set must already be loaded (see Load).
func (e *Engine) SetPreset(i int) error {
	s, err := e.Load(i)
	if err != nil {
		return err
	}
	e.push(event{kind: 3, pre: &Presets[i], set: s})
	return nil
}

func (e *Engine) ActiveVoices() int {
	n := 0
	for i := range e.voices {
		if e.voices[i].active {
			n++
		}
	}
	return n
}

func (e *Engine) Peak() float64 { p := e.peak; e.peak *= 0.5; return p }

func (e *Engine) allocVoice() *voice {
	for i := range e.voices {
		if !e.voices[i].active {
			return &e.voices[i]
		}
	}
	best, score := 0, math.Inf(1)
	for i := range e.voices {
		v := &e.voices[i]
		s := v.env + float64(v.age)*1e-9
		if v.keyDown {
			s += 10
		}
		if s < score {
			best, score = i, s
		}
	}
	return &e.voices[best]
}

func (e *Engine) addParts(v *voice, note int, vel float64, cents, gain float64) {
	p, s := e.pre, e.set
	nl := len(s.Layers)
	pos := vel * float64(nl-1)
	i0 := int(pos)
	if i0 >= nl-1 {
		i0 = nl - 1
	}
	f := pos - float64(i0)
	if nl == 1 {
		f = 0
	}
	type lw struct {
		l int
		w float64
	}
	ls := []lw{{i0, math.Cos(f * math.Pi / 2)}}
	if f > 0.02 && i0+1 < nl {
		ls = append(ls, lw{i0 + 1, math.Sin(f * math.Pi / 2)})
	}
	dets := []float64{0}
	if p.Detune > 0 {
		dets = []float64{-p.Detune / 2, p.Detune / 2}
	}
	for _, l := range ls {
		z := s.nearest(l.l, note)
		if z == nil {
			continue
		}
		for _, d := range dets {
			if v.np >= maxParts {
				return
			}
			g := l.w * gain
			if len(dets) > 1 {
				g *= 0.62
			}
			v.parts[v.np] = part{z: z, inc: math.Pow(2, (float64(note-z.Root)+(cents+d-z.Tune)/100)/12), gain: g}
			v.np++
		}
	}
}

func (e *Engine) noteOn(note int, vel float64) {
	if e.set == nil || note < 0 || note > 127 {
		return
	}
	p := e.pre
	vel = math.Max(0.01, math.Min(1, vel))
	vel = math.Pow(vel, p.VelGamma)
	for i := range e.voices {
		v := &e.voices[i]
		if v.active && v.note == note && !v.fading {
			v.fading = true
			v.fadeMul = math.Exp(-1 / (0.07 * SampleRate))
		}
	}
	v := e.allocVoice()
	e.clock++
	*v = voice{active: true, note: note, keyDown: true, age: e.clock, env: 1, fadeMul: 1}
	gain := math.Pow(10, -p.VelRange*(1-vel)/20) * storeGain * p.gainLin()
	e.addParts(v, note, vel, 0, gain)
	if p.OctaveUp > 0 && note+12 <= 127 {
		e.addParts(v, note+12, vel*0.8, 0, gain*p.OctaveUp)
	}
	if v.np == 0 {
		v.active = false
		return
	}
	// Dampers: bass dampers take longer to stop the heavy strings.
	tau := p.Release * (1 + math.Max(0, float64(60-note))/40)
	v.relMul = math.Exp(-1 / (tau * SampleRate))
	v.holdMul = 1
	if p.HoldDecay > 0 {
		v.holdMul = math.Exp(-1 / (p.HoldDecay * SampleRate))
	}
	v.noDamper = p.Piano && note > 88
	v.atk = 1
	if p.AttackMs > 0 {
		v.atk = 0
		v.atkInc = 1 / (p.AttackMs / 1000 * SampleRate)
	}
	// Mono instruments are panned by pitch from the player's seat.
	pan := (float64(note-64) / 44) * 0.35
	v.gainL = math.Cos((pan + 1) * math.Pi / 4 * 1)
	v.gainR = math.Sin((pan + 1) * math.Pi / 4 * 1)
	if e.set.Layers[0][0].Ch == 2 {
		v.gainL, v.gainR = 1, 1
	}
}

func (e *Engine) noteOff(note int) {
	for i := range e.voices {
		v := &e.voices[i]
		if v.active && v.note == note && v.keyDown {
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
			if e.set != nil && e.set != ev.set {
				// Different instrument: let the old notes ring out quickly.
				for i := range e.voices {
					v := &e.voices[i]
					if v.active {
						v.fading = true
						v.fadeMul = math.Exp(-1 / (0.15 * SampleRate))
					}
				}
			}
			e.pre, e.set = ev.pre, ev.set
			e.fx.configure(ev.pre)
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

// cubic Hermite interpolation
func hermite(xm1, x0, x1, x2, t float64) float64 {
	c := (x1 - xm1) * 0.5
	v := x0 - x1
	w := c + v
	a := w + v + (x2-x0)*0.5
	b := w + a
	return ((a*t-b)*t+c)*t + x0
}

func (v *voice) render(outL, outR *[blockSize]float64) {
	var bl, br [blockSize]float64
	alive := false
	for pi := 0; pi < v.np; pi++ {
		p := &v.parts[pi]
		if p.done {
			continue
		}
		z := p.z
		d := z.Data
		loop := z.LoopEnd > z.LoopStart+64
		last := z.Frames - 3
		pos, inc, g := p.pos, p.inc, p.gain/32768
		for j := 0; j < blockSize; j++ {
			if loop && pos >= float64(z.LoopEnd) {
				pos -= float64(z.LoopEnd - z.LoopStart)
			}
			if pos >= float64(last) {
				p.done = true
				break
			}
			i := int(pos)
			t := pos - float64(i)
			im := i - 1
			if im < 0 {
				im = 0
			}
			if z.Ch == 2 {
				l := hermite(float64(d[2*im]), float64(d[2*i]), float64(d[2*i+2]), float64(d[2*i+4]), t)
				r := hermite(float64(d[2*im+1]), float64(d[2*i+1]), float64(d[2*i+3]), float64(d[2*i+5]), t)
				bl[j] += l * g
				br[j] += r * g
			} else {
				s := hermite(float64(d[im]), float64(d[i]), float64(d[i+1]), float64(d[i+2]), t) * g
				bl[j] += s
				br[j] += s
			}
			pos += inc
		}
		p.pos = pos
		if !p.done {
			alive = true
		}
	}
	damped := v.damped && !v.noDamper
	for j := 0; j < blockSize; j++ {
		if v.atk < 1 {
			v.atk = math.Min(1, v.atk+v.atkInc)
		}
		if damped {
			v.env *= v.relMul
		} else if v.keyDown || v.holdMul < 1 {
			v.env *= v.holdMul
		}
		if v.fading {
			v.env *= v.fadeMul
		}
		g := v.env * v.atk
		outL[j] += bl[j] * g * v.gainL
		outR[j] += br[j] * g * v.gainR
	}
	if !alive || v.env < 1e-4 {
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
	if e.pre == nil {
		return
	}
	e.fx.process(&e.bufL, &e.bufR, e.Reverb, e.pedal)
	vol := e.Volume
	for j := 0; j < blockSize; j++ {
		l, r := e.bufL[j]*vol, e.bufR[j]*vol
		const thr = 0.8
		env := math.Max(math.Abs(l), math.Abs(r))
		target := 1.0
		if env > thr {
			target = thr / env
		}
		if target < e.limGain {
			e.limGain = target
		} else {
			e.limGain += (target - e.limGain) * 0.0002
		}
		l, r = softClip(l*e.limGain), softClip(r*e.limGain)
		e.bufL[j], e.bufR[j] = l, r
		if a := math.Max(math.Abs(l), math.Abs(r)); a > e.peak {
			e.peak = a
		}
	}
}

func softClip(x float64) float64 {
	const t = 0.9
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

// Read implements io.Reader producing little-endian float32 stereo (ebiten NewPlayerF32).
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
