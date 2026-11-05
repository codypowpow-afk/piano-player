package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/codypowpow-afk/piano-player/synth"
)

const arpY = 300 // arpeggiator control strip

// arpCtl is one control on the arpeggiator strip: a toggle, a button, or a ◄ value ► stepper.
type arpCtl struct {
	label  string
	x, w   int
	value  func() string
	on     func() bool // toggles: lit when true
	inc    func(d int) // steppers
	click  func()      // toggles / buttons
	accent bool
}

func (c *arpCtl) side(x int) int {
	if c.inc == nil {
		return 0
	}
	if x < c.x+c.w/3 {
		return -1
	}
	return 1 // middle and right step forward
}

func (c *arpCtl) act(side int) {
	if c.inc != nil {
		if side == 0 {
			side = 1
		}
		c.inc(side)
	} else if c.click != nil {
		c.click()
	}
}

func wrap(i, n int) int { return ((i % n) + n) % n }

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func (g *Game) arpChanged() { g.eng.SetArp(g.arp) }

func (g *Game) tapTempo() {
	now := time.Now()
	if len(g.taps) > 0 && now.Sub(g.taps[len(g.taps)-1]) > 2*time.Second {
		g.taps = nil
	}
	g.taps = append(g.taps, now)
	if len(g.taps) > 5 {
		g.taps = g.taps[len(g.taps)-5:]
	}
	if len(g.taps) >= 2 {
		span := g.taps[len(g.taps)-1].Sub(g.taps[0]).Seconds() / float64(len(g.taps)-1)
		g.arp.BPM = clampF(math.Round(60/span), 30, 300)
		g.toast(fmt.Sprintf("Tempo %d BPM", int(g.arp.BPM)))
	}
}

func (g *Game) buildArpControls() {
	a := &g.arp
	x := 24
	add := func(c *arpCtl) {
		c.x = x
		x += c.w + 8
		g.arpCtls = append(g.arpCtls, c)
	}
	add(&arpCtl{label: "ARPEGGIATOR  F7", w: 118, accent: true,
		value: func() string {
			if a.On {
				return "ON"
			}
			return "OFF"
		},
		on: func() bool { return a.On }, click: func() { a.On = !a.On }})
	add(&arpCtl{label: "PATTERN  F8", w: 168, value: func() string { return synth.ArpModes[a.Mode] },
		inc: func(d int) { a.Mode = wrap(a.Mode+d, len(synth.ArpModes)) }})
	add(&arpCtl{label: "RATE", w: 112, value: func() string { return synth.ArpRates[a.Rate].Name },
		inc: func(d int) { a.Rate = wrap(a.Rate+d, len(synth.ArpRates)) }})
	add(&arpCtl{label: "TEMPO", w: 132, value: func() string { return fmt.Sprintf("%d BPM", int(a.BPM)) },
		inc: func(d int) {
			step := 1.0
			if ebiten.IsKeyPressed(ebiten.KeyShift) {
				step = 10
			}
			a.BPM = clampF(a.BPM+float64(d)*step, 30, 300)
		}})
	add(&arpCtl{label: "TAP", w: 58, value: func() string { return "TAP" }, click: g.tapTempo})
	add(&arpCtl{label: "OCTAVES", w: 104, value: func() string { return fmt.Sprint(a.Octaves) },
		inc: func(d int) { a.Octaves = int(clampF(float64(a.Octaves+d), 1, 4)) }})
	add(&arpCtl{label: "GATE", w: 112, value: func() string { return fmt.Sprintf("%d%%", int(math.Round(a.Gate*100))) },
		inc: func(d int) { a.Gate = clampF(math.Round(a.Gate*10+float64(d))/10, 0.1, 2) }})
	add(&arpCtl{label: "SWING", w: 112, value: func() string { return fmt.Sprintf("%d%%", int(math.Round(a.Swing*100))) },
		inc: func(d int) { a.Swing = clampF(math.Round(a.Swing*20+float64(d))/20, 0, 0.6) }})
	add(&arpCtl{label: "LATCH  F9", w: 96, value: func() string { return onOff(a.Latch) },
		on: func() bool { return a.Latch }, click: func() { a.Latch = !a.Latch }})
	add(&arpCtl{label: "ACCENT", w: 96, value: func() string { return onOff(a.Accent) },
		on: func() bool { return a.Accent }, click: func() { a.Accent = !a.Accent }})
	add(&arpCtl{label: "PRESETS", w: 150, value: func() string { return arpRecipes[g.recipe].name },
		inc: func(d int) {
			g.recipe = wrap(g.recipe+d, len(arpRecipes))
			on := a.On
			*a = arpRecipes[g.recipe].s
			a.On = on || g.recipe > 0
		}})
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// Ready-made arpeggiator setups.
var arpRecipes = []struct {
	name string
	s    synth.ArpSettings
}{
	{"Default", synth.DefaultArp()},
	{"Classic Alberti", synth.ArpSettings{Mode: 8, Rate: 3, BPM: 96, Octaves: 1, Gate: 0.9}},
	{"Rolling 8ths", synth.ArpSettings{Mode: 2, Rate: 1, BPM: 92, Octaves: 2, Gate: 1.1}},
	{"Harp Glissando", synth.ArpSettings{Mode: 0, Rate: 5, BPM: 100, Octaves: 4, Gate: 1.6}},
	{"Swing Triplets", synth.ArpSettings{Mode: 2, Rate: 2, BPM: 120, Octaves: 2, Gate: 0.7, Accent: true}},
	{"Dreamy Random", synth.ArpSettings{Mode: 5, Rate: 1, BPM: 70, Octaves: 3, Gate: 1.8, Latch: true}},
	{"Stabbing Chords", synth.ArpSettings{Mode: 7, Rate: 1, BPM: 124, Octaves: 1, Gate: 0.35, Accent: true}},
	{"Funky 16ths", synth.ArpSettings{Mode: 3, Rate: 3, BPM: 104, Octaves: 2, Gate: 0.45, Swing: 0.2, Accent: true}},
	{"Thumb Ostinato", synth.ArpSettings{Mode: 11, Rate: 3, BPM: 110, Octaves: 1, Gate: 0.8}},
	{"Cascade", synth.ArpSettings{Mode: 9, Rate: 4, BPM: 90, Octaves: 3, Gate: 1.2}},
}

func (g *Game) drawArp(screen *ebiten.Image) {
	if b := g.eng.ArpBeat(); b != g.arpBeat {
		g.arpBeat, g.arpBeatT = b, time.Now()
	}
	for _, c := range g.arpCtls {
		x, w := float32(c.x), float32(c.w)
		lit := c.on != nil && c.on()
		bg := colPanel
		if lit {
			bg = colPanelHi
		}
		rect(screen, x, arpY, w, 34, bg)
		lc := colDim
		vc := colText
		if c.on != nil && !lit {
			vc = colDim
		}
		if lit {
			ac := colAccent2
			if c.accent {
				ac = colAccent
			}
			rect(screen, x, arpY+31, w, 3, ac)
			vc = ac
		}
		g.drawText(screen, c.label, g.faceTiny, float64(x+8), arpY+3, lc, text.AlignStart)
		g.drawText(screen, c.value(), g.faceBtn, float64(x+w/2), arpY+14, vc, text.AlignCenter)
		if c.inc != nil {
			g.drawText(screen, "◄", g.faceTiny, float64(x+7), arpY+17, colDim, text.AlignStart)
			g.drawText(screen, "►", g.faceTiny, float64(x+w-7), arpY+17, colDim, text.AlignEnd)
		}
		if c.accent && g.arp.On {
			// beat light
			bc := color.RGBA{0x55, 0x4a, 0x3a, 0xff}
			if time.Since(g.arpBeatT) < 90*time.Millisecond {
				bc = color.RGBA{0xff, 0xd0, 0x80, 0xff}
			}
			vector.DrawFilledCircle(screen, x+w-12, arpY+21, 5, bc, true)
		}
	}
}
