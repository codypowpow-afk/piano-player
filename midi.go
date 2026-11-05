package main

import (
	"math"
	"sync/atomic"
)

// midiInput is a platform MIDI input backend.
type midiInput interface {
	Rescan()
	Devices() (open []string, busy []string)
	Close()
}

// Velocity curves for MIDI keyboards (how hard you must play for a given loudness).
var velCurves = []struct {
	name  string
	gamma float64
}{
	{"Light", 0.65}, // light keys / soft player: loud comes easily
	{"Normal", 1.0},
	{"Heavy", 1.5}, // need to dig in for forte
}

// midiState is shared between the MIDI callback thread and the UI.
type midiState struct {
	down     [128]atomic.Int32 // notes sounding from the MIDI keyboard (after octave shift)
	playing  [128]atomic.Int32 // physical MIDI key -> note it started + 1 (0 = none)
	octave   atomic.Int32      // octave shift applied to the MIDI keyboard, -4..+4
	sustain  atomic.Bool
	soft     atomic.Bool
	activity atomic.Int64 // counts incoming notes (activity light)
	curve    atomic.Int32
}

// handleMIDI turns raw MIDI bytes into piano actions. It runs on the MIDI thread and talks to
// the audio engine directly for the lowest possible latency.
func (g *Game) handleMIDI(status, d1, d2 byte) {
	ms := &g.midi
	switch status & 0xF0 {
	case 0x90:
		if d2 > 0 {
			v := math.Pow(float64(d2)/127, velCurves[ms.curve.Load()].gamma)
			if ms.soft.Load() {
				v *= 0.72 // una corda: softer and darker
			}
			if d1 >= 128 {
				return
			}
			n := int(d1) + 12*int(ms.octave.Load())
			if n < 0 || n > 127 {
				return
			}
			// A key held while the octave changes must still release the note it started.
			if old := ms.playing[d1].Swap(int32(n) + 1); old > 0 && ms.down[old-1].Load() > 0 {
				ms.down[old-1].Store(0)
				g.eng.NoteOff(int(old - 1))
			}
			ms.down[n].Store(1)
			ms.activity.Add(1)
			g.eng.NoteOn(n, v)
			return
		}
		fallthrough
	case 0x80:
		if d1 >= 128 {
			return
		}
		if n := ms.playing[d1].Swap(0) - 1; n >= 0 && ms.down[n].Load() > 0 {
			ms.down[n].Store(0)
			g.eng.NoteOff(int(n))
		}
	case 0xC0: // program change picks a sound preset
		g.midiProgram.Store(int32(d1) + 1)
	case 0xB0:
		switch d1 {
		case 64: // sustain (damper) pedal
			ms.sustain.Store(d2 >= 64)
			g.eng.SetPedal(d2 >= 64 || g.kbPedal.Load())
		case 67: // soft pedal
			ms.soft.Store(d2 >= 64)
		case 120, 123: // all sound / all notes off
			for i := range ms.playing {
				ms.playing[i].Store(0)
			}
			for i := range ms.down {
				if ms.down[i].Swap(0) > 0 {
					g.eng.NoteOff(i)
				}
			}
		}
	}
}

// shiftMIDIOctave moves the MIDI keyboard up or down by octaves (-4..+4).
func (g *Game) shiftMIDIOctave(d int) {
	o := int(g.midi.octave.Load()) + d
	if o < -4 || o > 4 {
		return
	}
	g.midi.octave.Store(int32(o))
	g.toast("MIDI keyboard octave " + signed(o))
}

func signed(o int) string {
	if o > 0 {
		return "+" + string(rune('0'+o))
	}
	if o < 0 {
		return "-" + string(rune('0'-o))
	}
	return "0"
}
