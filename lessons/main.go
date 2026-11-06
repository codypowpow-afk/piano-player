// Piano Lessons: turns sheet music (PDF scans, page images, MIDI or MusicXML)
// into falling notes over an 88-key piano, and waits for you to play each
// note before moving on. Sound comes from the Piano Player sampler engine.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	"github.com/codypowpow-afk/piano-player/internal/lesson"
	"github.com/codypowpow-afk/piano-player/internal/midiin"
	"github.com/codypowpow-afk/piano-player/score"
	"github.com/codypowpow-afk/piano-player/synth"
)

const (
	screenW = 1400
	screenH = 884

	kbX     = 24
	kbY     = 650
	whiteW  = 26
	whiteH  = 168
	blackW  = 16
	blackH  = 106
	lowNote = 21
	topNote = 108

	barY    = 8
	ctlY    = 58
	progY   = 106
	fallTop = 124
)

type keyMap struct {
	key    ebiten.Key
	offset int
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
	colFall    = color.RGBA{0x1d, 0x1b, 0x1a, 0xff}
	colPanel   = color.RGBA{0x24, 0x21, 0x1f, 0xff}
	colPanelHi = color.RGBA{0x33, 0x2e, 0x2a, 0xff}
	colText    = color.RGBA{0xee, 0xe6, 0xda, 0xff}
	colDim     = color.RGBA{0x9a, 0x90, 0x84, 0xff}
	colFaint   = color.RGBA{0x2c, 0x29, 0x26, 0xff}
	colAccent  = color.RGBA{0xe0, 0xa9, 0x58, 0xff}
	colIvory   = color.RGBA{0xf6, 0xf0, 0xe4, 0xff}
	colIvoryLo = color.RGBA{0xd9, 0xd0, 0xc0, 0xff}
	colEbony   = color.RGBA{0x14, 0x12, 0x11, 0xff}
	colEbonyHi = color.RGBA{0x3a, 0x35, 0x31, 0xff}
	colFelt    = color.RGBA{0x7a, 0x1f, 0x24, 0xff}
	colGood    = color.RGBA{0x6c, 0xd0, 0x7a, 0xff}
	colBad     = color.RGBA{0xe0, 0x55, 0x4a, 0xff}

	// hand colours: right = teal, left = amber
	colHand   = [2]color.RGBA{{0x4f, 0xc3, 0xd9, 0xff}, {0xf0, 0x9a, 0x45, 0xff}}
	colHandLo = [2]color.RGBA{{0x2a, 0x5a, 0x66, 0xff}, {0x6e, 0x4a, 0x2a, 0xff}}
)

type uiBtn struct {
	x, y, w, h float32
	label      string
	on, wide   bool
	act        func()
	tip        string
}

type Game struct {
	eng    *synth.Engine
	player *audio.Player
	midiIn midiin.Input

	lib      []*LibEntry
	entry    *LibEntry
	piece    int
	lesson   *lesson.Lesson
	preset   int
	showLib  bool
	libHover int
	names    bool
	ppb      float64 // pixels per beat in the waterfall
	pctTempo float64

	base    int
	held    map[ebiten.Key]int
	down    [128]int // learner keys held (any source)
	mouseNt int

	presses  chan int // MIDI thread -> lesson
	midiDown [128]bool
	midiMu   sync.Mutex
	pedal    bool

	prog    importProgress
	results chan loadResult

	ui    []uiBtn
	last  time.Time
	pulse float64
	keyX  [128]float32
	black [128]bool

	face, faceSmall, faceBold, faceTitle, faceTiny text.Face
	toastMsg                                       string
	toastT                                         time.Time
}

func isBlackNote(m int) bool {
	switch m % 12 {
	case 1, 3, 6, 8, 10:
		return true
	}
	return false
}

func newGame() *Game {
	g := &Game{
		eng:      synth.NewEngine(assets.FS),
		base:     48,
		held:     map[ebiten.Key]int{},
		mouseNt:  -1,
		ppb:      110,
		presses:  make(chan int, 256),
		results:  make(chan loadResult, 4),
		names:    true,
		pctTempo: 1,
	}
	wi := 0
	for m := lowNote; m <= topNote; m++ {
		if isBlackNote(m) {
			g.black[m] = true
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
	g.face = &text.GoTextFace{Source: reg, Size: 14}
	g.faceSmall = &text.GoTextFace{Source: bold, Size: 11}
	g.faceBold = &text.GoTextFace{Source: bold, Size: 14}
	g.faceTitle = &text.GoTextFace{Source: bold, Size: 20}
	g.faceTiny = &text.GoTextFace{Source: bold, Size: 9}

	ctx := audio.NewContext(synth.SampleRate)
	p, err := ctx.NewPlayerF32(g.eng)
	if err != nil {
		log.Fatal(err)
	}
	p.SetBufferSize(20 * time.Millisecond)
	p.Play()
	g.player = p
	g.eng.Volume = 0.85
	g.eng.Reverb = 0.9
	g.setPreset(0)
	g.midiIn = midiin.Start(g.handleMIDI)

	g.lib = loadLibrary()
	if len(g.lib) > 0 {
		g.openEntry(g.lib[0], 1) // No. 2 (E-flat) is the famous one
	}
	if len(os.Args) > 1 {
		g.importPaths(os.Args[1:])
	}
	g.last = time.Now()
	return g
}

func (g *Game) toast(s string) {
	g.toastMsg = s
	g.toastT = time.Now()
}

func (g *Game) setPreset(i int) {
	n := len(synth.Presets)
	i = ((i % n) + n) % n
	g.preset = i
	go func() {
		if _, err := g.eng.Load(i); err == nil && g.preset == i {
			g.eng.SetPreset(i)
		}
	}()
}

func (g *Game) openEntry(e *LibEntry, piece int) {
	if e == nil || len(e.Songs) == 0 {
		return
	}
	if piece < 0 || piece >= len(e.Songs) {
		piece = 0
	}
	g.entry = e
	g.piece = piece
	g.eng.AllNotesOff()
	old := g.lesson
	l := lesson.NewLesson(e.Songs[piece], g.eng)
	if old != nil {
		l.Wait = old.Wait
		l.Hands = old.Hands
		l.Rebuild()
	}
	l.Tempo = l.Song.Tempo * g.pctTempo
	l.Restart()
	g.lesson = l
	g.showLib = false
}

func (g *Game) importPaths(paths []string) {
	files, err := readPaths(paths)
	if err != nil {
		g.toast(err.Error())
		return
	}
	g.importFiles(files)
}

func (g *Game) importFiles(files []inFile) {
	if a, _, _ := g.prog.get(); a {
		g.toast("Still reading the previous file…")
		return
	}
	g.prog.set(true, "Opening "+filepath.Base(files[0].name)+"…", 0, 1)
	go func() {
		e, err := importFiles(files, &g.prog)
		g.prog.set(false, "", 0, 0)
		g.results <- loadResult{e, err}
	}()
}

// handleMIDI runs on the MIDI thread: sound immediately, tell the lesson later.
func (g *Game) handleMIDI(status, d1, d2 byte) {
	switch status & 0xF0 {
	case 0x90:
		if d2 > 0 && d1 < 128 {
			g.eng.NoteOn(int(d1), math.Pow(float64(d2)/127, 1.0))
			g.midiMu.Lock()
			g.midiDown[d1] = true
			g.midiMu.Unlock()
			select {
			case g.presses <- int(d1):
			default:
			}
			return
		}
		fallthrough
	case 0x80:
		if d1 < 128 {
			g.eng.NoteOff(int(d1))
			g.midiMu.Lock()
			g.midiDown[d1] = false
			g.midiMu.Unlock()
		}
	case 0xB0:
		if d1 == 64 {
			g.eng.SetPedal(d2 >= 64)
		}
	}
}

func (g *Game) playKey(m int, vel float64) {
	if m < lowNote || m > topNote {
		return
	}
	g.down[m]++
	g.eng.NoteOn(m, vel)
	if g.lesson != nil {
		g.lesson.Press(m)
	}
}

func (g *Game) releaseKey(m int) {
	if m < 0 || m > 127 || g.down[m] == 0 {
		return
	}
	g.down[m]--
	if g.down[m] == 0 {
		g.eng.NoteOff(m)
	}
}

func (g *Game) song() *score.Song {
	if g.lesson == nil {
		return nil
	}
	return g.lesson.Song
}

func (g *Game) setTempoPct(p float64) {
	g.pctTempo = math.Max(0.25, math.Min(1.5, p))
	if g.lesson != nil {
		g.lesson.Tempo = g.lesson.Song.Tempo * g.pctTempo
	}
}

func (g *Game) togglePlay() {
	l := g.lesson
	if l == nil {
		return
	}
	if l.Finished {
		l.Restart()
	}
	l.Play = !l.Play
	if !l.Play {
		l.Silence()
	}
}

func (g *Game) seekMeasure(d int) {
	s := g.song()
	if s == nil || len(s.Measures) == 0 {
		return
	}
	m := s.MeasureAt(math.Max(g.lesson.Pos, 0)) + d
	m = max(0, min(len(s.Measures)-1, m))
	g.lesson.Seek(s.Measures[m] - 0.001)
}

func (g *Game) exportMIDI() {
	s := g.song()
	if s == nil {
		return
	}
	dir := exportDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		g.toast(err.Error())
		return
	}
	p := filepath.Join(dir, sanitize(s.Title)+".mid")
	if err := os.WriteFile(p, score.WriteMIDI(s), 0o644); err != nil {
		g.toast(err.Error())
		return
	}
	g.toast("Saved " + p)
}

func (g *Game) openDialog() {
	if !hasDialog {
		g.toast("Drag a PDF, image, MIDI or MusicXML file onto the window")
		return
	}
	go func() {
		paths, ok := openFileDialog()
		if !ok || len(paths) == 0 {
			return
		}
		files, err := readPaths(paths)
		if err != nil {
			g.results <- loadResult{nil, err}
			return
		}
		e, err := importFiles(files, &g.prog)
		g.prog.set(false, "", 0, 0)
		g.results <- loadResult{e, err}
	}()
}

// ---------------------------------------------------------------- update

func (g *Game) Update() error {
	now := time.Now()
	dt := math.Min(now.Sub(g.last).Seconds(), 0.05)
	g.last = now
	g.pulse += dt

	select {
	case r := <-g.results:
		if r.err != nil {
			g.toast("Couldn't read that file: " + r.err.Error())
		} else {
			g.lib = append(g.lib, r.entry)
			g.openEntry(r.entry, 0)
			n := 0
			for _, s := range r.entry.Songs {
				n += len(s.Notes)
			}
			msg := fmt.Sprintf("Loaded %s — %d notes", r.entry.Title, n)
			if len(r.entry.Songs) > 1 {
				msg += fmt.Sprintf(" in %d pieces", len(r.entry.Songs))
			}
			g.toast(msg)
		}
	default:
	}
	for done := false; !done; {
		select {
		case p := <-g.presses:
			if g.lesson != nil {
				g.lesson.Press(p)
			}
		default:
			done = true
		}
	}
	if fsys := ebiten.DroppedFiles(); fsys != nil {
		if files := readDropped(fsys); len(files) > 0 {
			g.importFiles(files)
		}
	}

	g.keys()
	g.mouse()
	if g.lesson != nil {
		g.lesson.Advance(dt)
	}
	g.buildUI()
	return nil
}

func (g *Game) keys() {
	l := g.lesson
	jp := inpututil.IsKeyJustPressed
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl) || ebiten.IsKeyPressed(ebiten.KeyMeta)
	if ctrl && jp(ebiten.KeyO) {
		g.openDialog()
		return
	}
	if g.showLib && jp(ebiten.KeyEscape) {
		g.showLib = false
		return
	}
	if l != nil {
		switch {
		case jp(ebiten.KeySpace):
			g.togglePlay()
		case jp(ebiten.KeyHome):
			l.Restart()
		case jp(ebiten.KeyTab):
			l.Skip()
		case jp(ebiten.KeyF1):
			l.SetHands((l.Hands + 1) % 3)
			g.toast("You play: " + lesson.HandsNames[l.Hands])
		case jp(ebiten.KeyF2):
			l.Wait = !l.Wait
		case jp(ebiten.KeyF3):
			l.SetListen(!l.Listen)
		case jp(ebiten.KeyF4):
			l.SetLoop(!l.LoopOn)
		case jp(ebiten.KeyArrowUp):
			g.setTempoPct(g.pctTempo + 0.05)
		case jp(ebiten.KeyArrowDown):
			g.setTempoPct(g.pctTempo - 0.05)
		case jp(ebiten.KeyPageUp):
			g.seekMeasure(-1)
		case jp(ebiten.KeyPageDown):
			g.seekMeasure(1)
		case jp(ebiten.KeyF7):
			g.setPreset(g.preset - 1)
			g.toast(synth.Presets[g.preset].Name)
		case jp(ebiten.KeyF8):
			g.setPreset(g.preset + 1)
			g.toast(synth.Presets[g.preset].Name)
		case jp(ebiten.KeyF9):
			g.names = !g.names
		case jp(ebiten.KeyF10):
			g.showLib = !g.showLib
		case jp(ebiten.KeyMinus):
			g.ppb = math.Max(40, g.ppb/1.15)
		case jp(ebiten.KeyEqual) && ctrl:
			g.ppb = math.Min(320, g.ppb*1.15)
		case jp(ebiten.KeyEscape):
			l.Play = false
			l.Silence()
			g.eng.AllNotesOff()
		}
	}
	if jp(ebiten.KeyArrowLeft) && g.base > 24 {
		g.base -= 12
		g.releaseAll()
	}
	if jp(ebiten.KeyArrowRight) && g.base < 84 {
		g.base += 12
		g.releaseAll()
	}
	if ctrl {
		return
	}
	for row, keys := range [][]keyMap{lowerRow, upperRow} {
		for _, km := range keys {
			if jp(km.key) {
				n := g.base + 12*row + km.offset
				g.held[km.key] = n
				g.playKey(n, 0.68)
			} else if inpututil.IsKeyJustReleased(km.key) {
				if n, ok := g.held[km.key]; ok {
					delete(g.held, km.key)
					g.releaseKey(n)
				}
			}
		}
	}
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)
	if shift != g.pedal {
		g.pedal = shift
		g.eng.SetPedal(shift)
	}
}

func (g *Game) releaseAll() {
	for k, n := range g.held {
		delete(g.held, k)
		g.releaseKey(n)
	}
}

func inRect(x, y int, rx, ry, rw, rh float32) bool {
	fx, fy := float32(x), float32(y)
	return fx >= rx && fx < rx+rw && fy >= ry && fy < ry+rh
}

func (g *Game) noteAt(x, y int) int {
	fx, fy := float32(x), float32(y)
	if fy < kbY || fy > kbY+whiteH {
		return -1
	}
	if fy < kbY+blackH {
		for m := lowNote; m <= topNote; m++ {
			if g.black[m] && fx >= g.keyX[m] && fx < g.keyX[m]+blackW {
				return m
			}
		}
	}
	for m := lowNote; m <= topNote; m++ {
		if !g.black[m] && fx >= g.keyX[m] && fx < g.keyX[m]+whiteW {
			return m
		}
	}
	return -1
}

func (g *Game) mouse() {
	x, y := ebiten.CursorPosition()
	pressed := inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	if g.showLib {
		g.libHover = -1
		lx, ly, lw := float32(330), float32(150), float32(740)
		for i := range g.lib {
			ry := ly + 70 + float32(i)*44
			if inRect(x, y, lx+20, ry, lw-40, 40) {
				g.libHover = i
				if pressed {
					g.openEntry(g.lib[i], 0)
				}
			}
		}
		if pressed {
			for _, b := range g.ui {
				if inRect(x, y, b.x, b.y, b.w, b.h) && b.act != nil && b.label == "Open file…" {
					b.act()
					return
				}
			}
			if !inRect(x, y, lx, ly, lw, float32(110+44*len(g.lib))) {
				g.showLib = false
			}
		}
		return
	}
	if pressed {
		for _, b := range g.ui {
			if inRect(x, y, b.x, b.y, b.w, b.h) && b.act != nil {
				b.act()
				return
			}
		}
		// progress bar seek
		if s := g.song(); s != nil && inRect(x, y, kbX, progY-4, 52*whiteW, 20) {
			f := float64(float32(x)-kbX) / float64(52*whiteW)
			if len(s.Measures) > 0 {
				m := int(f * float64(len(s.Measures)))
				m = max(0, min(len(s.Measures)-1, m))
				g.lesson.Seek(s.Measures[m] - 0.001)
			}
			return
		}
	}
	// piano keys with the mouse
	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		n := g.noteAt(x, y)
		if n != g.mouseNt {
			if g.mouseNt >= 0 {
				g.releaseKey(g.mouseNt)
			}
			if n >= 0 && (pressed || g.mouseNt >= 0) {
				g.playKey(n, 0.68)
				g.mouseNt = n
			} else {
				g.mouseNt = -1
			}
		}
	} else if g.mouseNt >= 0 {
		g.releaseKey(g.mouseNt)
		g.mouseNt = -1
	}
	// wheel over the waterfall scrolls through the song while paused
	if _, wy := ebiten.Wheel(); wy != 0 && g.lesson != nil && y > fallTop && y < kbY {
		if !g.lesson.Play {
			g.lesson.Seek(g.lesson.Pos + wy*0.5)
		} else {
			g.ppb = math.Max(40, math.Min(320, g.ppb*math.Pow(1.1, wy)))
		}
	}
}

func (g *Game) buildUI() {
	g.ui = g.ui[:0]
	l := g.lesson
	add := func(x, w float32, y float32, label string, on bool, act func()) {
		g.ui = append(g.ui, uiBtn{x: x, y: y, w: w, h: 32, label: label, on: on, act: act})
	}
	// top bar
	right := float32(screenW - 24)
	add(right-120, 120, barY+6, "Open file…", false, g.openDialog)
	add(right-250, 122, barY+6, "Library (F10)", g.showLib, func() { g.showLib = !g.showLib })
	add(right-380, 122, barY+6, "Export MIDI", false, g.exportMIDI)
	if g.entry != nil && len(g.entry.Songs) > 1 {
		add(right-560, 32, barY+6, "◄", false, func() { g.openEntry(g.entry, (g.piece+len(g.entry.Songs)-1)%len(g.entry.Songs)) })
		add(right-424, 32, barY+6, "►", false, func() { g.openEntry(g.entry, (g.piece+1)%len(g.entry.Songs)) })
	}
	if l == nil {
		return
	}
	x := float32(kbX)
	y := float32(ctlY)
	playLbl := "► Play"
	if l.Play {
		playLbl = "Pause"
	}
	add(x, 104, y, playLbl, l.Play, g.togglePlay)
	x += 110
	add(x, 84, y, "Restart", false, l.Restart)
	x += 90
	waitLbl := "Wait: on"
	if !l.Wait {
		waitLbl = "Wait: off"
	}
	add(x, 92, y, waitLbl, l.Wait, func() { l.Wait = !l.Wait })
	x += 98
	add(x, 120, y, lesson.HandsNames[l.Hands], l.Hands != lesson.HandsBoth, func() { l.SetHands((l.Hands + 1) % 3) })
	x += 126
	add(x, 74, y, "Listen", l.Listen, func() { l.SetListen(!l.Listen) })
	x += 80
	loopLbl := fmt.Sprintf("Loop %d bars", l.LoopBars)
	add(x, 112, y, loopLbl, l.LoopOn, func() { l.SetLoop(!l.LoopOn) })
	x += 112
	add(x, 26, y, "▼", false, func() {
		l.LoopBars = map[int]int{1: 2, 2: 4, 4: 8, 8: 1}[l.LoopBars]
		if l.LoopOn {
			l.SetLoop(true)
		}
	})
	x += 40
	add(x, 30, y, "−", false, func() { g.setTempoPct(g.pctTempo - 0.05) })
	add(x+130, 30, y, "+", false, func() { g.setTempoPct(g.pctTempo + 0.05) })
	x += 170
	add(x, 68, y, "Skip", false, l.Skip)
	x += 74
	add(x, 74, y, "Names", g.names, func() { g.names = !g.names })
	x += 88
	add(x, 30, y, "◄", false, func() { g.setPreset(g.preset - 1) })
	add(right-30, 30, y, "►", false, func() { g.setPreset(g.preset + 1) })
}

// ---------------------------------------------------------------- draw

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
	l := g.lesson
	// title
	g.drawText(screen, "PIANO LESSONS", g.faceSmall, kbX, barY+4, colAccent, text.AlignStart)
	title := "Drop sheet music here"
	if s := g.song(); s != nil {
		title = s.Title
	}
	if r := []rune(title); len(r) > 58 {
		title = string(r[:57]) + "…"
	}
	g.drawText(screen, title, g.faceTitle, kbX, barY+18, colText, text.AlignStart)
	if g.entry != nil && len(g.entry.Songs) > 1 {
		g.drawText(screen, fmt.Sprintf("Piece %d of %d", g.piece+1, len(g.entry.Songs)), g.faceBold, screenW-24-492, barY+14, colText, text.AlignCenter)
	}
	for _, b := range g.ui {
		g.button(screen, b)
	}
	if l != nil {
		// tempo readout between − and +
		for _, b := range g.ui {
			if b.label == "−" {
				g.drawText(screen, fmt.Sprintf("Tempo %d%%", int(math.Round(g.pctTempo*100))), g.faceBold, float64(b.x+80), ctlY+8, colText, text.AlignCenter)
			}
			if b.label == "◄" && b.y == ctlY {
				name := synth.Presets[g.preset].Name
				g.drawText(screen, name, g.faceBold, float64(b.x+30+(screenW-24-30-b.x-30)/2), ctlY+8, colText, text.AlignCenter)
			}
		}
		g.drawProgress(screen)
		g.drawFall(screen)
	}
	g.drawKeyboard(screen)
	g.drawStatus(screen)
	if g.showLib {
		g.drawLibrary(screen)
	}
	if a, msg, f := g.prog.get(); a {
		g.drawImport(screen, msg, f)
	}
	if l != nil && l.Finished {
		g.drawFinished(screen)
	}
	if g.toastMsg != "" && time.Since(g.toastT) < 3500*time.Millisecond {
		w, _ := text.Measure(g.toastMsg, g.faceBold, 0)
		bx := float32(screenW/2) - float32(w)/2 - 14
		rect(screen, bx, fallTop+14, float32(w)+28, 30, colPanelHi)
		g.drawText(screen, g.toastMsg, g.faceBold, screenW/2, fallTop+21, colText, text.AlignCenter)
	}
}

func (g *Game) button(dst *ebiten.Image, b uiBtn) {
	c, tc := colPanel, colText
	if b.on {
		c, tc = colAccent, colBG
	}
	x, y := ebiten.CursorPosition()
	if !b.on && inRect(x, y, b.x, b.y, b.w, b.h) {
		c = colPanelHi
	}
	rect(dst, b.x, b.y, b.w, b.h, c)
	g.drawText(dst, b.label, g.faceBold, float64(b.x+b.w/2), float64(b.y+8), tc, text.AlignCenter)
}

func (g *Game) drawProgress(screen *ebiten.Image) {
	s := g.song()
	w := float32(52 * whiteW)
	rect(screen, kbX, progY, w, 8, colPanel)
	end := s.End()
	if end <= 0 {
		return
	}
	l := g.lesson
	if l.LoopOn {
		a := float32(l.LoopA/end) * w
		b := float32(l.LoopB/end) * w
		rect(screen, kbX+a, progY-3, b-a, 14, colPanelHi)
	}
	f := float32(math.Max(0, math.Min(1, l.Pos/end)))
	rect(screen, kbX, progY, w*f, 8, colAccent)
	vector.DrawFilledCircle(screen, kbX+w*f, progY+4, 6, colText, true)
}

func (g *Game) noteRect(p int) (float32, float32) {
	if g.black[p] {
		return g.keyX[p] + 1, blackW - 2
	}
	return g.keyX[p] + 3, whiteW - 6
}

func (g *Game) drawFall(screen *ebiten.Image) {
	l := g.lesson
	s := l.Song
	top, bot := float32(fallTop), float32(kbY-12)
	rect(screen, kbX, top, 52*whiteW, bot-top, colFall)
	// octave guide lines at each C, faint F lines
	for m := lowNote; m <= topNote; m++ {
		if m%12 == 0 {
			rect(screen, g.keyX[m], top, 1, bot-top, colFaint)
		}
	}
	ppb := float32(g.ppb)
	yOf := func(t float64) float32 { return bot - float32(t-l.Pos)*ppb }
	// measure lines
	for i, mt := range s.Measures {
		y := yOf(mt)
		if y < top || y > bot {
			continue
		}
		rect(screen, kbX, y, 52*whiteW, 1, colPanelHi)
		g.drawText(screen, fmt.Sprint(i+1), g.faceTiny, kbX+4, float64(y-12), colDim, text.AlignStart)
	}
	cur := l.Current()
	curSet := map[int]bool{}
	if cur >= 0 {
		for _, i := range l.Steps[cur].Notes {
			curSet[i] = true
		}
	}
	horizon := l.Pos + float64(bot-top)/g.ppb + 1
	for i := range s.Notes {
		n := &s.Notes[i]
		if n.Start > horizon {
			break
		}
		if n.Start+n.Dur < l.Pos-0.05 {
			continue
		}
		y0 := yOf(n.Start)
		y1 := yOf(n.Start + math.Max(n.Dur, 0.12))
		if y0 > bot {
			y0 = bot
		}
		if y1 < top {
			y1 = top
		}
		if y0 <= y1 {
			continue
		}
		x, w := g.noteRect(n.Pitch)
		h := min(n.Hand, 1)
		learner := l.Learner(n)
		c := colHand[h]
		if !learner {
			c = colHandLo[h]
		}
		rect(screen, x, y1, w, y0-y1, c)
		if curSet[i] {
			a := uint8(160 + 95*math.Abs(math.Sin(g.pulse*5)))
			if !l.Waiting {
				a = 255
			}
			vector.StrokeRect(screen, x-1, y1-1, w+2, y0-y1+2, 2, color.RGBA{0xff, 0xff, 0xff, a}, false)
		}
		if g.names && y0-y1 > 16 && learner {
			nm := score.PitchClassName(n.Pitch, s.KeyFifths)
			g.drawText(screen, nm, g.faceTiny, float64(x+w/2), float64(y0-13), colBG, text.AlignCenter)
		}
	}
	// hit line
	rect(screen, kbX, bot, 52*whiteW, 2, colAccent)
	if l.Waiting && cur >= 0 {
		msg := "Waiting for you — play the highlighted keys"
		w, _ := text.Measure(msg, g.faceBold, 0)
		rect(screen, kbX+8, top+8, float32(w)+20, 26, colPanelHi)
		g.drawText(screen, msg, g.faceBold, kbX+18, float64(top+13), colText, text.AlignStart)
	}
	if !l.Play && !l.Finished {
		g.drawText(screen, "Space = play   ·   scroll = move through the song", g.face, screenW/2, float64(top+60), colDim, text.AlignCenter)
	}
}

func (g *Game) keyState(m int) (color.Color, bool) {
	l := g.lesson
	g.midiMu.Lock()
	md := g.midiDown[m]
	g.midiMu.Unlock()
	pressed := md || g.down[m] > 0
	if l != nil {
		if l.WrongFlash[m] > 0 {
			return colBad, true
		}
		if pressed && l.GoodFlash[m] > -0.01 {
			return colGood, true
		}
		if cur := l.Current(); cur >= 0 && l.Steps[cur].Start-l.Pos < 1.0 {
			for _, i := range l.Steps[cur].Notes {
				if n := l.Song.Notes[i]; n.Pitch == m {
					return colHand[min(n.Hand, 1)], true
				}
			}
		}
		if h, on := l.SoundingHand(m); on {
			return colHandLo[min(h, 1)], true
		}
	}
	if pressed {
		return colAccent, true
	}
	return nil, false
}

func (g *Game) drawKeyboard(screen *ebiten.Image) {
	rect(screen, kbX-4, kbY-10, 52*whiteW+8, 10, colFelt)
	// computer-keyboard range
	lo, hi := g.base, g.base+31
	x0, x1 := g.keyX[max(lo, lowNote)], g.keyX[min(hi, topNote)]+whiteW
	rect(screen, x0, kbY-14, x1-x0, 3, colAccent)
	for m := lowNote; m <= topNote; m++ {
		if g.black[m] {
			continue
		}
		x := g.keyX[m]
		c := color.Color(colIvory)
		if kc, ok := g.keyState(m); ok {
			c = kc
		}
		rect(screen, x+1, kbY, whiteW-2, whiteH, c)
		rect(screen, x+1, kbY+whiteH-6, whiteW-2, 6, colIvoryLo)
		if m%12 == 0 {
			g.drawText(screen, score.NoteName(m), g.faceTiny, float64(x+whiteW/2), float64(kbY+whiteH-20), colDim, text.AlignCenter)
		}
	}
	for m := lowNote; m <= topNote; m++ {
		if !g.black[m] {
			continue
		}
		x := g.keyX[m]
		c := color.Color(colEbony)
		hiC := color.Color(colEbonyHi)
		if kc, ok := g.keyState(m); ok {
			c, hiC = kc, kc
		}
		rect(screen, x, kbY, blackW, blackH, c)
		rect(screen, x+2, kbY, blackW-4, blackH-8, hiC)
		rect(screen, x+3, kbY, blackW-6, blackH-12, c)
	}
}

func (g *Game) drawStatus(screen *ebiten.Image) {
	y := float64(kbY + whiteH + 12)
	l := g.lesson
	if l != nil {
		s := l.Song
		m := s.MeasureAt(math.Max(l.Pos, 0)) + 1
		st := fmt.Sprintf("Bar %d / %d    Right notes %d    Wrong %d", m, len(s.Measures), l.Correct, l.Wrong)
		if l.Skipped > 0 {
			st += fmt.Sprintf("    skipped %d", l.Skipped)
		}
		if l.Correct+l.Wrong > 0 {
			st += fmt.Sprintf("    accuracy %d%%", int(math.Round(l.Accuracy()*100)))
		}
		g.drawText(screen, st, g.faceBold, kbX, y, colText, text.AlignStart)
	}
	open, _ := g.midiIn.Devices()
	midi := "No MIDI keyboard — computer keys Z…/ and Q…] (← → octave) or mouse"
	mc := color.Color(colDim)
	if len(open) > 0 {
		midi = "MIDI: " + strings.Join(open, ", ")
		mc = colGood
	}
	g.drawText(screen, midi, g.face, screenW-24, y, mc, text.AlignEnd)
	help := "Space play/pause · Home restart · Tab skip a note · F1 hands · F2 wait · F3 listen · F4 loop · ↑↓ tempo · PgUp/PgDn bar · F7/F8 sound · F9 names · −/scroll zoom · Shift = pedal · Ctrl+O open"
	g.drawText(screen, help, g.faceSmall, screenW/2, y+22, colDim, text.AlignCenter)
}

func (g *Game) drawLibrary(screen *ebiten.Image) {
	lx, ly, lw := float32(330), float32(150), float32(740)
	lh := float32(110 + 44*len(g.lib))
	vector.DrawFilledRect(screen, 0, 0, screenW, screenH, color.RGBA{0, 0, 0, 0x90}, false)
	rect(screen, lx, ly, lw, lh, colPanel)
	g.drawText(screen, "Library", g.faceTitle, float64(lx+20), float64(ly+18), colText, text.AlignStart)
	g.drawText(screen, "Drop a PDF, page images, MIDI or MusicXML onto the window to add music", g.face, float64(lx+20), float64(ly+46), colDim, text.AlignStart)
	for i, e := range g.lib {
		ry := ly + 70 + float32(i)*44
		c := colPanelHi
		if i == g.libHover {
			c = colAccent
		}
		rect(screen, lx+20, ry, lw-40, 40, c)
		tc := colText
		if i == g.libHover {
			tc = colBG
		}
		g.drawText(screen, e.Title, g.faceBold, float64(lx+34), float64(ry+12), tc, text.AlignStart)
		info := fmt.Sprintf("%d piece", len(e.Songs))
		if len(e.Songs) != 1 {
			info += "s"
		}
		if e.Builtin {
			info += " · built in"
		}
		g.drawText(screen, info, g.face, float64(lx+lw-34), float64(ry+12), tc, text.AlignEnd)
	}
}

func (g *Game) drawImport(screen *ebiten.Image, msg string, f float64) {
	vector.DrawFilledRect(screen, 0, 0, screenW, screenH, color.RGBA{0, 0, 0, 0x90}, false)
	bx, by, bw := float32(400), float32(330), float32(600)
	rect(screen, bx, by, bw, 120, colPanel)
	g.drawText(screen, "Reading sheet music", g.faceTitle, screenW/2, float64(by+18), colText, text.AlignCenter)
	g.drawText(screen, msg, g.face, screenW/2, float64(by+52), colDim, text.AlignCenter)
	rect(screen, bx+30, by+84, bw-60, 10, colPanelHi)
	rect(screen, bx+30, by+84, (bw-60)*float32(f), 10, colAccent)
}

func (g *Game) drawFinished(screen *ebiten.Image) {
	l := g.lesson
	bx, by, bw := float32(450), float32(260), float32(500)
	rect(screen, bx, by, bw, 150, colPanel)
	g.drawText(screen, "Finished!", g.faceTitle, screenW/2, float64(by+20), colAccent, text.AlignCenter)
	msg := fmt.Sprintf("%d notes right, %d wrong — accuracy %d%%", l.Correct, l.Wrong, int(math.Round(l.Accuracy()*100)))
	if l.Listen {
		msg = "That was the whole piece."
	}
	g.drawText(screen, msg, g.faceBold, screenW/2, float64(by+62), colText, text.AlignCenter)
	g.drawText(screen, "Press Space to play again", g.face, screenW/2, float64(by+100), colDim, text.AlignCenter)
}

func (g *Game) Layout(w, h int) (int, int) { return screenW, screenH }

func main() {
	ebiten.SetWindowSize(screenW, screenH)
	ebiten.SetWindowTitle("Piano Lessons")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowIcon([]image.Image{appicon.Image(16), appicon.Image(32), appicon.Image(48)})
	ebiten.SetTPS(240)
	g := newGame()
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
