// Piano Player: a standalone acoustic piano you play with your computer keyboard.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/codypowpow-afk/piano-player/assets"
	"github.com/codypowpow-afk/piano-player/internal/appicon"
	"github.com/codypowpow-afk/piano-player/synth"
)

const (
	screenW = 1400
	screenH = 600

	kbX     = 24
	kbY     = 320
	whiteW  = 26
	whiteH  = 236
	blackW  = 16
	blackH  = 150
	lowNote = 21
	topNote = 108
)

// Computer-keyboard layout (like most DAWs): two rows of white keys with the black keys above them.
type keyMap struct {
	key    ebiten.Key
	offset int // semitones from the row's C
	label  string
}

var lowerRow = []keyMap{
	{ebiten.KeyZ, 0, "Z"}, {ebiten.KeyS, 1, "S"}, {ebiten.KeyX, 2, "X"}, {ebiten.KeyD, 3, "D"},
	{ebiten.KeyC, 4, "C"}, {ebiten.KeyV, 5, "V"}, {ebiten.KeyG, 6, "G"}, {ebiten.KeyB, 7, "B"},
	{ebiten.KeyH, 8, "H"}, {ebiten.KeyN, 9, "N"}, {ebiten.KeyJ, 10, "J"}, {ebiten.KeyM, 11, "M"},
	{ebiten.KeyComma, 12, ","}, {ebiten.KeyL, 13, "L"}, {ebiten.KeyPeriod, 14, "."},
	{ebiten.KeySemicolon, 15, ";"}, {ebiten.KeySlash, 16, "/"},
}

var upperRow = []keyMap{
	{ebiten.KeyQ, 0, "Q"}, {ebiten.Key2, 1, "2"}, {ebiten.KeyW, 2, "W"}, {ebiten.Key3, 3, "3"},
	{ebiten.KeyE, 4, "E"}, {ebiten.KeyR, 5, "R"}, {ebiten.Key5, 6, "5"}, {ebiten.KeyT, 7, "T"},
	{ebiten.Key6, 8, "6"}, {ebiten.KeyY, 9, "Y"}, {ebiten.Key7, 10, "7"}, {ebiten.KeyU, 11, "U"},
	{ebiten.KeyI, 12, "I"}, {ebiten.Key9, 13, "9"}, {ebiten.KeyO, 14, "O"}, {ebiten.Key0, 15, "0"},
	{ebiten.KeyP, 16, "P"}, {ebiten.KeyBracketLeft, 17, "["}, {ebiten.KeyEqual, 18, "="},
	{ebiten.KeyBracketRight, 19, "]"},
}

var (
	colBG      = color.RGBA{0x17, 0x15, 0x14, 0xff}
	colPanel   = color.RGBA{0x24, 0x21, 0x1f, 0xff}
	colPanelHi = color.RGBA{0x33, 0x2e, 0x2a, 0xff}
	colText    = color.RGBA{0xee, 0xe6, 0xda, 0xff}
	colDim     = color.RGBA{0x9a, 0x90, 0x84, 0xff}
	colAccent  = color.RGBA{0xe0, 0xa9, 0x58, 0xff}
	colAccent2 = color.RGBA{0x7f, 0xb8, 0xc9, 0xff}
	colIvory   = color.RGBA{0xf6, 0xf0, 0xe4, 0xff}
	colIvoryLo = color.RGBA{0xd9, 0xd0, 0xc0, 0xff}
	colEbony   = color.RGBA{0x14, 0x12, 0x11, 0xff}
	colEbonyHi = color.RGBA{0x3a, 0x35, 0x31, 0xff}
	colFelt    = color.RGBA{0x7a, 0x1f, 0x24, 0xff}
)

type slider struct {
	label    string
	x, y, w  float32
	value    *float64
	min, max float64
	format   func(float64) string
}

type Game struct {
	eng    *synth.Engine
	player *audio.Player

	base      int                // MIDI note of the lower row's Z key
	held      map[ebiten.Key]int // physical key -> note it started
	down      [128]int           // how many sources hold each note
	pedal     bool
	pedalKey  bool
	pedalLock bool
	touch     float64
	volume    float64
	reverb    float64
	preset    int

	mouseNote  int
	dragSlider *slider
	sliders    []*slider

	keyX    [128]float32
	isBlack [128]bool
	rnd     *rand.Rand
	meter   float64

	want        atomic.Int32 // preset the user asked for (loading may lag behind)
	loading     atomic.Bool
	midiProgram atomic.Int32 // pending MIDI program change + 1
	midi        midiState
	midiIn      midiInput
	kbPedal     atomic.Bool
	midiSeen    int64
	midiFlashT  time.Time

	face, faceSmall, faceBold, faceTitle text.Face
	faceBtn, faceTiny                    text.Face
	flashMu                              sync.Mutex
	flash                                string
	flashT                               time.Time
}

func isBlackNote(m int) bool {
	switch m % 12 {
	case 1, 3, 6, 8, 10:
		return true
	}
	return false
}

func noteName(m int) string {
	names := []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	return fmt.Sprintf("%s%d", names[m%12], m/12-1)
}

func newGame() *Game {
	g := &Game{
		eng:       synth.NewEngine(assets.FS),
		base:      48,
		held:      map[ebiten.Key]int{},
		touch:     0.62,
		volume:    0.8,
		reverb:    1.0,
		mouseNote: -1,
		rnd:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	// Key geometry.
	wi := 0
	for m := lowNote; m <= topNote; m++ {
		if isBlackNote(m) {
			g.isBlack[m] = true
			g.keyX[m] = float32(kbX+wi*whiteW) - blackW/2
		} else {
			g.keyX[m] = float32(kbX + wi*whiteW)
			wi++
		}
	}
	src := func(b []byte) *text.GoTextFaceSource {
		s, err := text.NewGoTextFaceSource(bytes.NewReader(b))
		if err != nil {
			log.Fatal(err)
		}
		return s
	}
	reg, bold := src(goregular.TTF), src(gobold.TTF)
	g.face = &text.GoTextFace{Source: reg, Size: 15}
	g.faceSmall = &text.GoTextFace{Source: bold, Size: 11}
	g.faceBold = &text.GoTextFace{Source: bold, Size: 16}
	g.faceTitle = &text.GoTextFace{Source: bold, Size: 26}
	g.faceBtn = &text.GoTextFace{Source: bold, Size: 14}
	g.faceTiny = &text.GoTextFace{Source: bold, Size: 9}

	pct := func(v float64) string { return fmt.Sprintf("%d%%", int(math.Round(v*100))) }
	g.sliders = []*slider{
		{label: "VOLUME", x: sideX, y: 58, w: sideW, value: &g.volume, min: 0, max: 1.2, format: pct},
		{label: "ROOM / REVERB", x: sideX, y: 104, w: sideW, value: &g.reverb, min: 0, max: 2, format: pct},
		{label: "TOUCH (computer keys)  ↑↓", x: sideX, y: 150, w: sideW, value: &g.touch, min: 0.1, max: 1, format: func(v float64) string {
			dyn := []string{"ppp", "pp", "p", "mp", "mf", "f", "ff", "fff"}
			i := int((v - 0.1) / 0.9 * float64(len(dyn)))
			if i >= len(dyn) {
				i = len(dyn) - 1
			}
			return dyn[i]
		}},
	}

	ctx := audio.NewContext(synth.SampleRate)
	p, err := ctx.NewPlayerF32(g.eng)
	if err != nil {
		log.Fatal(err)
	}
	p.SetBufferSize(20 * time.Millisecond) // low latency for MIDI keyboards
	p.Play()
	g.player = p
	g.midi.curve.Store(1)
	g.midiIn = startMIDI(g.handleMIDI)
	g.setPreset(0)
	return g
}

func (g *Game) noteOn(m int, vel float64) {
	if m < lowNote || m > topNote {
		return
	}
	g.down[m]++
	g.eng.NoteOn(m, vel)
}

func (g *Game) noteOff(m int) {
	if m < lowNote || m > topNote || g.down[m] == 0 {
		return
	}
	g.down[m]--
	if g.down[m] == 0 {
		g.eng.NoteOff(m)
	}
}

func (g *Game) keyVelocity() float64 {
	v := g.touch + (g.rnd.Float64()-0.5)*0.06 // a little human variation
	if ebiten.IsKeyPressed(ebiten.KeyShift) {
		v += 0.22 // accent
	}
	if ebiten.IsKeyPressed(ebiten.KeyControl) {
		v -= 0.22 // soft
	}
	return math.Max(0.05, math.Min(1, v))
}

// setPreset selects preset i. Its samples are decoded in the background the first time, while
// the previous sound keeps playing.
func (g *Game) setPreset(i int) {
	n := len(synth.Presets)
	i = ((i % n) + n) % n
	g.preset = i
	g.want.Store(int32(i))
	if g.eng.Loaded(i) {
		g.eng.SetPreset(i)
		g.toast(synth.Presets[i].Name)
		return
	}
	g.loading.Store(true)
	go func() {
		_, err := g.eng.Load(i)
		if g.want.Load() != int32(i) {
			return // the user picked something else meanwhile
		}
		if err != nil {
			g.toast("Could not load " + synth.Presets[i].Name + ": " + err.Error())
		} else {
			g.eng.SetPreset(i)
			g.toast(synth.Presets[i].Name)
		}
		g.loading.Store(false)
	}()
}

func (g *Game) firstOf(cat string) int {
	for i, p := range synth.Presets {
		if p.Category == cat {
			return i
		}
	}
	return 0
}

func (g *Game) toast(s string) {
	g.flashMu.Lock()
	g.flash, g.flashT = s, time.Now()
	g.flashMu.Unlock()
}

func (g *Game) shiftOctave(d int) {
	nb := g.base + 12*d
	if nb < 24 || nb > 84 {
		return
	}
	g.base = nb
	g.toast(fmt.Sprintf("Z key = %s", noteName(g.base)))
}

func (g *Game) Update() error {
	// Sounds: F1-F4 jump to a category, Tab / PgUp / PgDn step through every preset.
	for i, k := range []ebiten.Key{ebiten.KeyF1, ebiten.KeyF2, ebiten.KeyF3, ebiten.KeyF4} {
		if inpututil.IsKeyJustPressed(k) {
			g.setPreset(g.firstOf(categories[i]))
		}
	}
	if pc := g.midiProgram.Swap(0); pc > 0 {
		g.setPreset(int(pc-1) % len(synth.Presets))
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageDown) {
		g.setPreset(g.preset + 1)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageUp) {
		g.setPreset(g.preset - 1)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF5) {
		g.cycleCurve()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF6) {
		g.midiIn.Rescan()
		g.toast("Rescanned MIDI devices")
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		if ebiten.IsKeyPressed(ebiten.KeyShift) {
			g.setPreset(g.preset - 1)
		} else {
			g.setPreset(g.preset + 1)
		}
	}
	// Octave / touch.
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft) {
		g.shiftOctave(-1)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowRight) {
		g.shiftOctave(1)
	}
	if k := inpututil.KeyPressDuration(ebiten.KeyArrowUp); k == 1 || (k > 90 && k%15 == 0) {
		g.touch = math.Min(1, g.touch+0.05)
	}
	if k := inpututil.KeyPressDuration(ebiten.KeyArrowDown); k == 1 || (k > 90 && k%15 == 0) {
		g.touch = math.Max(0.1, g.touch-0.05)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.panic()
	}

	// Sustain pedal: hold Space, or Caps Lock / Enter toggles a latched pedal.
	g.pedalKey = ebiten.IsKeyPressed(ebiten.KeySpace)
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyCapsLock) {
		g.pedalLock = !g.pedalLock
	}
	kb := g.pedalKey || g.pedalLock
	g.kbPedal.Store(kb)
	if pd := kb || g.midi.sustain.Load(); pd != g.pedal {
		g.pedal = pd
		g.eng.SetPedal(pd)
	}

	// Notes from the computer keyboard.
	for row, keys := range [][]keyMap{lowerRow, upperRow} {
		for _, km := range keys {
			if inpututil.IsKeyJustPressed(km.key) {
				if _, ok := g.held[km.key]; !ok {
					m := g.base + 12*row + km.offset
					g.held[km.key] = m
					g.noteOn(m, g.keyVelocity())
				}
			}
			if inpututil.IsKeyJustReleased(km.key) {
				if m, ok := g.held[km.key]; ok {
					delete(g.held, km.key)
					g.noteOff(m)
				}
			}
		}
	}
	// Safety: if the window lost focus, release stuck keys.
	if !ebiten.IsFocused() && len(g.held) > 0 {
		for k, m := range g.held {
			delete(g.held, k)
			g.noteOff(m)
		}
	}

	g.updateMouse()
	if a := g.midi.activity.Load(); a != g.midiSeen {
		g.midiSeen, g.midiFlashT = a, time.Now()
	}

	g.eng.Volume = g.volume
	g.eng.Reverb = g.reverb
	g.meter = math.Max(g.eng.Peak(), g.meter*0.9)
	return nil
}

func (g *Game) cycleCurve() {
	c := (g.midi.curve.Load() + 1) % int32(len(velCurves))
	g.midi.curve.Store(c)
	g.toast("MIDI velocity: " + velCurves[c].name)
}

func (g *Game) panic() {
	for k, m := range g.held {
		delete(g.held, k)
		g.noteOff(m)
	}
	for i := range g.down {
		g.down[i] = 0
	}
	g.mouseNote = -1
	g.pedalLock = false
	for i := range g.midi.down {
		g.midi.down[i].Store(0)
	}
	g.midi.sustain.Store(false)
	g.eng.AllNotesOff()
	g.toast("All notes off")
}

func (g *Game) noteAt(x, y int) (int, float64) {
	fx, fy := float32(x), float32(y)
	if fy < kbY || fy > kbY+whiteH {
		return -1, 0
	}
	if fy < kbY+blackH {
		for m := lowNote; m <= topNote; m++ {
			if g.isBlack[m] && fx >= g.keyX[m] && fx < g.keyX[m]+blackW {
				return m, 0.25 + 0.75*float64(fy-kbY)/blackH
			}
		}
	}
	for m := lowNote; m <= topNote; m++ {
		if !g.isBlack[m] && fx >= g.keyX[m] && fx < g.keyX[m]+whiteW {
			return m, 0.25 + 0.75*float64(fy-kbY)/whiteH
		}
	}
	return -1, 0
}

func (g *Game) updateMouse() {
	x, y := ebiten.CursorPosition()
	pressed := ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		// Buttons.
		for i := range synth.Presets {
			bx, by, bw, bh := presetRect(i)
			if inRect(x, y, bx, by, bw, bh) {
				g.setPreset(i)
				return
			}
		}
		if inRect(x, y, 24, ctlY, 34, 34) {
			g.shiftOctave(-1)
		}
		if inRect(x, y, 196, ctlY, 34, 34) {
			g.shiftOctave(1)
		}
		if inRect(x, y, 250, ctlY, 220, 34) {
			g.pedalLock = !g.pedalLock
		}
		if inRect(x, y, midiX+midiW-200, ctlY, 200, 34) {
			g.cycleCurve()
			return
		}
		if inRect(x, y, midiX, ctlY, midiW-200, 34) {
			g.midiIn.Rescan()
			g.toast("Rescanned MIDI devices")
			return
		}
		for _, s := range g.sliders {
			if inRect(x, y, int(s.x)-6, int(s.y)+14, int(s.w)+12, 26) {
				g.dragSlider = s
			}
		}
	}
	if g.dragSlider != nil {
		if !pressed {
			g.dragSlider = nil
		} else {
			s := g.dragSlider
			t := math.Max(0, math.Min(1, float64((float32(x)-s.x)/s.w)))
			*s.value = s.min + t*(s.max-s.min)
		}
		return
	}
	// Play keys with the mouse (glissando by dragging).
	if pressed {
		m, vel := g.noteAt(x, y)
		if m != g.mouseNote {
			if g.mouseNote >= 0 {
				g.noteOff(g.mouseNote)
			}
			g.mouseNote = m
			if m >= 0 {
				g.noteOn(m, vel)
			}
		}
	} else if g.mouseNote >= 0 {
		g.noteOff(g.mouseNote)
		g.mouseNote = -1
	}
}

func inRect(x, y, rx, ry, rw, rh int) bool {
	return x >= rx && x < rx+rw && y >= ry && y < ry+rh
}

// Preset grid: 6 columns x 4 rows on the left, controls on the right.
const (
	gridX, gridY = 24, 58
	cellW, cellH = 146, 40
	cellGap      = 6
	gridCols     = 6
	sideX        = 960
	sideW        = screenW - 24 - sideX
	ctlY         = 252
)

var categories = []string{"Grand", "Upright", "Character", "Keys"}

var catColor = map[string]color.RGBA{
	"Grand":     {0xe0, 0xa9, 0x58, 0xff},
	"Upright":   {0x7f, 0xb8, 0xc9, 0xff},
	"Character": {0xd8, 0x84, 0x9b, 0xff},
	"Keys":      {0xa3, 0x95, 0xe0, 0xff},
}

func presetRect(i int) (int, int, int, int) {
	c, r := i%gridCols, i/gridCols
	return gridX + c*(cellW+cellGap), gridY + r*(cellH+cellGap), cellW, cellH
}

func (g *Game) drawText(dst *ebiten.Image, s string, face text.Face, x, y float64, clr color.Color, align text.Align) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(clr)
	op.PrimaryAlign = align
	text.Draw(dst, s, face, op)
}

func rect(dst *ebiten.Image, x, y, w, h float32, c color.Color) {
	vector.DrawFilledRect(dst, x, y, w, h, c, false)
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(colBG)
	g.drawText(screen, "PIANO PLAYER", g.faceTitle, 24, 14, colText, text.AlignStart)

	// Sound presets.
	for i, p := range synth.Presets {
		x, y, w, h := presetRect(i)
		sel := i == g.preset
		c := colPanel
		if sel {
			c = colPanelHi
		}
		rect(screen, float32(x), float32(y), float32(w), float32(h), c)
		cc := catColor[p.Category]
		rect(screen, float32(x), float32(y), 4, float32(h), cc)
		tc := colDim
		if sel {
			tc = colText
			rect(screen, float32(x), float32(y+h-3), float32(w), 3, cc)
		}
		g.drawText(screen, p.Name, g.faceBtn, float64(x+12), float64(y+6), tc, text.AlignStart)
		g.drawText(screen, strings.ToUpper(p.Category), g.faceTiny, float64(x+12), float64(y+24), cc, text.AlignStart)
		if sel && g.loading.Load() {
			g.drawText(screen, "loading…", g.faceTiny, float64(x+w-8), float64(y+24), colText, text.AlignEnd)
		}
	}

	// Sliders.
	for _, s := range g.sliders {
		g.drawText(screen, s.label, g.faceSmall, float64(s.x), float64(s.y), colDim, text.AlignStart)
		g.drawText(screen, s.format(*s.value), g.faceSmall, float64(s.x+s.w), float64(s.y), colText, text.AlignEnd)
		t := float32((*s.value - s.min) / (s.max - s.min))
		rect(screen, s.x, s.y+25, s.w, 4, colPanelHi)
		rect(screen, s.x, s.y+25, s.w*t, 4, colAccent)
		vector.DrawFilledCircle(screen, s.x+s.w*t, s.y+27, 8, colText, true)
	}
	// Output meter + what's playing.
	mx, my := float32(sideX), float32(198)
	g.drawText(screen, "LEVEL", g.faceSmall, float64(mx), float64(my), colDim, text.AlignStart)
	rect(screen, mx+50, my+3, sideW-50, 8, colPanel)
	lv := float32(math.Min(1, g.meter/0.85))
	mc := colAccent2
	if g.meter > 0.75 {
		mc = colAccent
	}
	rect(screen, mx+50, my+3, (sideW-50)*lv, 8, mc)
	cur := synth.Presets[g.preset]
	g.drawText(screen, cur.Name+": "+cur.Desc, g.face, float64(mx), float64(my+18), colDim, text.AlignStart)

	// Octave + pedal.
	g.button(screen, 24, ctlY, 34, 34, "◄", false)
	rect(screen, 62, ctlY, 130, 34, colPanel)
	g.drawText(screen, "Z = "+noteName(g.base), g.faceBold, 127, ctlY+8, colText, text.AlignCenter)
	g.button(screen, 196, ctlY, 34, 34, "►", false)
	pedalTxt := "SUSTAIN  (hold Space)"
	if g.pedalLock {
		pedalTxt = "SUSTAIN LATCHED (Enter)"
	}
	g.button(screen, 250, ctlY, 220, 34, pedalTxt, g.pedal)
	g.drawText(screen, fmt.Sprintf("%d voices", g.eng.ActiveVoices()), g.face, 490, ctlY+8, colDim, text.AlignStart)
	g.flashMu.Lock()
	fl, ft := g.flash, g.flashT
	g.flashMu.Unlock()
	if fl != "" && time.Since(ft) < 1800*time.Millisecond {
		g.drawText(screen, fl, g.faceBold, screenW-24, 22, colAccent, text.AlignEnd)
	}
	g.drawMIDI(screen)

	g.drawKeyboard(screen)

	help := "Lower row Z–/ and upper row Q–] play  ·  ← → octave  ·  ↑ ↓ touch  ·  Shift accent, Ctrl soft  ·  Space sustain  ·  Tab / PgUp PgDn sound  ·  F1–F4 category  ·  F5 MIDI velocity  ·  Esc all notes off"
	g.drawText(screen, help, g.face, screenW/2, float64(kbY+whiteH+30), colDim, text.AlignCenter)
}

const (
	midiX = 640
	midiW = screenW - 24 - midiX
)

func (g *Game) drawMIDI(screen *ebiten.Image) {
	rect(screen, midiX, ctlY, midiW, 34, colPanel)
	g.drawText(screen, "MIDI", g.faceSmall, midiX+12, ctlY+11, colDim, text.AlignStart)
	open, busy := g.midiIn.Devices()
	dot := color.Color(colPanelHi)
	if len(open) > 0 {
		dot = color.RGBA{0x4f, 0x8a, 0x5a, 0xff}
		if time.Since(g.midiFlashT) < 120*time.Millisecond {
			dot = color.RGBA{0x8c, 0xf0, 0x9a, 0xff}
		}
	}
	vector.DrawFilledCircle(screen, midiX+58, ctlY+17, 6, dot, true)
	msg, mc := "No MIDI keyboard found. Plug one in any time (F6 / click to rescan)", color.Color(colDim)
	if len(open) > 0 {
		msg, mc = strings.Join(open, " + "), colText
	}
	if len(busy) > 0 {
		msg, mc = busy[0]+" is in use by another program. Close it to play here", colAccent
		if len(open) > 0 {
			msg = strings.Join(open, " + ") + "   (" + busy[0] + " busy)"
		}
	}
	if r := []rune(msg); len(r) > 58 {
		msg = string(r[:57]) + "…"
	}
	g.drawText(screen, msg, g.face, midiX+74, ctlY+8, mc, text.AlignStart)
	extra := ""
	if g.midi.soft.Load() {
		extra = "SOFT  "
	}
	g.drawText(screen, extra+"Velocity: "+velCurves[g.midi.curve.Load()].name+" (F5)", g.faceSmall, midiX+midiW-12, ctlY+11, colAccent2, text.AlignEnd)
}

func (g *Game) button(dst *ebiten.Image, x, y, w, h float32, label string, on bool) {
	c := colPanel
	tc := colText
	if on {
		c = colAccent
		tc = colBG
	}
	rect(dst, x, y, w, h, c)
	g.drawText(dst, label, g.faceBold, float64(x+w/2), float64(y+8), tc, text.AlignCenter)
}

func (g *Game) labelFor(m int) (string, color.Color) {
	for row, keys := range [][]keyMap{lowerRow, upperRow} {
		for _, km := range keys {
			if g.base+12*row+km.offset == m {
				if row == 0 {
					return km.label, colAccent2
				}
				return km.label, colAccent
			}
		}
	}
	return "", nil
}

func (g *Game) drawKeyboard(screen *ebiten.Image) {
	// Felt strip and the keyboard's playable range from the computer keys.
	rect(screen, kbX-4, kbY-10, 52*whiteW+8, 10, colFelt)
	lo, hi := g.base, g.base+12+19
	if hi > topNote {
		hi = topNote
	}
	x0, x1 := g.keyX[lo], g.keyX[hi]
	if g.isBlack[hi] {
		x1 += blackW
	} else {
		x1 += whiteW
	}
	rect(screen, x0, kbY-14, x1-x0, 4, colAccent)

	for m := lowNote; m <= topNote; m++ {
		if g.isBlack[m] {
			continue
		}
		x := g.keyX[m]
		c := colIvory
		if g.down[m] > 0 || g.midi.down[m].Load() > 0 {
			c = color.RGBA{0xf0, 0xc2, 0x78, 0xff}
		}
		rect(screen, x+1, kbY, whiteW-2, whiteH, c)
		rect(screen, x+1, kbY+whiteH-8, whiteW-2, 8, colIvoryLo)
		if m%12 == 0 {
			g.drawText(screen, noteName(m), g.faceSmall, float64(x+whiteW/2), float64(kbY+whiteH-24), colDim, text.AlignCenter)
		}
		if l, lc := g.labelFor(m); l != "" {
			vector.DrawFilledCircle(screen, x+whiteW/2, kbY+whiteH-48, 9, lc, true)
			g.drawText(screen, l, g.faceSmall, float64(x+whiteW/2), float64(kbY+whiteH-55), colBG, text.AlignCenter)
		}
	}
	for m := lowNote; m <= topNote; m++ {
		if !g.isBlack[m] {
			continue
		}
		x := g.keyX[m]
		c, hiC := colEbony, colEbonyHi
		if g.down[m] > 0 || g.midi.down[m].Load() > 0 {
			c, hiC = color.RGBA{0xb0, 0x7a, 0x30, 0xff}, color.RGBA{0xd8, 0xa0, 0x50, 0xff}
		}
		rect(screen, x, kbY, blackW, blackH, c)
		rect(screen, x+2, kbY, blackW-4, blackH-8, hiC)
		rect(screen, x+3, kbY, blackW-6, blackH-12, c)
		if l, lc := g.labelFor(m); l != "" {
			vector.DrawFilledCircle(screen, x+blackW/2, kbY+blackH-22, 7, lc, true)
			g.drawText(screen, l, g.faceSmall, float64(x+blackW/2), float64(kbY+blackH-29), colBG, text.AlignCenter)
		}
	}
}

func (g *Game) Layout(w, h int) (int, int) { return screenW, screenH }

func main() {
	ebiten.SetWindowSize(screenW, screenH)
	ebiten.SetWindowTitle("Piano Player")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowIcon([]image.Image{appicon.Image(16), appicon.Image(32), appicon.Image(48)})
	ebiten.SetTPS(240) // poll the keyboard often for low latency
	g := newGame()
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
